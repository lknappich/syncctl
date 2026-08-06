package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// authenticator obtains bearer tokens for a Docker Registry v2 endpoint.
//
// The registry answers an unauthenticated request with 401 and a
// WWW-Authenticate header naming an auth realm, service, and scope. The
// client fetches a token from that realm and retries. This is the public
// Docker Registry v2 authentication flow; GitLab's registry implements
// it, which is why the reconciler previously received 401 on every call
// and silently reported "skipped".
//
// A statically configured token bypasses the exchange entirely.
type authenticator struct {
	static string
	client *http.Client

	// allowedHosts are the hosts a challenge may name as its auth realm.
	// The realm arrives in a header the responding server controls, so
	// without this it is an arbitrary outbound GET from a host holding
	// replication credentials and SSH access to both sites. Every entry
	// comes from the operator's own config.
	allowedHosts map[string]bool

	// allowInsecure permits an http realm, for a registry that is itself
	// plain http (local MinIO, dev stacks).
	allowInsecure bool

	mu     sync.Mutex
	cached map[string]string // scope -> token
}

func newAuthenticator(staticToken string, client *http.Client, allowedHosts []string, allowInsecure bool) *authenticator {
	allowed := make(map[string]bool, len(allowedHosts))
	for _, h := range allowedHosts {
		if h != "" {
			allowed[strings.ToLower(h)] = true
		}
	}
	a := &authenticator{
		static:        staticToken,
		client:        client,
		allowedHosts:  allowed,
		allowInsecure: allowInsecure,
		cached:        map[string]string{},
	}
	return a
}

// errRealmNotAllowed reports a challenge naming a host the operator
// never configured.
var errRealmNotAllowed = errors.New("auth realm host is not one of the configured registry or site hosts")

// checkRealm decides whether a challenge's realm may be fetched.
func (a *authenticator) checkRealm(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse auth realm: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !a.allowInsecure {
			return nil, fmt.Errorf("auth realm uses http but the registry is https")
		}
	default:
		return nil, fmt.Errorf("auth realm scheme %q is not http or https", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("auth realm has no host")
	}
	if !a.allowedHosts[strings.ToLower(u.Hostname())] {
		// Deliberately does not echo the realm: the error reaches logs,
		// and repeating an attacker-chosen URL there helps nobody.
		return nil, fmt.Errorf("%w (host %q)", errRealmNotAllowed, u.Hostname())
	}
	return u, nil
}

// challenge is a parsed Bearer WWW-Authenticate header.
type challenge struct {
	Realm   string
	Service string
	Scope   string
}

// parseChallenge reads a `Bearer realm="...",service="...",scope="..."`
// header. It returns ok=false for any other scheme.
func parseChallenge(header string) (challenge, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return challenge{}, false
	}
	var c challenge
	for _, part := range splitParams(header[len(prefix):]) {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "realm":
			c.Realm = value
		case "service":
			c.Service = value
		case "scope":
			c.Scope = value
		}
	}
	if c.Realm == "" {
		return challenge{}, false
	}
	return c, true
}

// splitParams splits on commas that are not inside a quoted value.
func splitParams(s string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			current.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// token returns a bearer token satisfying the challenge, fetching from
// the auth realm when no static token is configured.
func (a *authenticator) token(ctx context.Context, c challenge) (string, error) {
	if a.static != "" {
		return a.static, nil
	}

	a.mu.Lock()
	if tok, ok := a.cached[c.Scope]; ok {
		a.mu.Unlock()
		return tok, nil
	}
	a.mu.Unlock()

	u, err := a.checkRealm(c.Realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if c.Service != "" {
		q.Set("service", c.Service)
	}
	if c.Scope != "" {
		q.Set("scope", c.Scope)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	// A redirect can leave the approved host set, so each hop is checked
	// too rather than trusting the first URL alone.
	client := *a.client
	client.CheckRedirect = func(r *http.Request, _ []*http.Request) error {
		if !a.allowedHosts[strings.ToLower(r.URL.Hostname())] {
			return fmt.Errorf("%w (redirect to %q)", errRealmNotAllowed, r.URL.Hostname())
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch registry token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth realm at %s: status %d", u.Hostname(), resp.StatusCode)
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("auth realm %s returned no token", c.Realm)
	}

	// Only cache tokens that will outlive the sweep they were fetched
	// for; a short-lived one is cheaper to re-fetch than to expire.
	if body.ExpiresIn > int((2 * time.Minute).Seconds()) {
		a.mu.Lock()
		a.cached[c.Scope] = tok
		a.mu.Unlock()
	}
	return tok, nil
}

// invalidate drops a cached token that the registry has rejected.
func (a *authenticator) invalidate(scope string) {
	a.mu.Lock()
	delete(a.cached, scope)
	a.mu.Unlock()
}
