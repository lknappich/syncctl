// Package metrics exposes Prometheus collectors and an HTTP server for them.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

// Default registry we use; allows tests to reset.
var Registry = prometheus.DefaultRegisterer

// Collectors shared across reconcilers.
var (
	PGReplayLagSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "syncctl",
			Name:      "pg_replay_lag_seconds",
			Help:      "PostgreSQL streaming replication replay lag in seconds, per secondary.",
		},
		[]string{"secondary"},
	)

	SyncDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "syncctl",
			Name:      "sync_duration_seconds",
			Help:      "Duration of a sync reconciler run.",
			Buckets:   prometheus.ExponentialBuckets(0.1, 2, 12),
		},
		[]string{"component", "result"},
	)

	DriftTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "syncctl",
			Name:      "drift_total",
			Help:      "Total number of detected drifts, by component and severity.",
		},
		[]string{"component", "severity"},
	)

	LastSyncTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "syncctl",
			Name:      "last_sync_timestamp_seconds",
			Help:      "Unix timestamp of the last successful sync, per component.",
		},
		[]string{"component"},
	)
)

// Register registers all collectors on the provided registerer.
//
// Duplicate registration of the same collector is not an error worth
// killing the process over — MustRegister panics, so a second call (say,
// a command that both registers and delegates to one that does) took the
// whole binary down. Already-registered is treated as success; anything
// else is returned.
func Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{
		PGReplayLagSeconds,
		SyncDurationSeconds,
		DriftTotal,
		LastSyncTimestamp,
	} {
		if err := reg.Register(c); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				continue
			}
			return fmt.Errorf("register collector: %w", err)
		}
	}
	return nil
}

// Server serves the /metrics endpoint. Callers should Start() it and
// cancel the context to stop.
type Server struct {
	addr    string
	handler http.Handler
	srv     *http.Server
}

// NewServer returns a metrics server bound to addr.
func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return &Server{addr: addr, handler: mux}
}

// Start blocks until ctx is cancelled or the server errors.
func (s *Server) Start(ctx context.Context) error {
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.ListenAndServe() }()
	log.Info().Str("addr", s.addr).Msg("metrics server listening")
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
