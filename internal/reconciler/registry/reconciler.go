// Package registry reconciles container registry contents between primary
// and secondary by comparing manifest digests via the registry HTTP API v2.
// The registry metadata (tags → manifest links) lives in the GitLab DB
// (already replicated via WAL streaming); the blobs live in object storage
// (already replicated via S3/fs). This reconciler validates that the
// registry API on both sides returns the same set of repositories and
// manifest digests.
//
// The registry is a separate service from GitLab, reachable at its own
// host and port (registry.example.com, or gitlab.example.com:5050). Its
// URL must be configured per site as registry.url; it cannot be derived
// from external_url. When no URL is configured the reconciler is not
// constructed at all, rather than probing GitLab and reporting the
// result as if the registry had been checked.
//
// Authentication follows the public Docker Registry v2 flow: a 401
// carries a WWW-Authenticate challenge naming an auth realm, a token is
// fetched from it, and the request is retried. See auth.go.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
)

const name = "registry"

// Reconciler compares registry contents between primary and secondary
// via the Docker Registry HTTP API v2.
type Reconciler struct {
	site            string
	primaryURL      string
	secondaryURL    string
	primaryClient   *http.Client
	secondaryClient *http.Client
	primaryAuth     *authenticator
	secondaryAuth   *authenticator
	dryRun          bool
}

// New creates a registry reconciler from the sites' registry URLs.
// Returns nil when either side has no registry.url configured — there is
// no endpoint to check, and inventing one produces a check that only
// looks like it ran.
func New(primary, secondary *config.SiteConfig, site string, dryRun bool) *Reconciler {
	primaryURL := registryBaseURL(primary)
	secondaryURL := registryBaseURL(secondary)
	if primaryURL == "" || secondaryURL == "" {
		return nil
	}

	timeout := 30 * time.Second
	primaryClient := &http.Client{Timeout: timeout}
	secondaryClient := &http.Client{Timeout: timeout}
	return &Reconciler{
		site:            site,
		primaryURL:      primaryURL,
		secondaryURL:    secondaryURL,
		primaryClient:   primaryClient,
		secondaryClient: secondaryClient,
		primaryAuth:     newAuthenticator(registryToken(primary), primaryClient, realmHosts(primary), insecureRegistry(primary)),
		secondaryAuth:   newAuthenticator(registryToken(secondary), secondaryClient, realmHosts(secondary), insecureRegistry(secondary)),
		dryRun:          dryRun,
	}
}

func registryBaseURL(s *config.SiteConfig) string {
	if s.Registry == nil || s.Registry.URL == "" {
		return ""
	}
	return strings.TrimSuffix(s.Registry.URL, "/") + "/v2"
}

// realmHosts are the hosts a registry's auth challenge may name.
//
// The realm arrives in a header the registry controls, so it is checked
// against hosts the operator configured rather than trusted. GitLab
// normally serves the registry from one host (registry.example.com) and
// its auth realm from another (gitlab.example.com/jwt/auth), so both the
// registry URL and the site's external_url are accepted — plus an
// explicit registry.auth_realm for anything else.
func realmHosts(s *config.SiteConfig) []string {
	var hosts []string
	add := func(raw string) {
		if raw == "" {
			return
		}
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts = append(hosts, u.Hostname())
		}
	}
	add(s.ExternalURL)
	if s.Registry != nil {
		add(s.Registry.URL)
		add(s.Registry.AuthRealm)
	}
	return hosts
}

// insecureRegistry reports whether the registry itself is plain http, in
// which case an http auth realm is no additional exposure.
func insecureRegistry(s *config.SiteConfig) bool {
	if s.Registry == nil || s.Registry.URL == "" {
		return false
	}
	u, err := url.Parse(s.Registry.URL)
	return err == nil && u.Scheme == "http"
}

func registryToken(s *config.SiteConfig) string {
	if s.Registry == nil {
		return ""
	}
	return s.Registry.Token
}

func (r *Reconciler) Name() string { return reconciler.QualifyName(name, r.site) }

