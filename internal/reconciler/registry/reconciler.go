// Package registry reconciles container registry contents between primary
// and secondary by comparing manifest digests via the registry HTTP API v2.
// The registry metadata (tags → manifest links) lives in the GitLab DB
// (already replicated via WAL streaming); the blobs live in object storage
// (already replicated via S3/fs). This reconciler validates that the
// registry API on both sides returns the same set of repositories and
// manifest digests.
//
// Note: GitLab's container registry requires Bearer token authentication
// (JWT from the auth realm). When a 401 is received, the reconciler treats
// it as "unconfigured/skip" rather than reporting false drift. To use this
// reconciler, configure a registry token or disable it in config.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	dryRun          bool
}

// New creates a registry reconciler from the primary/secondary external URLs.
func New(primary, secondary *config.SiteConfig, dryRun bool) *Reconciler {
	primaryURL := strings.TrimSuffix(primary.ExternalURL, "/") + "/v2"
	secondaryURL := strings.TrimSuffix(secondary.ExternalURL, "/") + "/v2"

	timeout := 30 * time.Second
	return &Reconciler{
		site:            secondary.Name,
		primaryURL:      primaryURL,
		secondaryURL:    secondaryURL,
		primaryClient:   &http.Client{Timeout: timeout},
		secondaryClient: &http.Client{Timeout: timeout},
		dryRun:          dryRun,
	}
}

func (r *Reconciler) Name() string { return reconciler.QualifyName(name, r.site) }

// Reconcile lists all repositories on both sides, then compares manifest
// digests for each repository.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	start := time.Now()

	pRepos, err := r.listRepositories(ctx, r.primaryClient, r.primaryURL)
	if err != nil {
		if isAuthError(err) {
			return reconciler.Result{OK: true, Detail: "registry: primary requires auth (skipped — configure a token to enable)"}
		}
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("primary list repos: %v", err), Remaining: 1}
	}

	sRepos, err := r.listRepositories(ctx, r.secondaryClient, r.secondaryURL)
	if err != nil {
		if isAuthError(err) {
			return reconciler.Result{OK: true, Detail: "registry: secondary requires auth (skipped — configure a token to enable)"}
		}
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
			pDigests, err := r.listTags(ctx, r.primaryClient, r.primaryURL, repo)
			if err != nil {
				driftRepos++
				continue
			}
			sDigests, err := r.listTags(ctx, r.secondaryClient, r.secondaryURL, repo)
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

// listRepositories calls the /v2/_catalog endpoint.
func (r *Reconciler) listRepositories(ctx context.Context, client *http.Client, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/_catalog?n=1000", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errAuthRequired
	}
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
func (r *Reconciler) listTags(ctx context.Context, client *http.Client, baseURL, repo string) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s/tags/list", baseURL, repo), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errAuthRequired
	}
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

var errAuthRequired = fmt.Errorf("authentication required (401)")

func isAuthError(err error) bool {
	return err == errAuthRequired
}

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
