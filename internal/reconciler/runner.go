// Package reconciler defines the common interface and runner for all
// sync reconcilers. Each reconciler is an idempotent, retriable loop
// that observes replication state and performs repairs when safe.
package reconciler

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// Result is the outcome of one Reconcile call.
type Result struct {
	OK        bool          // true if the component is fully in sync
	Lag       time.Duration // observed lag, if measurable
	Detail    string        // human-readable summary for logs
	Repaired  int           // number of items repaired this run
	Remaining int           // items still out of sync (0 when OK)
}

// Reconciler is implemented by every sync component (pg, git, s3, etc.).
type Reconciler interface {
	Name() string
	Reconcile(ctx context.Context) Result
}

// QualifyName appends the site a reconciler is replicating to, so that
// logs and the metric "component" label distinguish the instances when
// more than one secondary is configured. An empty site leaves the base
// name untouched.
func QualifyName(base, site string) string {
	if site == "" {
		return base
	}
	return base + "@" + site
}

// Runner drives a set of reconcilers on a fixed interval until ctx is
// cancelled. It records metrics and structured logs for each run.
type Runner struct {
	interval    time.Duration
	reconcilers []Reconciler
}

// NewRunner creates a Runner that fires every interval.
func NewRunner(interval time.Duration, recs ...Reconciler) *Runner {
	return &Runner{interval: interval, reconcilers: recs}
}

// Run blocks until ctx is cancelled. Each reconciler runs sequentially
// within a sweep; a single sweep failure does not abort the others.
func (r *Runner) Run(ctx context.Context) {
	log.Info().Dur("interval", r.interval).Int("reconcilers", len(r.reconcilers)).
		Msg("reconciler runner started")
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("reconciler runner stopped")
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

func (r *Runner) sweep(ctx context.Context) {
	for _, rec := range r.reconcilers {
		select {
		case <-ctx.Done():
			return
		default:
		}
		start := time.Now()
		result := rec.Reconcile(ctx)
		elapsed := time.Since(start)
		logger := log.With().
			Str("reconciler", rec.Name()).
			Dur("elapsed", elapsed).
			Logger()
		switch {
		case result.OK && result.Remaining == 0:
			logger.Debug().Str("detail", result.Detail).Dur("lag", result.Lag).
				Msg("in sync")
		case result.OK:
			logger.Info().Str("detail", result.Detail).Int("remaining", result.Remaining).
				Msg("partial sync")
		default:
			logger.Warn().Str("detail", result.Detail).Int("repaired", result.Repaired).
				Int("remaining", result.Remaining).Dur("lag", result.Lag).
				Msg("drift detected")
		}
	}
}
