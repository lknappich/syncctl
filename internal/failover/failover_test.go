package failover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lknappich/syncctl/internal/config"
)

func TestNewControllerDefaults(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
	}
	fc := New(cfg, true)
	if fc.quorum != 1 {
		t.Errorf("quorum = %d, want 1", fc.quorum)
	}
	if fc.autoFailover {
		t.Error("autoFailover should be false by default")
	}
}

func TestNewControllerWithFailoverConfig(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
		Failover: &config.FailoverConfig{
			AutoFailover:   true,
			QuorumRequired: 3,
			DNSPlugin:      "route53",
		},
	}
	fc := New(cfg, false)
	if fc.quorum != 3 {
		t.Errorf("quorum = %d, want 3", fc.quorum)
	}
	if !fc.autoFailover {
		t.Error("autoFailover should be true")
	}
}

func TestPromoteRejectsWhenFailoverDisabled(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
		Sync: config.SyncConfig{FailoverEnabled: false},
	}
	fc := New(cfg, false)
	err := fc.Promote(context.Background(), "s")
	if err == nil {
		t.Fatal("expected error when failover disabled")
	}
}

func TestPromoteDryRunSkipsChecks(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
		Sync: config.SyncConfig{FailoverEnabled: false},
	}
	fc := New(cfg, true) // dryRun=true
	err := fc.Promote(context.Background(), "s")
	if err != nil {
		t.Fatalf("dry-run promote should succeed, got: %v", err)
	}
}

func TestPromoteRejectsUnknownSecondary(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
	}
	fc := New(cfg, true)
	err := fc.Promote(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown secondary")
	}
}

func TestIsPrimaryDownInitiallyFalse(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
	}
	fc := New(cfg, false)
	if fc.IsPrimaryDown() {
		t.Error("primary should be up initially")
	}
}

func TestFindSecondaryFound(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{
			{Name: "s1"},
			{Name: "s2"},
		},
	}
	fc := New(cfg, true)
	got, err := fc.findSecondary("s2")
	if err != nil {
		t.Fatalf("findSecondary: %v", err)
	}
	if got.Name != "s2" {
		t.Errorf("Name = %q, want s2", got.Name)
	}
}

func TestFindSecondaryNotFound(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{
			{Name: "s1"},
		},
	}
	fc := New(cfg, true)
	_, err := fc.findSecondary("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown secondary")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestVerifyPrimaryDownRefusesWhenUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{{Name: "s"}},
	}
	fc := New(cfg, false)
	err := fc.verifyPrimaryDown(context.Background())
	if err == nil {
		t.Fatal("verifyPrimaryDown should error while the primary answers health checks")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should point at the override flag, got: %v", err)
	}
}

// A one-shot `syncctl failover` never runs the health-check loop, so
// promotion has to probe rather than read state only the daemon sets.
func TestVerifyPrimaryDownProbesWhenLoopNeverRan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{{Name: "s"}},
	}
	fc := New(cfg, false)
	if fc.IsPrimaryDown() {
		t.Fatal("primaryDown should start false")
	}
	if err := fc.verifyPrimaryDown(context.Background()); err != nil {
		t.Fatalf("verifyPrimaryDown should pass for an unhealthy primary: %v", err)
	}
	if !fc.IsPrimaryDown() {
		t.Error("a successful probe should record the primary as down")
	}
}

func TestVerifyPrimaryDownForceOverridesHealthyPrimary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{{Name: "s"}},
	}
	fc := New(cfg, false)
	fc.SetForce(true)
	if err := fc.verifyPrimaryDown(context.Background()); err != nil {
		t.Fatalf("--force should bypass the liveness gate: %v", err)
	}
}

func TestNewClampsQuorumToAtLeastOne(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{{Name: "s"}},
		Failover:    &config.FailoverConfig{QuorumRequired: 0},
	}
	if fc := New(cfg, false); fc.quorum != 1 {
		t.Errorf("quorum = %d, want 1 — a zero quorum declares every primary down", fc.quorum)
	}
}

func TestVerifyPrimaryDownOKWhenDown(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
	}
	fc := New(cfg, false)
	fc.primaryDown.Store(true)
	err := fc.verifyPrimaryDown(context.Background())
	if err != nil {
		t.Fatalf("verifyPrimaryDown should succeed when down: %v", err)
	}
}

func TestPollURLSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fc := New(&config.Config{Primary: config.SiteConfig{ExternalURL: srv.URL}}, false)
	if !fc.pollURL(context.Background(), srv.URL) {
		t.Error("pollURL should return true for HTTP 200")
	}
}

func TestPollURLFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	fc := New(&config.Config{Primary: config.SiteConfig{ExternalURL: srv.URL}}, false)
	if fc.pollURL(context.Background(), srv.URL) {
		t.Error("pollURL should return false for HTTP 500")
	}
}

func TestPollURLConnectionError(t *testing.T) {
	fc := New(&config.Config{Primary: config.SiteConfig{ExternalURL: "http://localhost:1"}}, false)
	if fc.pollURL(context.Background(), "http://127.0.0.1:1/health") {
		t.Error("pollURL should return false for connection error")
	}
}

func TestCheckRecoveryAfterFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary: config.SiteConfig{ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
		Failover: &config.FailoverConfig{QuorumRequired: 1, AutoFailover: false},
	}
	fc := New(cfg, true)
	fc.consecutiveFails.Store(5)
	fc.check(context.Background())
	if fc.consecutiveFails.Load() != 0 {
		t.Error("consecutiveFails should be reset on recovery")
	}
	if fc.IsPrimaryDown() {
		t.Error("primaryDown should be cleared on recovery")
	}
}

func TestCheckIncrementingFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary: config.SiteConfig{ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
		Failover: &config.FailoverConfig{QuorumRequired: 1, AutoFailover: false},
	}
	fc := New(cfg, true)
	fc.check(context.Background())
	if fc.consecutiveFails.Load() != 1 {
		t.Errorf("consecutiveFails = %d, want 1", fc.consecutiveFails.Load())
	}
}

func TestAdoptAsSecondaryRejectsWhenDisabled(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
		Sync: config.SyncConfig{FailoverEnabled: false},
	}
	fc := New(cfg, false)
	err := fc.AdoptAsSecondary(context.Background(), "old-primary:22")
	if err == nil {
		t.Fatal("expected error when failover disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("err = %v", err)
	}
}

func TestAdoptAsSecondaryDryRun(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
		Sync: config.SyncConfig{FailoverEnabled: false},
	}
	fc := New(cfg, true)
	err := fc.AdoptAsSecondary(context.Background(), "old-primary:22")
	if err != nil {
		t.Fatalf("dry-run AdoptAsSecondary should succeed: %v", err)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{
			{Name: "s"},
		},
		Failover: &config.FailoverConfig{QuorumRequired: 1, HealthCheckInterval: 100 * time.Millisecond},
	}
	fc := New(cfg, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fc.Run(ctx)
}

func TestSSHSecondaryCheckHostError(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{{Name: "s"}},
	}
	fc := New(cfg, true)
	err := fc.sshSecondary(context.Background(), "", "echo ok")
	if err == nil {
		t.Fatal("expected error for empty ssh host")
	}
	if !strings.Contains(err.Error(), "ssh_host not configured") {
		t.Errorf("err = %v", err)
	}
}

func TestAdoptAsSecondaryRejectsUnknownSecondary(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{{Name: "s"}},
		Sync:        config.SyncConfig{FailoverEnabled: true},
	}
	fc := New(cfg, true)
	// With failover enabled and dry-run, should succeed.
	err := fc.AdoptAsSecondary(context.Background(), "old:22")
	if err != nil {
		t.Fatalf("dry-run with failover enabled should succeed: %v", err)
	}
}

func TestPromoteRejectsNonExistentSecondary(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{{Name: "s"}},
		Sync:        config.SyncConfig{FailoverEnabled: true},
	}
	fc := New(cfg, true)
	err := fc.Promote(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown secondary")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckSetsPrimaryDownAfter3Fails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cfg := &config.Config{
		Primary:     config.SiteConfig{ExternalURL: srv.URL},
		Secondaries: []config.SiteConfig{{Name: "s"}},
		Failover:    &config.FailoverConfig{QuorumRequired: 1, AutoFailover: false},
	}
	fc := New(cfg, true)
	fc.check(context.Background())
	fc.check(context.Background())
	fc.check(context.Background())
	if !fc.IsPrimaryDown() {
		t.Error("primary should be down after 3 consecutive fails")
	}
}
