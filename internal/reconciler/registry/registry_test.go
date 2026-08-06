package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
)

func TestToSet(t *testing.T) {
	s := toSet([]string{"a", "b", "c"})
	if len(s) != 3 || !s["a"] || !s["b"] || !s["c"] {
		t.Errorf("toSet got %v", s)
	}
}

func TestSetDiff(t *testing.T) {
	a := toSet([]string{"a", "b", "c"})
	b := toSet([]string{"b", "c"})
	diff := setDiff(a, b)
	if len(diff) != 1 || diff[0] != "a" {
		t.Errorf("setDiff got %v", diff)
	}
}

func TestSetDiffEmpty(t *testing.T) {
	a := toSet([]string{"a", "b"})
	b := toSet([]string{"a", "b"})
	diff := setDiff(a, b)
	if len(diff) != 0 {
		t.Errorf("setDiff got %v, want empty", diff)
	}
}

func TestEqualSet(t *testing.T) {
	a := toSet([]string{"a", "b"})
	b := toSet([]string{"a", "b"})
	if !equalSet(a, b) {
		t.Error("equalSet should be true")
	}
	c := toSet([]string{"a"})
	if equalSet(a, c) {
		t.Error("equalSet should be false for different sizes")
	}
	d := toSet([]string{"a", "c"})
	if equalSet(a, d) {
		t.Error("equalSet should be false for different keys")
	}
}

func TestNewURLConstruction(t *testing.T) {
	primary := &config.SiteConfig{
		ExternalURL: "https://gitlab.primary.example.com/",
		Registry:    &config.RegistryConfig{URL: "https://registry.primary.example.com/"},
	}
	secondary := &config.SiteConfig{
		ExternalURL: "https://gitlab.secondary.example.com",
		Registry:    &config.RegistryConfig{URL: "https://registry.secondary.example.com"},
	}
	r := New(primary, secondary, "", true)
	if r == nil {
		t.Fatal("New returned nil for two configured registry URLs")
	}
	if r.primaryURL != "https://registry.primary.example.com/v2" {
		t.Errorf("primaryURL = %q", r.primaryURL)
	}
	if r.secondaryURL != "https://registry.secondary.example.com/v2" {
		t.Errorf("secondaryURL = %q", r.secondaryURL)
	}
	if !r.dryRun {
		t.Error("dryRun should be true")
	}
}

// The registry lives at its own host and port. Deriving its URL from
// external_url probed GitLab, which answers 404 or a sign-in redirect for
// /v2/_catalog — reported either as drift that does not exist or, on 401,
// as a pass. With no registry.url there is nothing to check, and New says
// so by returning nil.
func TestNewReturnsNilWithoutRegistryURL(t *testing.T) {
	withURL := &config.SiteConfig{Registry: &config.RegistryConfig{URL: "https://registry.example.com"}}
	cases := map[string][2]*config.SiteConfig{
		"neither configured": {
			{ExternalURL: "https://p.example.com"},
			{ExternalURL: "https://s.example.com"},
		},
		"primary only":   {withURL, {ExternalURL: "https://s.example.com"}},
		"secondary only": {{ExternalURL: "https://p.example.com"}, withURL},
	}
	for label, pair := range cases {
		t.Run(label, func(t *testing.T) {
			if got := New(pair[0], pair[1], "", true); got != nil {
				t.Errorf("New should return nil; got a reconciler pointed at %q", got.primaryURL)
			}
		})
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "registry" {
		t.Errorf("Name() = %q, want registry", r.Name())
	}
}

func TestListRepositoriesOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"repositories": []string{"foo", "bar"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	repos, err := r.listRepositories(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2")
	if err != nil {
		t.Fatalf("listRepositories: %v", err)
	}
	if len(repos) != 2 || repos[0] != "foo" || repos[1] != "bar" {
		t.Errorf("repos = %v", repos)
	}
}

func TestListRepositoriesAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	_, err := r.listRepositories(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2")
	if !errors.Is(err, errAuthFailed) {
		t.Errorf("a 401 with no usable challenge must be a failure, got %v", err)
	}
}

func TestListRepositoriesBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	_, err := r.listRepositories(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestListRepositoriesBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	_, err := r.listRepositories(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2")
	if err == nil {
		t.Fatal("expected error on bad JSON")
	}
}

func TestListRepositoriesBadURL(t *testing.T) {
	r := &Reconciler{primaryClient: &http.Client{}}
	_, err := r.listRepositories(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), "http://[::1")
	if err == nil {
		t.Fatal("expected error on bad URL")
	}
}

func TestListTagsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "foo",
			"tags": []string{"v1", "v2"},
		})
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	set, err := r.listTags(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2", "foo")
	if err != nil {
		t.Fatalf("listTags: %v", err)
	}
	if len(set) != 2 || !set["v1"] || !set["v2"] {
		t.Errorf("set = %v", set)
	}
}

func TestListTagsAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	_, err := r.listTags(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2", "foo")
	if !errors.Is(err, errAuthFailed) {
		t.Errorf("a 401 with no usable challenge must be a failure, got %v", err)
	}
}

func TestListTagsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	_, err := r.listTags(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2", "foo")
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestListTagsBadURL(t *testing.T) {
	r := &Reconciler{primaryClient: &http.Client{}}
	_, err := r.listTags(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), "http://[::1", "foo")
	if err == nil {
		t.Fatal("expected error on bad URL")
	}
}

func TestListTagsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "foo"})
	}))
	defer srv.Close()
	r := &Reconciler{primaryClient: srv.Client()}
	set, err := r.listTags(context.Background(), r.primaryClient, newAuthenticator("", r.primaryClient), srv.URL+"/v2", "foo")
	if err != nil {
		t.Fatalf("listTags: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

// A registry we cannot authenticate to is a check that did not run.
// Reporting OK for it — as the previous "skip on 401" branch did — left
// the registry permanently unverified behind a green dashboard.
func TestReconcileReportsAuthFailureRatherThanSkipping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	r := newTestReconciler(srv.URL, srv.URL, srv.Client(), srv.Client())
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("a registry that cannot be authenticated to must not report OK")
	}
	if !strings.Contains(res.Detail, "authentication failed") {
		t.Errorf("Detail should name the cause, got: %s", res.Detail)
	}
}

// The full Docker Registry v2 flow: 401 with a Bearer challenge, token
// fetched from the auth realm, request retried with it.
func TestReconcileCompletesTokenExchange(t *testing.T) {
	var authRealmHits, catalogHits int
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		authRealmHits++
		if r.URL.Query().Get("service") != "container_registry" {
			t.Errorf("service param = %q", r.URL.Query().Get("service"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "jwt-abc", "expires_in": 3600})
	})
	mux.HandleFunc("/v2/_catalog", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-abc" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+srv.URL+`/auth",service="container_registry",scope="registry:catalog:*"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		catalogHits++
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{}})
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	r := newTestReconciler(srv.URL, srv.URL, srv.Client(), srv.Client())
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Fatalf("expected OK after a successful token exchange, got: %s", res.Detail)
	}
	if authRealmHits == 0 {
		t.Error("the auth realm was never contacted")
	}
	if catalogHits == 0 {
		t.Error("the catalog was never fetched with a token")
	}
}

func newTestReconciler(primaryURL, secondaryURL string, pc, sc *http.Client) *Reconciler {
	return &Reconciler{
		primaryURL:      primaryURL + "/v2",
		secondaryURL:    secondaryURL + "/v2",
		primaryClient:   pc,
		secondaryClient: sc,
		primaryAuth:     newAuthenticator("", pc),
		secondaryAuth:   newAuthenticator("", sc),
	}
}

func TestReconcileRepoListDrift(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"repositories": []string{"a", "b"},
		})
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"repositories": []string{"a"},
		})
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:      psrv.URL + "/v2",
		secondaryURL:    ssrv.URL + "/v2",
		primaryClient:   psrv.Client(),
		secondaryClient: ssrv.Client(),
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on drift")
	}
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0")
	}
}

func TestReconcilePrimaryCatalogError(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{}})
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:      psrv.URL + "/v2",
		secondaryURL:    ssrv.URL + "/v2",
		primaryClient:   psrv.Client(),
		secondaryClient: ssrv.Client(),
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on primary catalog error")
	}
}

func TestReconcileSecondaryCatalogError(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"a"}})
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:      psrv.URL + "/v2",
		secondaryURL:    ssrv.URL + "/v2",
		primaryClient:   psrv.Client(),
		secondaryClient: ssrv.Client(),
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on secondary catalog error")
	}
}

func TestReconcileInSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"a"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "a", "tags": []string{"v1"}})
	}))
	defer srv.Close()
	r := &Reconciler{
		primaryURL:      srv.URL + "/v2",
		secondaryURL:    srv.URL + "/v2",
		primaryClient:   srv.Client(),
		secondaryClient: srv.Client(),
	}
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
}

func TestReconcileTagDrift(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"a"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "a", "tags": []string{"v1", "v2"}})
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"a"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "a", "tags": []string{"v1"}})
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:      psrv.URL + "/v2",
		secondaryURL:    ssrv.URL + "/v2",
		primaryClient:   psrv.Client(),
		secondaryClient: ssrv.Client(),
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on tag drift")
	}
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0")
	}
}
