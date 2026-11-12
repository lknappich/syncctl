// Package postgresreconciler monitors PostgreSQL physical streaming
// replication and reports lag. The actual WAL receiver is a native
// postgres process; this reconciler observes it via pg_stat_replication
// (on the primary) and pg_is_in_recovery() (on the secondary).
package postgresreconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
)

const name = "postgres"

// Querier is the minimal subset of *pgxpool.Pool that this reconciler
// needs. Tests pass mocks; production passes *pgxpool.Pool via poolAdapter.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Close()
}

// Pool is a Querier plus a Close method (already on Querier) — kept as
// an alias for clarity at call sites.
type Pool = Querier

// PoolFactory builds a Pool from a DSN. The default is pgxpool.New
// wrapped in poolAdapter; tests inject a fake.
type PoolFactory func(ctx context.Context, dsn string) (Pool, error)

// poolAdapter wraps *pgxpool.Pool so it satisfies Querier.
type poolAdapter struct {
	*pgxpool.Pool
}

func (a *poolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.Pool.QueryRow(ctx, sql, args...)
}

var defaultPoolFactory PoolFactory = func(ctx context.Context, dsn string) (Pool, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &poolAdapter{Pool: p}, nil
}

// Reconciler implements reconciler.Reconciler for PostgreSQL streaming.
type Reconciler struct {
	primary     Pool
	secondaries []secondaryConn
	lagWarning  time.Duration
	lagCritical time.Duration
}

type secondaryConn struct {
	name string
	pool Pool
}

// New creates a Postgres reconciler from config. Pools are created
// immediately; close them with Close().
func New(ctx context.Context, cfg *config.Config) (*Reconciler, error) {
	return NewWithFactory(ctx, cfg, defaultPoolFactory)
}

// NewWithFactory is like New but accepts an injectable PoolFactory.
func NewWithFactory(ctx context.Context, cfg *config.Config, pf PoolFactory) (*Reconciler, error) {
	pPool, err := pf(ctx, cfg.Primary.Postgres.DSN())
	if err != nil {
		return nil, fmt.Errorf("connect primary pg: %w", err)
	}
	r := &Reconciler{
		primary:     pPool,
		lagWarning:  cfg.Sync.LagWarningThreshold,
		lagCritical: cfg.Sync.LagCriticalThreshold,
	}
	for _, sc := range cfg.Secondaries {
		sPool, err := pf(ctx, sc.Postgres.DSN())
		if err != nil {
			pPool.Close()
			for _, prev := range r.secondaries {
				prev.pool.Close()
			}
			return nil, fmt.Errorf("connect secondary %s pg: %w", sc.Name, err)
		}
		r.secondaries = append(r.secondaries, secondaryConn{name: sc.Name, pool: sPool})
	}
	return r, nil
}

// Close releases all DB pools.
func (r *Reconciler) Close() {
	if r.primary != nil {
		r.primary.Close()
	}
	for _, s := range r.secondaries {
		s.pool.Close()
	}
}

func (r *Reconciler) Name() string { return name }

// Reconcile queries replication status on both sides and reports lag.
// Lag at or above the warning threshold stays OK but is logged; lag at
// or above the critical threshold, or lag that cannot be measured, is
// drift. A zero threshold disables that check.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	var result reconciler.Result
	var notes []string

	for _, sc := range r.secondaries {
		lag, err := r.checkSecondary(ctx, sc)
		if err != nil {
			result.Remaining++
			notes = append(notes, fmt.Sprintf("%s error: %v", sc.name, err))
			metrics.PGReplayLagSeconds.WithLabelValues(sc.name).Set(-1)
			metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
			continue
		}
		metrics.PGReplayLagSeconds.WithLabelValues(sc.name).Set(lag.Seconds())
		result.Lag = max(result.Lag, lag)
		switch {
		case r.lagCritical > 0 && lag >= r.lagCritical:
			result.Remaining++
			notes = append(notes, fmt.Sprintf("%s lag=%s at or above critical threshold %s", sc.name, lag, r.lagCritical))
			metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		case r.lagWarning > 0 && lag >= r.lagWarning:
			notes = append(notes, fmt.Sprintf("%s lag=%s at or above warning threshold %s", sc.name, lag, r.lagWarning))
			log.Warn().Str("reconciler", r.Name()).Str("secondary", sc.name).
				Dur("lag", lag).Dur("threshold", r.lagWarning).
				Msg("postgres replay lag at or above warning threshold")
		case lag > 0:
			notes = append(notes, fmt.Sprintf("%s lag=%s", sc.name, lag))
		}
	}
	result.OK = result.Remaining == 0
	if result.OK {
		notes = append([]string{"ok"}, notes...)
	}
	result.Detail = strings.Join(notes, "; ")
	return result
}

// checkSecondary verifies the secondary is in recovery and measures
// lag from the primary's pg_stat_replication view.
func (r *Reconciler) checkSecondary(ctx context.Context, sc secondaryConn) (time.Duration, error) {
	var inRecovery bool
	err := sc.pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery)
	if err != nil {
		return 0, fmt.Errorf("query pg_is_in_recovery: %w", err)
	}
	if !inRecovery {
		return 0, fmt.Errorf("secondary %s is not in recovery mode (already promoted?)", sc.name)
	}

	row := r.primary.QueryRow(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM replay_lag), 0)
		FROM pg_stat_replication
		WHERE application_name = $1
		LIMIT 1`, sc.name)

	var lagSec float64
	err = row.Scan(&lagSec)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("no pg_stat_replication row on primary for application_name %q (replay lag unknown)", sc.name)
	}
	if err != nil {
		return 0, fmt.Errorf("primary has no replication row for %s: %w", sc.name, err)
	}
	if lagSec < 0 {
		return 0, fmt.Errorf("primary reported negative replay lag %gs (replay lag unknown)", lagSec)
	}
	return time.Duration(lagSec * float64(time.Second)), nil
}

// PrimaryPool returns the primary connection pool (for reuse by other
// reconcilers like the consistency sweep).
func (r *Reconciler) PrimaryPool() *pgxpool.Pool {
	if a, ok := r.primary.(*poolAdapter); ok {
		return a.Pool
	}
	return nil
}

// SecondaryPool returns the pool for the named secondary, or nil.
func (r *Reconciler) SecondaryPool(name string) *pgxpool.Pool {
	for _, s := range r.secondaries {
		if s.name == name {
			if a, ok := s.pool.(*poolAdapter); ok {
				return a.Pool
			}
			return nil
		}
	}
	return nil
}

// PrimaryQuerier returns the primary as a Querier (for test access).
func (r *Reconciler) PrimaryQuerier() Querier { return r.primary }

// SecondaryQuerier returns the named secondary as a Querier, or nil.
func (r *Reconciler) SecondaryQuerier(name string) Querier {
	for _, s := range r.secondaries {
		if s.name == name {
			return s.pool
		}
	}
	return nil
}
