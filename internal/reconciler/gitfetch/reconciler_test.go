package gitfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lknappich/syncctl/internal/localcmd"
)

func TestRepoDiskPathLegacy(t *testing.T) {
	got, err := repoDiskPath("default", false, "group/subgroup/project", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "group/subgroup/project.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The sweep reads routes.path straight out of the replicated database.
// filepath.Join cleans ".." rather than rejecting it, so an unvalidated
// route path resolves to a location outside the repositories root.
func TestRepoDiskPathRejectsTraversal(t *testing.T) {
	for _, bad := range []string{
		"../../etc/cron.d/x",
		"a/../../../root/.ssh/authorized_keys",
		"/absolute/path",
		"group/../../escape",
		"group/proj;rm -rf /",
		"group\\proj",
	} {
		t.Run(bad, func(t *testing.T) {
			if got, err := repoDiskPath("default", false, bad, 42); err == nil {
				t.Errorf("route path %q accepted, resolved to %q", bad, got)
			}
		})
	}
}

func TestContainedJoin(t *testing.T) {
	root := "/var/opt/gitlab/git-data/repositories"
	tests := []struct {
		rel     string
		wantErr bool
	}{
		{rel: "group/proj.git"},
		{rel: "@hashed/6b/86/abc.git"},
		{rel: "../../etc/cron.d/x.git", wantErr: true},
		{rel: "a/../../../root/.ssh/authorized_keys.git", wantErr: true},
		{rel: "..", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.rel, func(t *testing.T) {
			got, err := containedJoin(root, tc.rel)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected containment error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(got, root+"/") {
				t.Errorf("joined path %q escaped %q", got, root)
			}
		})
	}
}

func TestRepoDiskPathLegacyEmptyRoute(t *testing.T) {
	got, err := repoDiskPath("default", false, "", 42)
	if err == nil {
		t.Errorf("expected an error for an empty route, got %q", got)
	}
	if !errors.Is(err, errNoRoutePath) {
		t.Errorf("err = %v, want errNoRoutePath", err)
	}
}

func TestRepoDiskPathHashed(t *testing.T) {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d", 42)
	full := hex.EncodeToString(h.Sum(nil))
	got, err := repoDiskPath("default", true, "ignored", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("@hashed/%s/%s/%s.git", full[0:2], full[2:4], full)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Pins the layout against the value documented in GitLab's public
// "Repository storage paths" page: SHA-256 of the project ID, split into
// two two-character directory levels under @hashed.
func TestRepoDiskPathHashedGolden(t *testing.T) {
	got, err := repoDiskPath("default", true, "ignored", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	out   []byte
	err   error
	calls []gitCall
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
	pool := &mockPool{}
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
	pool := &mockPool{projects: []mockProject{{id: 1, route: "group/proj"}}}
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
	pool := &mockPool{projects: []mockProject{{id: 1, route: "group/proj"}}}
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
	pool := &mockPool{projects: []mockProject{{id: 1, route: "group/proj"}}, storageVersion: 1}
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

// The CE schema has no projects.hashed_storage; selecting it fails every
// sweep and every webhook fetch with "column does not exist".
func TestQueriesSelectStorageVersion(t *testing.T) {
	pool := &mockPool{projects: []mockProject{{id: 1, route: "group/proj"}}}
	r := (&Reconciler{}).WithPool(pool)
	_, _ = r.listProjects(context.Background())
	_, _ = r.resolveProject(context.Background(), "group/proj")

	if len(pool.queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(pool.queries))
	}
	for _, q := range pool.queries {
		if strings.Contains(q, "hashed_storage") {
			t.Errorf("query selects projects.hashed_storage, which is not in the CE schema:\n%s", q)
		}
		if !strings.Contains(q, "p.storage_version") {
			t.Errorf("query does not select projects.storage_version:\n%s", q)
		}
	}
}

func TestStorageVersionSelectsLayout(t *testing.T) {
	const route = "group/proj"
	const hashed = "@hashed/6b/86/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b.git"
	for _, tc := range []struct {
		name           string
		storageVersion any
		want           string
	}{
		{name: "null", storageVersion: nil, want: route + ".git"},
		{name: "0", storageVersion: 0, want: route + ".git"},
		{name: "1", storageVersion: 1, want: hashed},
		{name: "2", storageVersion: 2, want: hashed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &mockPool{projects: []mockProject{{id: 1, route: route}}, storageVersion: tc.storageVersion}
			r := (&Reconciler{}).WithPool(pool)

			projects, err := r.listProjects(context.Background())
			if err != nil {
				t.Fatalf("listProjects: %v", err)
			}
			if len(projects) != 1 || projects[0].RepoPath != tc.want {
				t.Errorf("sweep resolved %+v, want RepoPath %q", projects, tc.want)
			}

			p, err := r.resolveProject(context.Background(), route)
			if err != nil {
				t.Fatalf("resolveProject: %v", err)
			}
			if p.RepoPath != tc.want {
				t.Errorf("webhook resolved %q, want %q", p.RepoPath, tc.want)
			}
		})
	}
}

// projects.id is bigint. The expected digest was computed independently
// with `printf 3000000000 | sha256sum`.
func TestProjectIDAboveInt32Range(t *testing.T) {
	const want = "/r/@hashed/d5/97/d59718945ff6986c9082bb8adbee04c7a748fa6ed32528c318c6b03da3ce0f43.git"
	pool := &mockPool{projects: []mockProject{{id: 3_000_000_000, route: "group/proj"}}, storageVersion: 1}

	t.Run("sweep", func(t *testing.T) {
		runner := &mockGitRunner{}
		r := (&Reconciler{primarySSHHost: "p", reposPath: "/r", maxParallel: 1}).WithPool(pool).WithRunner(runner)
		if res := r.Reconcile(context.Background()); !res.OK {
			t.Fatalf("Reconcile: %s", res.Detail)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("expected 1 git call, got %d", len(runner.calls))
		}
		if got := runner.calls[0].args[1]; got != want {
			t.Errorf("fetched %q, want %q", got, want)
		}
	})

	t.Run("webhook", func(t *testing.T) {
		runner := &mockGitRunner{}
		r := (&Reconciler{primarySSHHost: "p", reposPath: "/r"}).WithPool(pool).WithRunner(runner)
		if err := r.FetchProject(context.Background(), "group/proj"); err != nil {
			t.Fatalf("FetchProject: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("expected 1 git call, got %d", len(runner.calls))
		}
		if got := runner.calls[0].args[1]; got != want {
			t.Errorf("fetched %q, want %q", got, want)
		}
	})
}

func TestFetchProjectUnknownProject(t *testing.T) {
	pool := &mockPool{}
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

type mockProject struct {
	id    int64
	route string
}

type mockPool struct {
	projects []mockProject
	// storageVersion is projects.storage_version for every row; nil is NULL.
	storageVersion any
	err            error
	queries        []string
}

func (p *mockPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	p.queries = append(p.queries, sql)
	if p.err != nil {
		return nil, p.err
	}
	return &mockRows{projects: p.projects, storageVersion: p.storageVersion, typeMap: pgtype.NewMap()}, nil
}

type mockRows struct {
	projects       []mockProject
	storageVersion any
	typeMap        *pgtype.Map
	idx            int
}

var _ pgx.Rows = (*mockRows)(nil)

func (m *mockRows) Next() bool {
	if m.idx >= len(m.projects) {
		return false
	}
	m.idx++
	return true
}

type mockColumn struct {
	oid   uint32
	value any
}

// Scan encodes each value as its CE column type and decodes it with pgx's
// own codecs. Assigning Go values straight into the destinations let a
// bigint read into an int32 and a smallint read into a bool pass here
// while failing on every real database.
func (m *mockRows) Scan(dest ...any) error {
	p := m.projects[m.idx-1]
	id := mockColumn{pgtype.Int8OID, p.id}
	storage := mockColumn{pgtype.VarcharOID, "default"}
	version := mockColumn{pgtype.Int2OID, m.storageVersion}
	// resolveProject scans (id, repository_storage, storage_version);
	// listProjects scans those plus namespace id and route path.
	cols := []mockColumn{id, storage, version}
	if len(dest) == 5 {
		cols = []mockColumn{id, storage, {pgtype.Int8OID, nil}, version, {pgtype.TextOID, p.route}}
	}
	if len(dest) != len(cols) {
		return fmt.Errorf("mock scan: got %d destinations, want %d", len(dest), len(cols))
	}
	for i, c := range cols {
		src, err := m.typeMap.Encode(c.oid, pgtype.BinaryFormatCode, c.value, nil)
		if err != nil {
			return fmt.Errorf("mock encode column %d: %w", i, err)
		}
		if err := m.typeMap.Scan(c.oid, pgtype.BinaryFormatCode, src, dest[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func (m *mockRows) Close()                                       {}
func (m *mockRows) Err() error                                   { return nil }
func (m *mockRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (m *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (m *mockRows) Values() ([]any, error)                       { return nil, nil }
func (m *mockRows) RawValues() [][]byte                          { return nil }
func (m *mockRows) Conn() *pgx.Conn                              { return nil }
func (m *mockRows) TypeMap() *pgtype.Map                         { return pgtype.NewMap() }

// End-to-end through the sweep: a project whose route path escapes the
// repositories root must be skipped without running git, and must not
// stop the sweep from replicating the well-formed projects beside it.
func TestReconcileSkipsTraversalRouteWithoutRunningGit(t *testing.T) {
	pool := &mockPool{projects: []mockProject{
		{id: 1, route: "group/good"},
		{id: 2, route: "../../etc/cron.d/evil"},
	}}
	runner := &mockGitRunner{out: []byte("")}
	r := (&Reconciler{primarySSHHost: "p", reposPath: "/var/opt/gitlab/git-data/repositories", maxParallel: 1}).
		WithPool(pool).WithRunner(runner)
	r.Reconcile(context.Background())

	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "/etc/cron.d") {
			t.Errorf("git ran against a path outside the repositories root: %s", joined)
		}
	}
	if len(runner.calls) != 1 {
		t.Errorf("expected only the well-formed project to be fetched, got %d calls", len(runner.calls))
	}
}
