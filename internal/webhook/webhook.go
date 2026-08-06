// Package webhook implements an HTTP receiver for GitLab push/create/delete
// webhooks. When a webhook arrives, it triggers an immediate per-project
// git sync for the affected project, bypassing the normal sweep interval.
// This reduces lag to near-real-time for hot paths while periodic sweeps
// remain the safety net.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/logging"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/projectpath"
)

// Server receives GitLab webhooks and triggers immediate sync.
type Server struct {
	addr string
	// tokenDigest is the SHA-256 of the configured secret. Comparing
	// digests keeps the comparison fixed-width: hmac.Equal returns early
	// on a length mismatch, which would leak the secret's length.
	tokenDigest [sha256.Size]byte
	trigger     TriggerFunc
	mux         *http.ServeMux
	srv         *http.Server
	ctx         context.Context
	sem         chan struct{} // concurrency cap for triggered syncs
	tlsCert     string
	tlsKey      string
}

// TriggerFunc is called when a webhook is received. It receives the
// project path (e.g. "group/subgroup/project") and the event type.
// Implementations should run git fetch for the specific project.
type TriggerFunc func(ctx context.Context, projectPath, eventType string) error

// NewServer creates a webhook receiver. Returns an error if secretToken
// is empty — an empty token would cause hmac.Equal("","") to accept
// every unauthenticated request.
func NewServer(addr, secretToken string, trigger TriggerFunc) (*Server, error) {
	if secretToken == "" {
		return nil, fmt.Errorf("webhook secret_token must not be empty")
	}
	s := &Server{
		addr:        addr,
		tokenDigest: sha256.Sum256([]byte(secretToken)),
		trigger:     trigger,
		mux:         http.NewServeMux(),
		sem:         make(chan struct{}, 8), // max 8 concurrent webhook-triggered syncs
	}
	s.mux.HandleFunc("/webhook", s.handleWebhook)
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return s, nil
}

// WithTLS serves the receiver over HTTPS using the given certificate and
// key. Without it the GitLab secret token crosses the network in a
// cleartext header on every delivery, so a TLS-terminating proxy is the
// only other acceptable deployment.
func (s *Server) WithTLS(certFile, keyFile string) *Server {
	s.tlsCert = certFile
	s.tlsKey = keyFile
	return s
}

