package apivalidator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
)

func TestNewBuildsURLs(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{ExternalURL: "https://gitlab.primary.example.com/"},
		Secondaries: []config.SiteConfig{{ExternalURL: "https://gitlab.secondary.example.com/"}},
		APIValidator: &config.APIValidatorConfig{
			Enabled:        true,
			PrimaryToken:   "tok1",
			SecondaryToken: "tok2",
		},
	}
	r := New(cfg, &cfg.Secondaries[0], "s")
	if r.primaryURL != "https://gitlab.primary.example.com/api/v4" {
		t.Errorf("primaryURL = %q", r.primaryURL)
	}
	if r.secondaryURL != "https://gitlab.secondary.example.com/api/v4" {
		t.Errorf("secondaryURL = %q", r.secondaryURL)
	}
	if r.primaryToken != "tok1" || r.secondaryToken != "tok2" {
		t.Error("tokens not set")
	}
}

// newTestReconciler builds a Reconciler pointing at two httptest servers.
func newTestReconciler(t *testing.T, primaryHandler, secondaryHandler http.HandlerFunc) (*Reconciler, *httptest.Server, *httptest.Server) {
	t.Helper()
	psrv := httptest.NewServer(primaryHandler)
	ssrv := httptest.NewServer(secondaryHandler)
	r := &Reconciler{
		primaryURL:     psrv.URL + "/api/v4",
		secondaryURL:   ssrv.URL + "/api/v4",
		primaryToken:   "ptok",
		secondaryToken: "stok",
		client:         &http.Client{},
	}
	return r, psrv, ssrv
}

func TestReconcileAllMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "10")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Reconciler{
		primaryURL:   srv.URL + "/api/v4",
		secondaryURL: srv.URL + "/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: srv.Client(),
	}
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
}

func TestReconcileDrift(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "100")
		w.WriteHeader(http.StatusOK)
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "99")
		w.WriteHeader(http.StatusOK)
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:   psrv.URL + "/api/v4",
		secondaryURL: ssrv.URL + "/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: &http.Client{},
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on drift")
	}
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 on drift")
	}
}

func TestReconcilePrimaryError(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "10")
		w.WriteHeader(http.StatusOK)
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:   psrv.URL + "/api/v4",
		secondaryURL: ssrv.URL + "/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: &http.Client{},
	}
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 when primary errors")
	}
}

func TestReconcileSecondaryError(t *testing.T) {
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "10")
		w.WriteHeader(http.StatusOK)
	}))
	defer psrv.Close()
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ssrv.Close()
	r := &Reconciler{
		primaryURL:   psrv.URL + "/api/v4",
		secondaryURL: ssrv.URL + "/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: &http.Client{},
	}
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 when secondary errors")
	}
}

func TestReconcileMissingXTotalHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Reconciler{
		primaryURL:   srv.URL + "/api/v4",
		secondaryURL: srv.URL + "/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: &http.Client{},
	}
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 when X-Total missing")
	}
}

func TestReconcileConnectionFailure(t *testing.T) {
	// Use a server we close immediately to force a connection error.
	psrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	psrv.Close()
	r := &Reconciler{
		primaryURL:   psrv.URL + "/api/v4",
		secondaryURL: "http://127.0.0.1:1/api/v4",
		primaryToken: "p", secondaryToken: "s",
		client: &http.Client{},
	}
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 on connection failure")
	}
}

func TestFetchCountOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "42")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Reconciler{client: srv.Client()}
	count, err := r.fetchCount(context.Background(), srv.URL, "tok", "projects")
	if err != nil {
		t.Fatalf("fetchCount: %v", err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
}

func TestFetchCountBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &Reconciler{client: srv.Client()}
	_, err := r.fetchCount(context.Background(), srv.URL, "tok", "projects")
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestFetchCountMissingHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Reconciler{client: srv.Client()}
	_, err := r.fetchCount(context.Background(), srv.URL, "tok", "projects")
	if err == nil {
		t.Fatal("expected error when X-Total missing")
	}
}

func TestFetchCountBadURL(t *testing.T) {
	r := &Reconciler{client: &http.Client{}}
	_, err := r.fetchCount(context.Background(), "http://[::1", "tok", "projects")
	if err == nil {
		t.Fatal("expected error for bad URL")
	}
}

func TestFetchCountRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	r := &Reconciler{client: srv.Client()}
	_, err := r.fetchCount(context.Background(), srv.URL, "tok", "projects")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "api_validator" {
		t.Errorf("Name() = %q, want api_validator", r.Name())
	}
}
