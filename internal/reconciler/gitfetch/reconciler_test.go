package gitfetch

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lknappich/syncctl/internal/localcmd"
)

func TestRepoDiskPathLegacy(t *testing.T) {
	got := repoDiskPath("default", false, "group/subgroup/project", 42)
	want := "group/subgroup/project.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoDiskPathLegacyEmptyRoute(t *testing.T) {
	got := repoDiskPath("default", false, "", 42)
	if got != "" {
		t.Errorf("expected empty path for empty route, got %q", got)
	}
}

func TestRepoDiskPathHashed(t *testing.T) {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "%d", 42)
	full := hex.EncodeToString(h.Sum(nil))
	got := repoDiskPath("default", true, "ignored", 42)
	want := fmt.Sprintf("@hashed/%s/%s.git", full[:2], full)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSha1HexDeterministic(t *testing.T) {
	h1 := sha1Hex(1)
	h2 := sha1Hex(1)
	if h1 != h2 {
		t.Errorf("sha1Hex not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 40 {
		t.Errorf("expected 40-char hex, got %d", len(h1))
	}
}

func TestFetchProjectEmptyPath(t *testing.T) {
	r := &Reconciler{}
	err := r.FetchProject(context.TODO(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestFetchProjectRejectsTraversal(t *testing.T) {
	r := &Reconciler{}
	err := r.FetchProject(context.TODO(), "../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for traversal path")
	}
}

// mockGitRunner records calls and returns canned output.
type mockGitRunner struct {
	out     []byte
	err     error
	calls   []gitCall
	perPath map[string][]byte
	perErr  map[string]error
}

type gitCall struct {
	name string
	args []string
	env  []string
}

func (m *mockGitRunner) Run(ctx context.Context, name string, args, env []string) ([]byte, error) {
	m.calls = append(m.calls, gitCall{name, args, env})
	// Match by env GIT_SSH_COMMAND containing the remote URL hint.
	for _, e := range env {
		if strings.Contains(e, "FAIL") && m.err != nil {
			return m.out, m.err
		}
	}
	return m.out, m.err
}

var _ localcmd.Runner = (*mockGitRunner)(nil)

func TestReconcileNoProjects(t *testing.T) {
	pool := &mockPool{rows: []projectRow{}}
	r := (&Reconciler{reposPath: "/r", maxParallel: 1}).WithPool(pool)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK with no projects, got: %s", res.Detail)
	}
}

func TestReconcilePoolError(t *testing.T) {
	pool := &mockPool{err: errors.New("db down")}
	r := (&Reconciler{reposPath: "/r", maxParallel: 1}).WithPool(pool)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on pool error")
	}
}

func TestReconcileFetchOneSuccess(t *testing.T) {
	pool := &mockPool{rows: []projectRow{{ID: 1, RepoPath: "group/proj.git"}}}
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{reposPath: "/r", maxParallel: 1}).WithPool(pool).WithRunner(runner)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 git call, got %d", len(runner.calls))
	}
}

func TestReconcileFetchOneFailure(t *testing.T) {
	pool := &mockPool{rows: []projectRow{{ID: 1, RepoPath: "group/proj.git"}}}
	runner := &mockGitRunner{err: errors.New("fetch failed")}
	r := (&Reconciler{reposPath: "/r", maxParallel: 1}).WithPool(pool).WithRunner(runner)
	res := r.Reconcile(context.Background())
	// fetchOne returns false on error; Reconcile still reports OK if no
	// concurrency error, but counts the failure.
	_ = res
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 git call, got %d", len(runner.calls))
	}
}

func TestFetchProjectSuccess(t *testing.T) {
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{reposPath: "/r"}).WithRunner(runner)
	err := r.FetchProject(context.TODO(), "group/proj")
	if err != nil {
		t.Fatalf("FetchProject: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 git call, got %d", len(runner.calls))
	}
}

func TestFetchProjectDryRun(t *testing.T) {
	runner := &mockGitRunner{}
	r := (&Reconciler{reposPath: "/r", dryRun: true}).WithRunner(runner)
	err := r.FetchProject(context.TODO(), "group/proj")
	if err != nil {
		t.Fatalf("dry-run FetchProject should succeed: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("dry-run should not invoke git, got %d calls", len(runner.calls))
	}
}

func TestSetMaxParallel(t *testing.T) {
	r := &Reconciler{}
	r.SetMaxParallel(16)
	if r.maxParallel != 16 {
		t.Errorf("maxParallel = %d, want 16", r.maxParallel)
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "git_fetch" {
		t.Errorf("Name() = %q, want git_fetch", r.Name())
	}
}

// --- mock pool for listProjects ---

type mockPool struct {
	rows []projectRow
	err  error
}

func (p *mockPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &mockRows{rows: p.rows}, nil
}

type mockRows struct {
	rows []projectRow
	idx  int
}

func (m *mockRows) Next() bool {
	if m.idx >= len(m.rows) {
		return false
	}
	m.idx++
	return true
}

func (m *mockRows) Scan(dest ...any) error {
	r := m.rows[m.idx-1]
	*(dest[0].(*int32)) = r.ID
	*(dest[1].(*string)) = "default"
	// namespaceID as sql.NullInt64 valid=false
	dest[2].(*sql.NullInt64).Valid = false
	// hashed bool
	*(dest[3].(*bool)) = false
	// routePath as sql.NullString
	dest[4].(*sql.NullString).String = r.RepoPath
	dest[4].(*sql.NullString).Valid = true
	return nil
}

func (m *mockRows) Close()                                       {}
func (m *mockRows) Err() error                                   { return nil }
func (m *mockRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (m *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (m *mockRows) Values() ([]any, error)                       { return nil, nil }
func (m *mockRows) RawValues() [][]byte                          { return nil }
func (m *mockRows) Conn() *pgx.Conn                              { return nil }
