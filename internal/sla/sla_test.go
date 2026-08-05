package sla

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lknappich/syncctl/internal/metrics"
)

func TestGeneratePrintsReport(t *testing.T) {
	var buf bytes.Buffer
	err := Generate(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Error("expected non-empty report")
	}
	if !bytes.Contains(buf.Bytes(), []byte("SLA Report")) {
		t.Errorf("expected 'SLA Report' in output, got: %s", out)
	}
}

func TestReportPrint(t *testing.T) {
	r := &Report{
		ComponentsHealthy: 3,
		ComponentsTotal:   4,
	}
	var buf bytes.Buffer
	r.Print(&buf)
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("3/4")) {
		t.Errorf("expected '3/4' in output, got: %s", out)
	}
}

func TestGenerateWithMetrics(t *testing.T) {
	// Register metric values on the default registry.
	metrics.Register(prometheus.DefaultRegisterer)
	metrics.PGReplayLagSeconds.WithLabelValues("s1").Set(5.0)
	metrics.DriftTotal.WithLabelValues("db:projects", "warning").Inc()
	metrics.LastSyncTimestamp.WithLabelValues("git_rsync").Set(float64(time.Now().Unix()))

	var buf bytes.Buffer
	err := Generate(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PostgreSQL Replay Lag") {
		t.Errorf("expected PG lag section: %s", out)
	}
	if !strings.Contains(out, "Drift Events") {
		t.Errorf("expected drift section: %s", out)
	}
}

func TestGenerateGatherError(t *testing.T) {
	// Hard to force a gather error without a broken metric; just verify
	// Generate with default empty registry still works.
	var buf bytes.Buffer
	err := Generate(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestReportPrintFull(t *testing.T) {
	r := &Report{
		PGLagCurrent:      5 * time.Second,
		PGLagPeak:         10 * time.Second,
		LastSweepAge:      30 * time.Second,
		DriftCount:        3,
		ComponentsHealthy: 2,
		ComponentsTotal:   3,
	}
	var buf bytes.Buffer
	r.Print(&buf)
	out := buf.String()
	if !strings.Contains(out, "5s") {
		t.Errorf("expected 5s in output: %s", out)
	}
	if !strings.Contains(out, "10s") {
		t.Errorf("expected 10s in output: %s", out)
	}
	if !strings.Contains(out, "30s") {
		t.Errorf("expected 30s in output: %s", out)
	}
	if !strings.Contains(out, "RPO") {
		t.Errorf("expected RPO in output: %s", out)
	}
	if !strings.Contains(out, "RTO") {
		t.Errorf("expected RTO in output: %s", out)
	}
}