// Start blocks until ctx is cancelled or the server errors.
func (s *Server) Start(ctx context.Context) error {
	s.ctx = ctx
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	if s.tlsCert != "" {
		go func() { errCh <- s.srv.ListenAndServeTLS(s.tlsCert, s.tlsKey) }()
	} else {
		go func() { errCh <- s.srv.ListenAndServe() }()
	}
	log.Info().Str("addr", s.addr).Bool("tls", s.tlsCert != "").
		Msg("webhook server listening")
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

// handleWebhook validates the GitLab webhook token and parses the payload.
func (s *Server) handleWebhook(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// GitLab sends the secret token in the X-Gitlab-Token header.
	token := sha256.Sum256([]byte(req.Header.Get("X-Gitlab-Token")))
	if !hmac.Equal(token[:], s.tokenDigest[:]) {
		metrics.DriftTotal.WithLabelValues("webhook", "critical").Inc()
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	eventType := req.Header.Get("X-Gitlab-Event")
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	projectPath, err := extractProjectPath(body)
	if err != nil {
		log.Warn().Err(err).Str("event", eventType).Msg("webhook: extract project path")
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := projectpath.Validate(projectPath); err != nil {
		log.Warn().Err(err).Str("project", logging.ProjectPath(projectPath)).Str("event", eventType).
			Msg("webhook: invalid project path")
		metrics.DriftTotal.WithLabelValues("webhook", "warning").Inc()
		w.WriteHeader(http.StatusOK)
		return
	}

	// Trigger sync asynchronously with a concurrency cap so a flood of
	// valid-token requests cannot spawn unbounded goroutines (DoS).
	select {
	case s.sem <- struct{}{}:
	default:
		metrics.DriftTotal.WithLabelValues("webhook", "warning").Inc()
		log.Warn().Msg("webhook: concurrency limit reached, dropping trigger")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// Trigger sync asynchronously so we respond to GitLab quickly.
	go func() {
		defer func() { <-s.sem }()
		parent := s.ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		start := time.Now()
		if err := s.trigger(ctx, projectPath, eventType); err != nil {
			log.Error().Err(err).Str("project", logging.ProjectPath(projectPath)).Str("event", eventType).
				Msg("webhook-triggered sync failed")
			metrics.SyncDurationSeconds.WithLabelValues("webhook_trigger", "error").
				Observe(time.Since(start).Seconds())
			return
		}
		metrics.SyncDurationSeconds.WithLabelValues("webhook_trigger", "ok").
			Observe(time.Since(start).Seconds())
		metrics.LastSyncTimestamp.WithLabelValues("webhook_trigger").
			Set(float64(time.Now().Unix()))
		log.Debug().Str("project", logging.ProjectPath(projectPath)).Str("event", eventType).
			Msg("webhook-triggered sync complete")
	}()

	w.WriteHeader(http.StatusOK)
}

// extractProjectPath pulls the project path from the webhook payload.
// GitLab webhooks include a "project" object with "path_with_namespace"
// in push, tag, and most system webhooks.
func extractProjectPath(body []byte) (string, error) {
	var payload struct {
		Project struct {
			PathWithNamespace string `json:"path_with_namespace"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse webhook payload: %w", err)
	}
	if payload.Project.PathWithNamespace == "" {
		return "", fmt.Errorf("no project.path_with_namespace in payload")
	}
	return payload.Project.PathWithNamespace, nil
}

// TriggerManager debounces rapid-fire webhooks for the same project so
// we don't run multiple concurrent fetches for the same repo during a
// push burst.
type TriggerManager struct {
	mu      sync.Mutex
	pending map[string]*pendingTrigger
	trigger TriggerFunc
}

// pendingTrigger identifies one in-flight invocation. Identity matters:
// the cleanup must remove only its own entry, never a newer one that
// replaced it.
type pendingTrigger struct {
	cancel context.CancelFunc
}

// NewTriggerManager wraps a TriggerFunc with per-project debouncing.
func NewTriggerManager(trigger TriggerFunc) *TriggerManager {
	return &TriggerManager{
		pending: map[string]*pendingTrigger{},
		trigger: trigger,
	}
}

// Trigger debounces: if a sync for this project is already pending,
// cancels it and starts a new one after a short delay.
func (m *TriggerManager) Trigger(ctx context.Context, projectPath, eventType string) error {
	ctx2, cancel := context.WithCancel(ctx)
	self := &pendingTrigger{cancel: cancel}

	m.mu.Lock()
	if prev, ok := m.pending[projectPath]; ok {
		prev.cancel()
	}
	m.pending[projectPath] = self
	m.mu.Unlock()

	// The stored cancel lets a later trigger pre-empt this one; this
	// deferred call releases the context when the trigger finishes.
	// Without it the context lives until the parent is cancelled, so a
	// project that receives exactly one webhook leaks one until shutdown.
	defer cancel()

	defer func() {
		m.mu.Lock()
		// A newer trigger may already own the slot. Deleting
		// unconditionally dropped its cancel func, so the webhook after
		// that one found nothing to pre-empt and ran concurrently with
		// the in-flight fetch — the exact thing debouncing prevents.
		if m.pending[projectPath] == self {
			delete(m.pending, projectPath)
		}
		m.mu.Unlock()
	}()

	// Debounce: wait 2 seconds for burst coalescing, then fetch.
	select {
	case <-ctx2.Done():
		return ctx2.Err()
	case <-time.After(2 * time.Second):
	}

	return m.trigger(ctx2, projectPath, eventType)
}