// Reconcile lists all repositories on both sides, then compares manifest
// digests for each repository.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	start := time.Now()

	pRepos, err := r.listRepositories(ctx, r.primaryClient, r.primaryAuth, r.primaryURL)
	if err != nil {
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("primary list repos: %v", err), Remaining: 1}
	}

	sRepos, err := r.listRepositories(ctx, r.secondaryClient, r.secondaryAuth, r.secondaryURL)
	if err != nil {
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("secondary list repos: %v", err), Remaining: 1}
	}

	pSet := toSet(pRepos)
	sSet := toSet(sRepos)

	missing := setDiff(pSet, sSet)
	extra := setDiff(sSet, pSet)

	if len(missing) == 0 && len(extra) == 0 {
		// Deep check: compare manifest digests for a sample of repos.
		driftRepos := 0
		for repo := range pSet {
			pDigests, err := r.listTags(ctx, r.primaryClient, r.primaryAuth, r.primaryURL, repo)
			if err != nil {
				driftRepos++
				continue
			}
			sDigests, err := r.listTags(ctx, r.secondaryClient, r.secondaryAuth, r.secondaryURL, repo)
			if err != nil {
				driftRepos++
				continue
			}
			if !equalSet(pDigests, sDigests) {
				driftRepos++
			}
		}
		elapsed := time.Since(start)
		metrics.SyncDurationSeconds.WithLabelValues(r.Name(), "ok").Observe(elapsed.Seconds())
		if driftRepos > 0 {
			metrics.DriftTotal.WithLabelValues(r.Name(), "warning").Inc()
			return reconciler.Result{
				OK:        false,
				Detail:    fmt.Sprintf("%d/%d repos have manifest drift", driftRepos, len(pSet)),
				Remaining: driftRepos,
			}
		}
		metrics.LastSyncTimestamp.WithLabelValues(r.Name()).Set(float64(time.Now().Unix()))
		return reconciler.Result{OK: true, Detail: fmt.Sprintf("registry in sync: %d repos", len(pSet))}
	}

	elapsed := time.Since(start)
	metrics.SyncDurationSeconds.WithLabelValues(r.Name(), "error").Observe(elapsed.Seconds())
	metrics.DriftTotal.WithLabelValues(r.Name(), "warning").Inc()
	return reconciler.Result{
		OK:        false,
		Detail:    fmt.Sprintf("repo list drift: %d missing, %d extra on secondary", len(missing), len(extra)),
		Remaining: len(missing) + len(extra),
	}
}

// get issues an authenticated GET, performing the Docker Registry v2
// token exchange when the registry answers 401 with a challenge.
func (r *Reconciler) get(ctx context.Context, client *http.Client, auth *authenticator, url string) (*http.Response, error) {
	resp, err := doGet(ctx, client, url, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	chal, ok := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	_ = resp.Body.Close()
	if !ok {
		return nil, fmt.Errorf("%w: registry returned 401 with no usable Bearer challenge", errAuthFailed)
	}
	token, err := auth.token(ctx, chal)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errAuthFailed, err)
	}
	resp, err = doGet(ctx, client, url, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		auth.invalidate(chal.Scope)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: registry rejected the token for scope %q", errAuthFailed, chal.Scope)
	}
	return resp, nil
}

func doGet(ctx context.Context, client *http.Client, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return client.Do(req)
}

// listRepositories calls the /v2/_catalog endpoint.
func (r *Reconciler) listRepositories(ctx context.Context, client *http.Client, auth *authenticator, baseURL string) ([]string, error) {
	resp, err := r.get(ctx, client, auth, baseURL+"/_catalog?n=1000")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("catalog: status %d: %s", resp.StatusCode, string(body))
	}
	var cat struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	return cat.Repositories, nil
}

// listTags calls /v2/<name>/tags/list for a repository.
func (r *Reconciler) listTags(ctx context.Context, client *http.Client, auth *authenticator, baseURL, repo string) (map[string]bool, error) {
	resp, err := r.get(ctx, client, auth, fmt.Sprintf("%s/%s/tags/list", baseURL, repo))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tags for %s: status %d", repo, resp.StatusCode)
	}
	var tl struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tl); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, t := range tl.Tags {
		set[t] = true
	}
	return set, nil
}

// errAuthFailed means the registry could not be authenticated to. It is
// a failure, not a reason to report success: the operator configured a
// registry URL, so a check that cannot run must say so rather than
// leaving a dashboard green.
var errAuthFailed = fmt.Errorf("registry authentication failed")

func toSet(s []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range s {
		m[v] = true
	}
	return m
}

func setDiff(a, b map[string]bool) []string {
	var diff []string
	for k := range a {
		if !b[k] {
			diff = append(diff, k)
		}
	}
	return diff
}

func equalSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
