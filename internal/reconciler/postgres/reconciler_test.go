package postgresreconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lknappich/syncctl/internal/config"
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
