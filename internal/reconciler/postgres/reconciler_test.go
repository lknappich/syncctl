package postgresreconciler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/metrics"
)

// --- Mocks ---

type mockRow struct {
	scanFn func(dest ...any) error
}

func (r *mockRow) Scan(dest ...any) error { return r.scanFn(dest...) }

var _ pgx.Row = (*mockRow)(nil)

type mockPool struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	closed     bool
}

func (p *mockPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.queryRowFn(ctx, sql, args...)
}

func (p *mockPool) Close() { p.closed = true }

var _ Querier = (*mockPool)(nil)

func fakePoolFactory(pool Pool) PoolFactory {
	return func(ctx context.Context, dsn string) (Pool, error) { return pool, nil }
}

func errPoolFactory(err error) PoolFactory {
	return func(ctx context.Context, dsn string) (Pool, error) { return nil, err }
}

// --- Existing tests (updated) ---

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "postgres" {
		t.Errorf("Name() = %q, want postgres", r.Name())
	}
}

func TestSecondaryPoolNotFound(t *testing.T) {
	r := &Reconciler{
		secondaries: []secondaryConn{
			{name: "s1", pool: &mockPool{}},
			{name: "s2", pool: &mockPool{}},
		},
	}
	if got := r.SecondaryPool("nonexistent"); got != nil {
		t.Errorf("SecondaryPool(unknown) = %v, want nil", got)
	}
}

func TestSecondaryPoolFoundWithMock(t *testing.T) {
	mp := &mockPool{}
	r := &Reconciler{secondaries: []secondaryConn{{name: "s1", pool: mp}}}
	// SecondaryPool returns *pgxpool.Pool only for poolAdapter-backed pools;
	// a mock returns nil (no concrete pool to expose).
	if got := r.SecondaryPool("s1"); got != nil {
		t.Errorf("SecondaryPool(s1) = %v, want nil for mock-backed", got)
	}
}

func TestSecondaryQuerierFound(t *testing.T) {
	mp := &mockPool{}
	r := &Reconciler{secondaries: []secondaryConn{{name: "s1", pool: mp}}}
	if got := r.SecondaryQuerier("s1"); got == nil {
		t.Error("SecondaryQuerier(s1) = nil, want mock")
	}
}

func TestSecondaryQuerierNotFound(t *testing.T) {
	r := &Reconciler{}
	if got := r.SecondaryQuerier("nope"); got != nil {
		t.Errorf("SecondaryQuerier(nope) = %v, want nil", got)
	}
}

func TestCloseNilSafe(t *testing.T) {
	r := &Reconciler{}
	r.Close()
}

func TestPrimaryPoolNil(t *testing.T) {
	r := &Reconciler{}
	if r.PrimaryPool() != nil {
		t.Error("PrimaryPool() should be nil on zero-value Reconciler")
	}
}

func TestPrimaryQuerier(t *testing.T) {
	mp := &mockPool{}
	r := &Reconciler{primary: mp}
	if r.PrimaryQuerier() != mp {
		t.Error("PrimaryQuerier() should return the primary pool")
	}
}

// --- NewWithFactory tests ---

func TestNewWithFactoryPrimaryError(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
	}
	_, err := NewWithFactory(context.Background(), cfg, errPoolFactory(errors.New("dial error")))
	if err == nil {
		t.Fatal("expected primary connect error")
	}
}

func TestNewWithFactorySecondaryError(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
		Secondaries: []config.SiteConfig{{Name: "s1", Postgres: config.PostgresConfig{Host: "h", Port: 5432}}},
	}
	// First call (primary) succeeds, second call (secondary) fails.
	calls := 0
	pf := func(ctx context.Context, dsn string) (Pool, error) {
		calls++
		if calls == 1 {
			return &mockPool{}, nil
		}
		return nil, errors.New("secondary dial error")
	}
	_, err := NewWithFactory(context.Background(), cfg, pf)
	if err == nil {
		t.Fatal("expected secondary connect error")
	}
}

func TestNewWithFactorySuccess(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
		Secondaries: []config.SiteConfig{
			{Name: "s1", Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
			{Name: "s2", Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
		},
	}
	pf := fakePoolFactory(&mockPool{})
	r, err := NewWithFactory(context.Background(), cfg, pf)
	if err != nil {
		t.Fatalf("NewWithFactory: %v", err)
	}
	if r.PrimaryQuerier() == nil {
		t.Error("expected primary querier")
	}
	if r.SecondaryQuerier("s2") == nil {
		t.Error("expected secondary s2 querier")
	}
	r.Close()
}

// --- Reconcile tests ---

func TestReconcileNoSecondaries(t *testing.T) {
	r := &Reconciler{primary: &mockPool{}}
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Error("expected OK with no secondaries")
	}
}

func TestReconcileSecondaryNotInRecovery(t *testing.T) {
	secondary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = false
			return nil
		}}
	}}
	r := &Reconciler{
		primary:     &mockPool{},
		secondaries: []secondaryConn{{name: "s1", pool: secondary}},
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK when secondary not in recovery")
	}
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0")
	}
}

