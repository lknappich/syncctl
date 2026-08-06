package consistency

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSampleGitFsckEmptyPath(t *testing.T) {
	r := &Reconciler{reposPath: "", samplePct: 0.1}
	if n := r.sampleGitFsck(context.Background()); n != 0 {
		t.Errorf("expected 0 failures on empty path, got %d", n)
	}
}

func TestSampleGitFsckNoRepos(t *testing.T) {
	dir := t.TempDir()
	r := &Reconciler{reposPath: dir, samplePct: 1.0}
	if n := r.sampleGitFsck(context.Background()); n != 0 {
		t.Errorf("expected 0 failures on dir with no repos, got %d", n)
	}
}

func TestSampleGitFsckFindsGitDirs(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "group", "project.git")
	if err := os.MkdirAll(filepath.Join(repoDir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Override exec to always succeed (we're testing discovery, not real git).
	oldExec := execCmd
	execCmd = func(ctx context.Context, name string, args ...string) cmdRunner {
		return &fakeCmd{out: []byte("ok"), err: nil}
	}
	t.Cleanup(func() { execCmd = oldExec })

	r := &Reconciler{reposPath: dir, samplePct: 1.0}
	n := r.sampleGitFsck(context.Background())
	if n != 0 {
		t.Errorf("expected 0 fsck failures with stub, got %d", n)
	}
}

type fakeCmd struct {
	out []byte
	err error
}

func (f *fakeCmd) CombinedOutput() ([]byte, error) { return f.out, f.err }

func TestIsApproxEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want bool
	}{
		{"exact match", 1000, 1000, true},
		{"within 10% tolerance", 1000, 1050, true},
		{"within 10% tolerance lower", 1050, 1000, true},
		{"exceeds 10% tolerance", 1000, 200, false},
		{"small values min tolerance 5", 10, 13, true},
		{"small values exceed min tolerance", 10, 20, false},
		{"both zero", 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isApproxEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("isApproxEqual(%d, %d) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// mockQuerier implements rowQuerier.
type mockQuerier struct {
	counts map[string]int64
	err    error
}

func (m *mockQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if m.err != nil {
		return &mockRow{err: m.err}
	}
	table, _ := args[0].(string)
	count, ok := m.counts[table]
	if !ok {
		return &mockRow{err: pgx.ErrNoRows}
	}
	return &mockRow{count: count}
}

type mockRow struct {
	count int64
	err   error
}

func (r *mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.count
	return nil
}

func TestReconcileAllMatch(t *testing.T) {
	counts := map[string]int64{"projects": 100, "namespaces": 50}
	primary := &mockQuerier{counts: counts}
	secondary := &mockQuerier{counts: counts}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
}

func TestReconcileDrift(t *testing.T) {
	primary := &mockQuerier{counts: map[string]int64{"projects": 100}}
	secondary := &mockQuerier{counts: map[string]int64{"projects": 50}}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on drift")
	}
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0")
	}
}

func TestReconcileApproxEqualSkipsDrift(t *testing.T) {
	primary := &mockQuerier{counts: map[string]int64{"projects": 1000}}
	secondary := &mockQuerier{counts: map[string]int64{"projects": 1005}}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK (within tolerance), got: %s", res.Detail)
	}
}

func TestReconcilePrimaryError(t *testing.T) {
	primary := &mockQuerier{err: errors.New("db down")}
	secondary := &mockQuerier{counts: map[string]int64{}}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 on primary error")
	}
}

func TestReconcileSecondaryError(t *testing.T) {
	primary := &mockQuerier{counts: map[string]int64{}}
	secondary := &mockQuerier{err: errors.New("db down")}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if res.Remaining == 0 {
		t.Error("expected Remaining > 0 on secondary error")
	}
}

func TestReconcileTableMissing(t *testing.T) {
	// Missing table returns pgx.ErrNoRows -> rowCount returns (0, nil).
	primary := &mockQuerier{counts: map[string]int64{}}
	secondary := &mockQuerier{counts: map[string]int64{}}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK when tables missing, got: %s", res.Detail)
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "consistency_sweep" {
		t.Errorf("Name() = %q", r.Name())
	}
}

// stubCmd returns canned output for execCmd.
type stubCmd struct {
	out []byte
	err error
}

func (s *stubCmd) CombinedOutput() ([]byte, error) { return s.out, s.err }

func TestExecGitFsckSuccess(t *testing.T) {
	orig := execCmd
	execCmd = func(ctx context.Context, name string, args ...string) cmdRunner {
		return &stubCmd{out: []byte("ok")}
	}
	defer func() { execCmd = orig }()
	out, err := execGitFsck(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("execGitFsck: %v", err)
	}
	if out != "ok" {
		t.Errorf("out = %q", out)
	}
}

func TestExecGitFsckError(t *testing.T) {
	orig := execCmd
	execCmd = func(ctx context.Context, name string, args ...string) cmdRunner {
		return &stubCmd{err: errors.New("fsck failed"), out: []byte("error")}
	}
	defer func() { execCmd = orig }()
	_, err := execGitFsck(context.Background(), "/repo")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGitFsckPasses(t *testing.T) {
	orig := execCmd
	execCmd = func(ctx context.Context, name string, args ...string) cmdRunner {
		return &stubCmd{out: []byte("")}
	}
	defer func() { execCmd = orig }()
	if !gitFsck(context.Background(), "/repo") {
		t.Error("gitFsck should return true on success")
	}
}

func TestGitFsckFails(t *testing.T) {
	orig := execCmd
	execCmd = func(ctx context.Context, name string, args ...string) cmdRunner {
		return &stubCmd{err: errors.New("broken"), out: []byte("corrupt")}
	}
	defer func() { execCmd = orig }()
	if gitFsck(context.Background(), "/repo") {
		t.Error("gitFsck should return false on error")
	}
}

// Sampling with rng.Intn per iteration draws with replacement, so the
// same repo is fsck'd more than once and effective coverage falls below
// consistency_sample_pct. Every sampled repo must be distinct.
func TestSampleGitFsckDrawsWithoutReplacement(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("repo%02d.git", i)), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := map[string]int{}
	orig := execCmd
	execCmd = func(_ context.Context, _ string, args ...string) cmdRunner {
		mu.Lock()
		// args are: -C <path> fsck --no-dangling
		seen[args[1]]++
		mu.Unlock()
		return &fakeCmd{}
	}
	t.Cleanup(func() { execCmd = orig })

	r := &Reconciler{reposPath: root, samplePct: 0.5}
	r.sampleGitFsck(context.Background())

	if len(seen) != 10 {
		t.Errorf("fsck'd %d distinct repos, want 10 (50%% of 20)", len(seen))
	}
	for repo, n := range seen {
		if n != 1 {
			t.Errorf("repo %s sampled %d times; sampling must be without replacement", repo, n)
		}
	}
}
