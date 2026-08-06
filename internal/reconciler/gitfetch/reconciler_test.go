package gitfetch

import (
	"context"
	"crypto/sha256"
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
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d", 42)
	full := hex.EncodeToString(h.Sum(nil))
	got := repoDiskPath("default", true, "ignored", 42)
	want := fmt.Sprintf("@hashed/%s/%s/%s.git", full[0:2], full[2:4], full)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Pins the layout against the value documented in GitLab's public
// "Repository storage paths" page: SHA-256 of the project ID, split into
// two two-character directory levels under @hashed.
func TestRepoDiskPathHashedGolden(t *testing.T) {
	got := repoDiskPath("default", true, "ignored", 1)
	want := "@hashed/6b/86/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHashedStorageHashDeterministic(t *testing.T) {
	h1 := hashedStorageHash(1)
	h2 := hashedStorageHash(1)
	if h1 != h2 {
		t.Errorf("hashedStorageHash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex, got %d", len(h1))
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
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", maxParallel: 1}).WithPool(pool)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK with no projects, got: %s", res.Detail)
	}
}

func TestReconcilePoolError(t *testing.T) {
	pool := &mockPool{err: errors.New("db down")}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", maxParallel: 1}).WithPool(pool)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on pool error")
	}
}

func TestReconcileFetchOneSuccess(t *testing.T) {
	pool := &mockPool{rows: []projectRow{{ID: 1, RepoPath: "group/proj.git"}}}
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", maxParallel: 1}).WithPool(pool).WithRunner(runner)
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
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", maxParallel: 1}).WithPool(pool).WithRunner(runner)
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
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r"}).WithRunner(runner)
	err := r.FetchProject(context.TODO(), "group/proj")
	if err != nil {
		t.Fatalf("FetchProject: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 git call, got %d", len(runner.calls))
	}
}

func TestFetchProjectBuildsSSHURLWithPort(t *testing.T) {
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{primarySSHHost: "git@p.example.com:2222", reposPath: "/r"}).WithRunner(runner)
	if err := r.FetchProject(context.TODO(), "group/proj"); err != nil {
		t.Fatalf("FetchProject: %v", err)
	}
	joined := strings.Join(runner.calls[0].args, " ")
	want := "ssh://git@p.example.com:2222/var/opt/gitlab/git-data/repositories/group/proj.git"
	if !strings.Contains(joined, want) {
		t.Errorf("args = %q, want remote URL %q", joined, want)
	}
}

func TestFetchProjectRejectsMalformedSSHHost(t *testing.T) {
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{primarySSHHost: "p:not-a-port", reposPath: "/r"}).WithRunner(runner)
	if err := r.FetchProject(context.TODO(), "group/proj"); err == nil {
		t.Error("expected an error for a malformed ssh_host")
	}
	if len(runner.calls) != 0 {
		t.Errorf("git should not run with a malformed ssh_host, got %d calls", len(runner.calls))
	}
}

func TestFetchProjectDryRun(t *testing.T) {
	runner := &mockGitRunner{}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", dryRun: true}).WithRunner(runner)
	err := r.FetchProject(context.TODO(), "group/proj")
	if err != nil {
		t.Fatalf("dry-run FetchProject should succeed: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("dry-run should not invoke git, got %d calls", len(runner.calls))
	}
}

// With hashed storage on, the webhook path must resolve the project ID
// from the DB and fetch into @hashed/..., not into the legacy route path.
func TestFetchProjectUsesHashedLayout(t *testing.T) {
	pool := &mockPool{rows: []projectRow{{ID: 1}}, hashed: true}
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r"}).WithPool(pool).WithRunner(runner)
	if err := r.FetchProject(context.TODO(), "group/proj"); err != nil {
		t.Fatalf("FetchProject: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 git call, got %d", len(runner.calls))
	}
	want := "/r/@hashed/6b/86/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b.git"
	if got := runner.calls[0].args[1]; got != want {
		t.Errorf("fetched %q, want %q", got, want)
	}
}

func TestFetchProjectUnknownProject(t *testing.T) {
	pool := &mockPool{rows: []projectRow{}}
	runner := &mockGitRunner{}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r"}).WithPool(pool).WithRunner(runner)
	err := r.FetchProject(context.TODO(), "group/proj")
	if err == nil {
		t.Fatal("expected error for project missing from the DB")
	}
	if len(runner.calls) != 0 {
		t.Errorf("should not fetch an unresolved project, got %d calls", len(runner.calls))
	}
}

func TestFetchProjectLookupError(t *testing.T) {
	pool := &mockPool{err: errors.New("db down")}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/r"}).WithPool(pool).WithRunner(&mockGitRunner{})
	if err := r.FetchProject(context.TODO(), "group/proj"); err == nil {
		t.Fatal("expected error when the lookup query fails")
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
	rows   []projectRow
	hashed bool
	err    error
}

func (p *mockPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &mockRows{rows: p.rows, hashed: p.hashed}, nil
}

type mockRows struct {
	rows   []projectRow
	hashed bool
	idx    int
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
	// resolveProject scans (id, repository_storage, hashed_storage);
	// listProjects scans those plus namespace id and route path.
	if len(dest) == 3 {
		*(dest[0].(*int32)) = r.ID
		*(dest[1].(*string)) = "default"
		*(dest[2].(*bool)) = m.hashed
		return nil
	}
	*(dest[0].(*int32)) = r.ID
	*(dest[1].(*string)) = "default"
	// namespaceID as sql.NullInt64 valid=false
	dest[2].(*sql.NullInt64).Valid = false
	// hashed bool
	*(dest[3].(*bool)) = m.hashed
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
