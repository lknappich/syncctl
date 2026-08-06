package reconciler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeReconciler is a test stub that counts calls and returns a canned result.
type fakeReconciler struct {
	name   string
	result Result
	calls  int32
}

func (f *fakeReconciler) Name() string { return f.name }
func (f *fakeReconciler) Reconcile(_ context.Context) Result {
	atomic.AddInt32(&f.calls, 1)
	return f.result
}

func TestRunnerFiresImmediately(t *testing.T) {
	r1 := &fakeReconciler{name: "r1", result: Result{OK: true, Detail: "ok"}}
	r2 := &fakeReconciler{name: "r2", result: Result{OK: false, Detail: "drift", Remaining: 3}}

	runner := NewRunner(time.Hour, r1, r2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runner.Run(ctx); close(done) }()

	// The runner fires once immediately, then waits on the ticker.
	// Cancel after we've seen at least one call.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if atomic.LoadInt32(&r1.calls) < 1 {
		t.Errorf("r1 not called, calls=%d", r1.calls)
	}
	if atomic.LoadInt32(&r2.calls) < 1 {
		t.Errorf("r2 not called, calls=%d", r2.calls)
	}
}

func TestRunnerStopsOnContextCancel(t *testing.T) {
	r := &fakeReconciler{name: "r", result: Result{OK: true}}
	runner := NewRunner(10*time.Millisecond, r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runner.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop within 1s")
	}
}

func TestQualifyName(t *testing.T) {
	tests := []struct {
		base, site, want string
	}{
		{"git_rsync", "secondary-us", "git_rsync@secondary-us"},
		{"git_rsync", "", "git_rsync"},
		{"db:projects", "secondary-eu", "db:projects@secondary-eu"},
	}
	for _, tc := range tests {
		if got := QualifyName(tc.base, tc.site); got != tc.want {
			t.Errorf("QualifyName(%q, %q) = %q, want %q", tc.base, tc.site, got, tc.want)
		}
	}
}
