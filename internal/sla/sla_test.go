package sla

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// metricsHandler serves an exposition-format body like a live syncctl.
func metricsHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(body))
	})
}

func TestGenerateReadsLiveMetrics(t *testing.T) {
	body := fmt.Sprintf(`# HELP syncctl_pg_replay_lag_seconds lag
# TYPE syncctl_pg_replay_lag_seconds gauge
syncctl_pg_replay_lag_seconds{secondary="s1"} 5
syncctl_pg_replay_lag_seconds{secondary="s2"} 12
# HELP syncctl_drift_total drift
# TYPE syncctl_drift_total counter
syncctl_drift_total{component="db:projects",severity="warning"} 3
syncctl_drift_total{component="git_rsync",severity="critical"} 1
# HELP syncctl_last_sync_timestamp_seconds last sync
# TYPE syncctl_last_sync_timestamp_seconds gauge
syncctl_last_sync_timestamp_seconds{component="git_rsync"} %d
`, time.Now().Add(-90*time.Second).Unix())

	srv := httptest.NewServer(metricsHandler(body))
	defer srv.Close()

	var buf bytes.Buffer
	if err := Generate(context.Background(), &buf, srv.URL); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Current: 12s") {
		t.Errorf("expected the highest observed lag across secondaries, got:\n%s", out)
	}
	if !strings.Contains(out, "Drift Events (cumulative): 4") {
		t.Errorf("expected drift counters summed, got:\n%s", out)
	}
	if strings.Contains(out, "RPO Estimate: 0s") {
		t.Errorf("a live endpoint must not produce a zeroed RPO:\n%s", out)
	}
}

// The old implementation gathered its own process registry, which in a
// one-shot command has never recorded anything — so it always printed
// zero lag and zero drift, the reading an operator most wants to see,
// with nothing behind it. An unreachable endpoint must be an error.
func TestGenerateFailsWhenEndpointUnreachable(t *testing.T) {
	srv := httptest.NewServer(metricsHandler(""))
	url := srv.URL
	srv.Close()

	var buf bytes.Buffer
	err := Generate(context.Background(), &buf, url)
	if err == nil {
		t.Fatal("expected an error rather than a zeroed report")
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should be printed when no metrics were read, got: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "syncctl serve") {
		t.Errorf("the error should hint at the cause, got: %v", err)
	}
}

func TestGenerateFailsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := Generate(context.Background(), &buf, srv.URL); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestDefaultMetricsURL(t *testing.T) {
	tests := []struct{ addr, want string }{
		{"", "http://127.0.0.1:9101/metrics"},
		{":9101", "http://127.0.0.1:9101/metrics"},
		{"127.0.0.1:9101", "http://127.0.0.1:9101/metrics"},
		{"10.0.0.5:9999", "http://10.0.0.5:9999/metrics"},
	}
	for _, tc := range tests {
		if got := DefaultMetricsURL(tc.addr); got != tc.want {
			t.Errorf("DefaultMetricsURL(%q) = %q, want %q", tc.addr, got, tc.want)
		}
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
	for _, want := range []string{"5s", "10s", "30s", "RPO", "RTO", "2/3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %s", want, out)
		}
	}
}
