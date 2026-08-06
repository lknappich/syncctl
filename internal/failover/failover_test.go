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
	err := fc.AdoptAsSecondary(context.Background(), "old-primary:22", "")
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
	err := fc.AdoptAsSecondary(context.Background(), "old-primary:22", "")
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
	err := fc.AdoptAsSecondary(context.Background(), "old:22", "")
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

// Parity must be checked before anything destructive runs. Checked after
// pg_ctl promote it necessarily SSHes to the primary we just declared
// dead, so a successful promotion reported as a failure.
func TestPromoteChecksParityBeforePromotingPostgres(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{{Name: "s"}},
	}
	fc := New(cfg, true)
	secondary, err := fc.findSecondary("s")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, step := range fc.promotionSteps(secondary) {
		order = append(order, step.name)
	}
	parity, promote := indexOf(order, "verify db_key_base parity"), indexOf(order, "promote postgres")
	if parity < 0 || promote < 0 {
		t.Fatalf("missing steps in %v", order)
	}
	if parity > promote {
		t.Errorf("parity check runs after promotion (%v); it must be a precondition", order)
	}
}

func TestAdoptAsSecondaryRequiresASecondary(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p"},
		Sync:    config.SyncConfig{FailoverEnabled: true},
	}
	fc := New(cfg, false)
	err := fc.AdoptAsSecondary(context.Background(), "old:22", "")
	if err == nil {
		t.Fatal("expected an error rather than an index-out-of-range panic")
	}
	if !strings.Contains(err.Error(), "no secondaries configured") {
		t.Errorf("err = %v", err)
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
func TestAutoFailoverTargetRequiresAnExplicitChoice(t *testing.T) {
	two := []config.SiteConfig{{Name: "secondary-us"}, {Name: "secondary-eu"}}

	t.Run("single secondary defaults", func(t *testing.T) {
		fc := New(&config.Config{Secondaries: []config.SiteConfig{{Name: "only"}}}, true)
		got, err := fc.autoFailoverTarget()
		if err != nil || got != "only" {
			t.Errorf("got (%q, %v), want (only, nil)", got, err)
		}
	})

	t.Run("multiple secondaries refuse", func(t *testing.T) {
		fc := New(&config.Config{Secondaries: two}, true)
		if got, err := fc.autoFailoverTarget(); err == nil {
			t.Errorf("expected a refusal, got %q — config order must not decide this", got)
		}
	})

	t.Run("multiple secondaries with explicit choice", func(t *testing.T) {
		fc := New(&config.Config{
			Secondaries: two,
			Failover:    &config.FailoverConfig{PromoteSecondary: "secondary-eu"},
		}, true)
		got, err := fc.autoFailoverTarget()
		if err != nil || got != "secondary-eu" {
			t.Errorf("got (%q, %v), want (secondary-eu, nil)", got, err)
		}
	})

	t.Run("unknown name is an error", func(t *testing.T) {
		fc := New(&config.Config{
			Secondaries: two,
			Failover:    &config.FailoverConfig{PromoteSecondary: "typo"},
		}, true)
		if _, err := fc.autoFailoverTarget(); err == nil {
			t.Error("an unknown secondary name must not silently fall through")
		}
	})
}

// Re-basing the old primary from the wrong site replicates a stale
// replica over it — and with --wipe-pgdata, after deleting its cluster.
func TestResolveNewPrimaryRequiresAnExplicitChoice(t *testing.T) {
	two := []config.SiteConfig{{Name: "secondary-us"}, {Name: "secondary-eu"}}

	fc := New(&config.Config{Secondaries: []config.SiteConfig{{Name: "only"}}}, true)
	if got, err := fc.ResolveNewPrimary(""); err != nil || got.Name != "only" {
		t.Errorf("single secondary should default, got (%v, %v)", got, err)
	}

	fc = New(&config.Config{Secondaries: two}, true)
	if _, err := fc.ResolveNewPrimary(""); err == nil {
		t.Error("expected a refusal with two secondaries and no --new-primary")
	}
	got, err := fc.ResolveNewPrimary("secondary-eu")
	if err != nil || got.Name != "secondary-eu" {
		t.Errorf("explicit name should resolve, got (%v, %v)", got, err)
	}
	if _, err := fc.ResolveNewPrimary("typo"); err == nil {
		t.Error("an unknown secondary name must be an error")
	}
}

func TestAdoptAsSecondaryRefusesAmbiguousNewPrimary(t *testing.T) {
	fc := New(&config.Config{
		Primary:     config.SiteConfig{Name: "p"},
		Secondaries: []config.SiteConfig{{Name: "a"}, {Name: "b"}},
		Sync:        config.SyncConfig{FailoverEnabled: true},
	}, false)
	err := fc.AdoptAsSecondary(context.Background(), "old:22", "")
	if err == nil {
		t.Fatal("expected a refusal rather than silently re-basing from Secondaries[0]")
	}
	if !strings.Contains(err.Error(), "--new-primary") {
		t.Errorf("error should name the flag, got: %v", err)
	}
}
