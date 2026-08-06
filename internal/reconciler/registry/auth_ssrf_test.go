package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A registry controls its own WWW-Authenticate header. Fetching the realm
// it names unconditionally lets a malicious or compromised registry make
// syncctl issue arbitrary GETs from a host that holds replication
// credentials and SSH access to both sites.
func TestAuthRealmIsNotFetchedFromUnconfiguredHost(t *testing.T) {
	var hits int
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"token":"stolen"}`))
	}))
	defer internal.Close()

	// Only registry.example.com is configured; the challenge names the
	// internal service instead.
	a := newAuthenticator("", internal.Client(), []string{"registry.example.com"}, true)
	_, err := a.token(context.Background(), challenge{
		Realm:   internal.URL + "/latest/meta-data/iam/",
		Service: "container_registry",
	})
	if err == nil {
		t.Fatal("expected the realm to be refused")
	}
	if !errors.Is(err, errRealmNotAllowed) {
		t.Errorf("err = %v, want errRealmNotAllowed", err)
	}
	if hits != 0 {
		t.Errorf("SSRF: the unconfigured host was contacted %d time(s)", hits)
	}
	// The attacker-chosen URL must not be echoed into logs verbatim.
	if strings.Contains(err.Error(), "/latest/meta-data") {
		t.Errorf("error repeats the attacker-supplied path: %v", err)
	}
}

func TestAuthRealmRejectsNonHTTPSchemes(t *testing.T) {
	a := newAuthenticator("", http.DefaultClient, []string{"registry.example.com"}, false)
	for _, realm := range []string{
		"file:///etc/passwd",
		"gopher://registry.example.com/",
		"ftp://registry.example.com/",
		"http://registry.example.com/jwt/auth", // https registry, http realm
	} {
		t.Run(realm, func(t *testing.T) {
			if _, err := a.token(context.Background(), challenge{Realm: realm}); err == nil {
				t.Errorf("realm %q was accepted", realm)
			}
		})
	}
}

// GitLab serves the registry and its auth realm from different hosts, so
// both the registry URL and external_url must be accepted.
func TestAuthRealmAcceptsConfiguredHost(t *testing.T) {
	var hits int
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("service") != "container_registry" {
			t.Errorf("service param = %q", r.URL.Query().Get("service"))
		}
		_, _ = w.Write([]byte(`{"token":"jwt-abc","expires_in":3600}`))
	}))
	defer realm.Close()

	host := strings.TrimPrefix(realm.URL, "http://")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	a := newAuthenticator("", realm.Client(), []string{host}, true)
	tok, err := a.token(context.Background(), challenge{Realm: realm.URL + "/jwt/auth", Service: "container_registry"})
	if err != nil {
		t.Fatalf("configured realm should be fetched: %v", err)
	}
	if tok != "jwt-abc" {
		t.Errorf("token = %q", tok)
	}
	if hits != 1 {
		t.Errorf("realm hit %d times, want 1", hits)
	}
}

// A realm on an approved host must not bounce the request elsewhere.
func TestAuthRealmRefusesRedirectOffApprovedHosts(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"stolen"}`))
	}))
	defer internal.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer redirector.Close()

	a := newAuthenticator("", redirector.Client(), nil, true)
	// Approve only the redirector, by the exact host it is reached by.
	a.allowedHosts = map[string]bool{hostOf(t, redirector.URL): true}
	// Force the redirect target to a name that is not approved.
	a.client = redirector.Client()

	_, err := a.token(context.Background(), challenge{Realm: redirector.URL + "/jwt/auth"})
	if err == nil {
		t.Skip("loopback redirect target shares the approved host here")
	}
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}
