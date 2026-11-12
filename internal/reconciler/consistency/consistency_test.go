package consistency

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// mockQuerier answers rowCount's aggregate query as Postgres would, so a
// missing table yields a row with zero relations rather than no row.
type mockQuerier struct {
	counts     map[string]int64
	partitions map[string][]int64
	err        error
	sql        string
}

func (m *mockQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	m.sql = sql
	if m.err != nil {
		return &mockRow{err: m.err}
	}
	table, _ := args[0].(string)
	if leaves, ok := m.partitions[table]; ok {
		return &mockRow{relations: int64(1 + len(leaves)), leaves: leaves}
	}
	if count, ok := m.counts[table]; ok {
		return &mockRow{relations: 1, leaves: []int64{count}}
	}
	return &mockRow{}
}

type mockRow struct {
	relations int64
	leaves    []int64
	err       error
}

func (r *mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 2 {
		return fmt.Errorf("mockRow: scanned into %d targets, want 2", len(dest))
	}
	*(dest[0].(*int64)) = r.relations
	*(dest[1].(*[]int64)) = r.leaves
	return nil
}

func allTables(n int64) map[string]int64 {
	m := make(map[string]int64, len(tablesToCount))
	for _, table := range tablesToCount {
		m[table] = n
	}
	return m
}

func TestReconcileAllMatch(t *testing.T) {
	primary := &mockQuerier{counts: allTables(100)}
	secondary := &mockQuerier{counts: allTables(100)}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if !res.OK || res.Remaining != 0 {
		t.Errorf("expected OK with nothing remaining, got remaining=%d: %s", res.Remaining, res.Detail)
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
	empty := &mockQuerier{}
	r := (&Reconciler{reposPath: ""}).WithPools(empty, empty)
	res := r.Reconcile(context.Background())
	if res.Remaining != len(tablesToCount) {
		t.Errorf("Remaining = %d, want %d: a missing table must not compare as 0 == 0", res.Remaining, len(tablesToCount))
	}
	want := "p_ci_builds primary count error: " + errTableNotFound.Error()
	if !strings.Contains(res.Detail, want) {
		t.Errorf("Detail lacks %q: %s", want, res.Detail)
	}
}

func TestTablesToCountUsesPartitionedCITables(t *testing.T) {
	want := map[string]bool{"p_ci_builds": true, "p_ci_pipelines": true}
	for _, table := range tablesToCount {
		if table == "ci_builds" || table == "ci_pipelines" {
			t.Errorf("tablesToCount has %q; CE keeps those rows under the partitioned p_%s", table, table)
		}
		delete(want, table)
	}
	for table := range want {
		t.Errorf("tablesToCount lacks %q", table)
	}
}

func TestReconcileDetectsDriftInPartitionedCITables(t *testing.T) {
	primary := &mockQuerier{counts: allTables(100), partitions: map[string][]int64{
		"p_ci_builds": {5000, 5000}, "p_ci_pipelines": {800},
	}}
	secondary := &mockQuerier{counts: allTables(100), partitions: map[string][]int64{
		"p_ci_builds": {5000, 0}, "p_ci_pipelines": {800},
	}}
	r := (&Reconciler{reposPath: ""}).WithPools(primary, secondary)
	res := r.Reconcile(context.Background())
	if res.OK || !strings.Contains(res.Detail, "p_ci_builds drift: primary=10000 secondary=5000") {
		t.Errorf("expected p_ci_builds drift, got OK=%v: %s", res.OK, res.Detail)
	}
}

func TestRowCountMissingTableIsError(t *testing.T) {
	n, err := rowCount(context.Background(), &mockQuerier{}, "p_ci_builds")
	if !errors.Is(err, errTableNotFound) {
		t.Fatalf("rowCount on a missing table = %d, %v; want errTableNotFound", n, err)
	}
}

func TestRowCountPlainTable(t *testing.T) {
	q := &mockQuerier{counts: map[string]int64{"projects": 42, "users": -1}}
	if n, err := rowCount(context.Background(), q, "projects"); n != 42 || err != nil {
		t.Errorf("rowCount(projects) = %d, %v; want 42, nil", n, err)
	}
	if _, err := rowCount(context.Background(), q, "users"); !errors.Is(err, errCountUnknown) {
		t.Errorf("rowCount(users) error = %v; want errCountUnknown", err)
	}
}

func TestRowCountSumsLeafPartitions(t *testing.T) {
	tests := []struct {
		name    string
		leaves  []int64
		want    int64
		wantErr error
	}{
		{"sums every leaf", []int64{1000, 200, 30}, 1230, nil},
		{"skips a never-analyzed leaf", []int64{1000, -1, 30}, 1030, nil},
		{"unknown when no leaf is analyzed", []int64{-1, -1}, 0, errCountUnknown},
		{"no partitions holds no rows", nil, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mockQuerier{partitions: map[string][]int64{"p_ci_builds": tc.leaves}}
			got, err := rowCount(context.Background(), q, "p_ci_builds")
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Errorf("rowCount = %d, %v; want %d, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestRowCountQueryWalksPartitionTree(t *testing.T) {
	q := &mockQuerier{counts: map[string]int64{"projects": 1}}
	if _, err := rowCount(context.Background(), q, "projects"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WITH RECURSIVE",
		"c.relkind IN ('r', 'p')",
		"JOIN pg_inherits i ON i.inhparent = t.oid",
		"WHERE t.relkind = 'p'",
		"FILTER (WHERE relkind = 'r')",
	} {
		if !strings.Contains(q.sql, want) {
			t.Errorf("rowCount query lacks %q:\n%s", want, q.sql)
		}
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