func TestReconcileSecondaryQueryError(t *testing.T) {
	secondary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("query failed") }}
	}}
	r := &Reconciler{
		primary:     &mockPool{},
		secondaries: []secondaryConn{{name: "s1", pool: secondary}},
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on query error")
	}
}

func TestReconcilePrimaryReplicationRowError(t *testing.T) {
	secondary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("no replication row") }}
	}}
	r := &Reconciler{
		primary:     primary,
		secondaries: []secondaryConn{{name: "s1", pool: secondary}},
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK when primary replication row missing")
	}
}

func TestReconcileLagZero(t *testing.T) {
	secondary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*float64)) = 0
			return nil
		}}
	}}
	r := &Reconciler{
		primary:     primary,
		secondaries: []secondaryConn{{name: "s1", pool: secondary}},
	}
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK with zero lag, got: %s", res.Detail)
	}
	if res.Lag != 0 {
		t.Errorf("Lag = %v, want 0", res.Lag)
	}
}

func TestReconcileLagPositive(t *testing.T) {
	secondary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*float64)) = 5.0
			return nil
		}}
	}}
	r := &Reconciler{
		primary:     primary,
		secondaries: []secondaryConn{{name: "s1", pool: secondary}},
	}
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK with lag, got: %s", res.Detail)
	}
	if res.Lag.Seconds() != 5 {
		t.Errorf("Lag = %v, want 5s", res.Lag)
	}
}

func TestReconcileMultipleSecondariesMixed(t *testing.T) {
	// s1 healthy, s2 not in recovery.
	s1 := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	s2 := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = false
			return nil
		}}
	}}
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*float64)) = 0
			return nil
		}}
	}}
	r := &Reconciler{
		primary: primary,
		secondaries: []secondaryConn{
			{name: "s1", pool: s1},
			{name: "s2", pool: s2},
		},
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK with one failing secondary")
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", res.Remaining)
	}
}

// --- Lag threshold tests ---

// lagPool answers both the secondary's pg_is_in_recovery() and the
// primary's replay-lag query, so one mock can stand in for either side.
func lagPool(lagSec float64) *mockPool {
	return &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			switch d := dest[0].(type) {
			case *bool:
				*d = true
			case *float64:
				*d = lagSec
			}
			return nil
		}}
	}}
}

func counterValue(t *testing.T, severity string) float64 {
	t.Helper()
	var m dto.Metric
	if err := metrics.DriftTotal.WithLabelValues(name, severity).Write(&m); err != nil {
		t.Fatalf("read drift counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, secondary string) float64 {
	t.Helper()
	var m dto.Metric
	if err := metrics.PGReplayLagSeconds.WithLabelValues(secondary).Write(&m); err != nil {
		t.Fatalf("read lag gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })
	return &buf
}

func TestReconcileLagThresholdBands(t *testing.T) {
	const (
		warning  = 30 * time.Second
		critical = 5 * time.Minute
	)
	tests := []struct {
		name          string
		lagSec        float64
		wantOK        bool
		wantRemaining int
		wantInDetail  string
		wantWarnLog   bool
	}{
		{name: "zero", lagSec: 0, wantOK: true},
		{name: "below warning", lagSec: 29.5, wantOK: true, wantInDetail: "s1 lag=29.5s"},
		{name: "exactly warning", lagSec: 30, wantOK: true, wantInDetail: "warning threshold 30s", wantWarnLog: true},
		{name: "between warning and critical", lagSec: 120, wantOK: true, wantInDetail: "warning threshold 30s", wantWarnLog: true},
		{name: "just below critical", lagSec: 299.5, wantOK: true, wantInDetail: "warning threshold 30s", wantWarnLog: true},
		{name: "exactly critical", lagSec: 300, wantOK: false, wantRemaining: 1, wantInDetail: "critical threshold 5m0s"},
		{name: "above critical", lagSec: 3600, wantOK: false, wantRemaining: 1, wantInDetail: "critical threshold 5m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			criticalBefore := counterValue(t, "critical")
			r := &Reconciler{
				primary:     lagPool(tt.lagSec),
				secondaries: []secondaryConn{{name: "s1", pool: lagPool(0)}},
				lagWarning:  warning,
				lagCritical: critical,
			}
			res := r.Reconcile(context.Background())

			if res.OK != tt.wantOK {
				t.Errorf("OK = %t, want %t (detail: %s)", res.OK, tt.wantOK, res.Detail)
			}
			if res.Remaining != tt.wantRemaining {
				t.Errorf("Remaining = %d, want %d", res.Remaining, tt.wantRemaining)
			}
			wantLag := time.Duration(tt.lagSec * float64(time.Second))
			if res.Lag != wantLag {
				t.Errorf("Lag = %s, want %s", res.Lag, wantLag)
			}
			if tt.wantInDetail != "" && !strings.Contains(res.Detail, tt.wantInDetail) {
				t.Errorf("Detail = %q, want it to contain %q", res.Detail, tt.wantInDetail)
			}
			if !tt.wantOK && strings.HasPrefix(res.Detail, "ok") {
				t.Errorf("Detail = %q reads as healthy for a not-OK result", res.Detail)
			}
			if tt.wantInDetail == "" && strings.Contains(res.Detail, "threshold") {
				t.Errorf("Detail = %q, want no threshold note", res.Detail)
			}

			gotDrift := counterValue(t, "critical") - criticalBefore
			wantDrift := float64(tt.wantRemaining)
			if gotDrift != wantDrift {
				t.Errorf("critical drift_total delta = %v, want %v", gotDrift, wantDrift)
			}

			gotWarnLog := strings.Contains(logs.String(), `"level":"warn"`)
			if gotWarnLog != tt.wantWarnLog {
				t.Errorf("warn log emitted = %t, want %t; logs: %s", gotWarnLog, tt.wantWarnLog, logs.String())
			}
		})
	}
}

func TestReconcileLagWarningLogIsStructured(t *testing.T) {
	logs := captureLogs(t)
	r := &Reconciler{
		primary:     lagPool(45),
		secondaries: []secondaryConn{{name: "s1", pool: lagPool(0)}},
		lagWarning:  30 * time.Second,
		lagCritical: 5 * time.Minute,
	}
	r.Reconcile(context.Background())

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("expected one JSON log line, got %q: %v", logs.String(), err)
	}
	if entry["level"] != "warn" || entry["secondary"] != "s1" {
		t.Errorf("log entry = %v, want level=warn secondary=s1", entry)
	}
	if _, ok := entry["lag"]; !ok {
		t.Errorf("log entry = %v, want a lag field", entry)
	}
	if _, ok := entry["threshold"]; !ok {
		t.Errorf("log entry = %v, want a threshold field", entry)
	}
}

func TestReconcileReplicationRowNotFound(t *testing.T) {
	criticalBefore := counterValue(t, "critical")
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
	}}
	r := &Reconciler{
		primary:     primary,
		secondaries: []secondaryConn{{name: "s1", pool: lagPool(0)}},
		lagWarning:  30 * time.Second,
		lagCritical: 5 * time.Minute,
	}
	res := r.Reconcile(context.Background())

	if res.OK {
		t.Errorf("expected not-OK when the replication row is missing, got detail: %s", res.Detail)
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", res.Remaining)
	}
	if strings.HasPrefix(res.Detail, "ok") {
		t.Errorf("Detail = %q reads as healthy", res.Detail)
	}
	if !strings.Contains(res.Detail, "replay lag unknown") {
		t.Errorf("Detail = %q, want it to say replay lag is unknown", res.Detail)
	}
	if got := gaugeValue(t, "s1"); got != -1 {
		t.Errorf("lag gauge = %v, want -1", got)
	}
	if got := counterValue(t, "critical") - criticalBefore; got != 1 {
		t.Errorf("critical drift_total delta = %v, want 1", got)
	}
}

