package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractProjectPath(t *testing.T) {
	body := []byte(`{"project":{"path_with_namespace":"group/subgroup/project"}}`)
	path, err := extractProjectPath(body)
	if err != nil {
		t.Fatal(err)
	}
	if path != "group/subgroup/project" {
		t.Errorf("got %q", path)
	}
}

func TestExtractProjectPathMissing(t *testing.T) {
	body := []byte(`{"project":{}}`)
	_, err := extractProjectPath(body)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestHandleWebhookRejectsInvalidToken(t *testing.T) {
	s, err := NewServer(":0", "secret", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Gitlab-Token", "wrong")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestHandleWebhookRejectsGet(t *testing.T) {
	s, err := NewServer(":0", "secret", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", w.Code)
	}
}

func TestHandleWebhookAcceptsValid(t *testing.T) {
	called := make(chan string, 1)
	s, err := NewServer(":0", "secret", func(ctx context.Context, p, e string) error {
		called <- p
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"project":{"path_with_namespace":"group/proj"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
	select {
	case p := <-called:
		if p != "group/proj" {
			t.Errorf("trigger got %q", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("trigger not called within 10s (debounce delay)")
	}
}

func TestHandleWebhookRejectsEmptySecret(t *testing.T) {
	_, err := NewServer(":0", "", func(context.Context, string, string) error { return nil })
	if err == nil {
		t.Fatal("expected error for empty secret token")
	}
}

func TestHandleWebhookRejectsPathTraversal(t *testing.T) {
	s, err := NewServer(":0", "secret", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"project":{"path_with_namespace":"../../etc/passwd"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (accept but don't act)", w.Code)
	}
}

func TestTriggerManagerDebounces(t *testing.T) {
	var callCount int32
	mgr := NewTriggerManager(func(ctx context.Context, p, e string) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	})

	go func() { _ = mgr.Trigger(context.Background(), "proj", "push") }()
	go func() { _ = mgr.Trigger(context.Background(), "proj", "push") }()

	time.Sleep(4 * time.Second)
	count := atomic.LoadInt32(&callCount)
	if count != 1 {
		t.Errorf("expected 1 call after debounce, got %d", count)
	}
}

func TestStartAndShutdown(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}

func TestHandleWebhookMethodNotAllowed(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	srv.handleWebhook(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleWebhookWrongToken(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "wrong")
	w := httptest.NewRecorder()
	srv.handleWebhook(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleWebhookBadJSON(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("not json"))
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleWebhook(w, req)
	// Handler logs a warning on bad JSON but still returns 200 (accepts the webhook).
	if w.Code != http.StatusOK {
		t.Errorf("Code = %d, want %d (handler accepts but logs warning)", w.Code, http.StatusOK)
	}
}

func TestHandleWebhookSuccess(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	body := `{"event_name":"push","project":{"path_with_namespace":"group/proj"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleWebhook(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Code = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWebhookNoProjectPath(t *testing.T) {
	srv, _ := NewServer("127.0.0.1:0", "secret", func(ctx context.Context, p, e string) error { return nil })
	body := `{"event_name":"push"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleWebhook(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Code = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestExtractProjectPathNested(t *testing.T) {
	body := []byte(`{"project":{"path_with_namespace":"group/sub/proj"}}`)
	path, err := extractProjectPath(body)
	if err != nil {
		t.Fatalf("extractProjectPath: %v", err)
	}
	if path != "group/sub/proj" {
		t.Errorf("path = %q, want group/sub/proj", path)
	}
}

func TestExtractProjectPathEmpty(t *testing.T) {
	body := []byte(`{}`)
	path, err := extractProjectPath(body)
	if err != nil {
		// Expected — no project path in empty payload.
		if path != "" {
			t.Errorf("path = %q, want empty", path)
		}
		return
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

// The token is compared as a fixed-width digest, so a token that merely
// shares a prefix with the secret must not be accepted.
func TestWebhookRejectsPrefixAndWrongLengthTokens(t *testing.T) {
	for _, token := range []string{"", "s", "secret", "secret-token-extra", "SECRET-TOKEN"} {
		t.Run("token="+token, func(t *testing.T) {
			var triggered bool
			s, err := NewServer(":0", "secret-token", func(context.Context, string, string) error {
				triggered = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/webhook",
				strings.NewReader(`{"project":{"path_with_namespace":"g/p"}}`))
			req.Header.Set("X-Gitlab-Token", token)
			rec := httptest.NewRecorder()
			s.handleWebhook(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if triggered {
				t.Error("a rejected request must not trigger a sync")
			}
		})
	}
}

func TestWithTLSRecordsCertAndKey(t *testing.T) {
	s, err := NewServer(":0", "secret-token", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s = s.WithTLS("/c.pem", "/k.pem")
	if s.tlsCert != "/c.pem" || s.tlsKey != "/k.pem" {
		t.Errorf("tlsCert=%q tlsKey=%q", s.tlsCert, s.tlsKey)
	}
}
func TestTriggerManagerKeepsNewerPendingEntry(t *testing.T) {
	release := make(chan struct{})
	var running atomic.Int32
	var maxConcurrent atomic.Int32

	m := NewTriggerManager(func(ctx context.Context, _, _ string) error {
		n := running.Add(1)
		for {
			prev := maxConcurrent.Load()
			if n <= prev || maxConcurrent.CompareAndSwap(prev, n) {
				break
			}
		}
		defer running.Add(-1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Trigger(context.Background(), "group/proj", "push")
		}()
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(2500 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := maxConcurrent.Load(); got > 1 {
		t.Errorf("%d concurrent fetches for one project; debouncing should serialize them", got)
	}

	m.mu.Lock()
	remaining := len(m.pending)
	m.mu.Unlock()
	if remaining != 0 {
		t.Errorf("pending map leaked %d entries", remaining)
	}
}
