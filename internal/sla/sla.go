// Package sla computes an RPO/RTO summary from a running syncctl's
// metrics endpoint.
//
// It scrapes over HTTP rather than reading the local process registry.
// `syncctl sla` is a one-shot command in its own process: no reconciler
// has ever run there, so gathering locally returned freshly-initialised
// collectors and printed "0s lag, 0 drift" — the reading an operator most
// wants to see, produced without measuring anything.
package sla

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
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

// Generate scrapes metricsURL and writes a summary to w. An unreachable
// endpoint is an error, not an empty report: reporting zeros for a
// syncctl that is not running would read as perfect health.
func Generate(ctx context.Context, w io.Writer, metricsURL string) error {
	mfs, err := scrape(ctx, metricsURL)
	if err != nil {
		return err
	}
	report := buildReport(mfs)
	report.Print(w)
	return nil
}

func scrape(ctx context.Context, metricsURL string) (map[string]*dto.MetricFamily, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape %s: %w (is `syncctl serve` running, and is metrics.addr reachable from here?)",
			metricsURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape %s: status %d", metricsURL, resp.StatusCode)
	}
	// The scheme must be set explicitly; a zero TextParser panics on the
	// first metric name it sees.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	mfs, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse metrics from %s: %w", metricsURL, err)
	}
	return mfs, nil
}

func buildReport(mfs map[string]*dto.MetricFamily) *Report {
	report := &Report{}
	componentSet := map[string]bool{}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "syncctl_pg_replay_lag_seconds":
			for _, m := range mf.GetMetric() {
				if m.GetGauge() == nil {
					continue
				}
				v := m.GetGauge().GetValue()
				if v > report.PGLagCurrent.Seconds() {
					report.PGLagCurrent = time.Duration(v * float64(time.Second))
				}
				report.PGLagPeak = report.PGLagCurrent
				if v >= 0 {
					report.ComponentsHealthy++
				}
			}
		case "syncctl_drift_total":
			for _, m := range mf.GetMetric() {
				if m.GetCounter() == nil {
					continue
				}
				report.DriftCount += int64(m.GetCounter().GetValue())
				for _, l := range m.GetLabel() {
					if l.GetName() == "component" {
						componentSet[l.GetValue()] = true
					}
				}
			}
		case "syncctl_last_sync_timestamp_seconds":
			for _, m := range mf.GetMetric() {
				if m.GetGauge() == nil {
					continue
				}
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

	report.ComponentsTotal = len(componentSet)
	if report.ComponentsTotal == 0 {
		report.ComponentsTotal = 1
	}
	return report
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

// DefaultMetricsURL turns a metrics listen address into a scrape URL,
// substituting loopback for a wildcard bind.
func DefaultMetricsURL(addr string) string {
	if addr == "" {
		addr = "127.0.0.1:9101"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr + "/metrics"
}
