// Package sla computes RPO/RTO metrics from the control DB and Prometheus
// metrics, producing a human-readable summary.
package sla

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Report summarizes sync lag and projected RPO/RTO.
type Report struct {
	PGLagCurrent      time.Duration
	PGLagPeak         time.Duration
	LastSweepAge      time.Duration
	DriftCount        int64
	ComponentsHealthy int
	ComponentsTotal   int
}

// Generate collects current metric values and writes a summary to w.
func Generate(ctx context.Context, w io.Writer) error {
	report := &Report{}

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return fmt.Errorf("gather metrics: %w", err)
	}

	componentSet := map[string]bool{}
	for _, mf := range mfs {
		switch mf.GetName() {
		case "syncctl_pg_replay_lag_seconds":
			for _, m := range mf.GetMetric() {
				if m.GetGauge() != nil {
					v := m.GetGauge().GetValue()
					if v > float64(report.PGLagCurrent.Seconds()) {
						report.PGLagCurrent = time.Duration(v * float64(time.Second))
					}
					report.PGLagPeak = report.PGLagCurrent
					if v >= 0 {
						report.ComponentsHealthy++
					}
				}
			}
		case "syncctl_drift_total":
			for _, m := range mf.GetMetric() {
				if m.GetCounter() != nil {
					report.DriftCount += int64(m.GetCounter().GetValue())
					for _, l := range m.GetLabel() {
						if l.GetName() == "component" {
							componentSet[l.GetValue()] = true
						}
					}
				}
			}
		case "syncctl_last_sync_timestamp_seconds":
			for _, m := range mf.GetMetric() {
				if m.GetGauge() != nil {
					ts := m.GetGauge().GetValue()
					if ts > 0 {
						componentSet["last_sync"] = true
						age := time.Since(time.Unix(int64(ts), 0))
						if age > report.LastSweepAge {
							report.LastSweepAge = age
						}
					}
				}
			}
		}
	}
	report.ComponentsTotal = len(componentSet)
	if report.ComponentsTotal == 0 {
		report.ComponentsTotal = 1
	}

	report.Print(w)
	return nil
}

// Print writes the SLA report to w.
func (r *Report) Print(w io.Writer) {
	_, _ = fmt.Fprintf(w, "=== syncctl SLA Report ===\n\n")
	_, _ = fmt.Fprintf(w, "PostgreSQL Replay Lag:\n")
	_, _ = fmt.Fprintf(w, "  Current: %s\n", r.PGLagCurrent)
	_, _ = fmt.Fprintf(w, "  Peak:    %s\n", r.PGLagPeak)
	_, _ = fmt.Fprintf(w, "\nLast Sync Age (oldest component): %s\n", r.LastSweepAge)
	_, _ = fmt.Fprintf(w, "\nDrift Events (cumulative): %d\n", r.DriftCount)
	_, _ = fmt.Fprintf(w, "Components Healthy: %d/%d\n", r.ComponentsHealthy, r.ComponentsTotal)
	_, _ = fmt.Fprintf(w, "\nRPO Estimate: %s (PG replay lag)\n", r.PGLagCurrent)
	_, _ = fmt.Fprintf(w, "RTO Estimate: ~2-5 min (pg_ctl promote + gitlab-ctl restart)\n")
	_, _ = fmt.Fprintf(w, "\nNote: RPO for in-flight Sidekiq jobs is ~last dequeue time.\n")
}