func TestReconcileNegativeLagIsNotHealthy(t *testing.T) {
	r := &Reconciler{
		primary:     lagPool(-1),
		secondaries: []secondaryConn{{name: "s1", pool: lagPool(0)}},
		lagWarning:  30 * time.Second,
		lagCritical: 5 * time.Minute,
	}
	res := r.Reconcile(context.Background())

	if res.OK {
		t.Errorf("expected not-OK for lag -1, got detail: %s", res.Detail)
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", res.Remaining)
	}
	if !strings.Contains(res.Detail, "replay lag unknown") {
		t.Errorf("Detail = %q, want it to say replay lag is unknown", res.Detail)
	}
	if got := gaugeValue(t, "s1"); got != -1 {
		t.Errorf("lag gauge = %v, want -1", got)
	}
}

func TestReconcileMixedSecondariesWorstWins(t *testing.T) {
	primary := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			if args[0] == "slow" {
				*(dest[0].(*float64)) = 600
			} else {
				*(dest[0].(*float64)) = 1
			}
			return nil
		}}
	}}
	r := &Reconciler{
		primary: primary,
		secondaries: []secondaryConn{
			{name: "fast", pool: lagPool(0)},
			{name: "slow", pool: lagPool(0)},
		},
		lagWarning:  30 * time.Second,
		lagCritical: 5 * time.Minute,
	}
	res := r.Reconcile(context.Background())

	if res.OK {
		t.Errorf("expected not-OK when one secondary is past critical, got detail: %s", res.Detail)
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", res.Remaining)
	}
	if res.Lag != 10*time.Minute {
		t.Errorf("Lag = %s, want 10m0s", res.Lag)
	}
}

func TestNewWithFactoryAppliesLagThresholds(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Postgres: config.PostgresConfig{Host: "h", Port: 5432}},
		Secondaries: []config.SiteConfig{{Name: "s1", Postgres: config.PostgresConfig{Host: "h", Port: 5432}}},
		Sync: config.SyncConfig{
			LagWarningThreshold:  5 * time.Second,
			LagCriticalThreshold: 10 * time.Second,
		},
	}
	r, err := NewWithFactory(context.Background(), cfg, fakePoolFactory(lagPool(10)))
	if err != nil {
		t.Fatalf("NewWithFactory: %v", err)
	}
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Errorf("expected not-OK at the configured critical threshold, got detail: %s", res.Detail)
	}
}
