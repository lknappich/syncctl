package registry

import (
	"context"
	"encoding/json"
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

	mu     sync.Mutex
	cached map[string]string // scope -> token
}

func newAuthenticator(staticToken string, client *http.Client) *authenticator {
	return &authenticator{
		static: staticToken,
		client: client,
		cached: map[string]string{},
	}
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

	u, err := url.Parse(c.Realm)
	if err != nil {
		return "", fmt.Errorf("parse auth realm %q: %w", c.Realm, err)
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
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch registry token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth realm %s: status %d", c.Realm, resp.StatusCode)
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
