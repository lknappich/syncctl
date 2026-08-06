package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/sshexec"
)

// --- Mocks ---

type mockRunner struct {
	out        []byte
	err        error
	calls      []mockCall
	perHost    map[string][]byte
	perHostErr map[string]error
}

type mockCall struct {
	host string
	cmd  string
}

func (m *mockRunner) CombinedOutput(ctx context.Context, host, cmd string) ([]byte, error) {
	m.calls = append(m.calls, mockCall{host, cmd})
	if m.perHost != nil {
		if out, ok := m.perHost[host]; ok {
			var err error
			if m.perHostErr != nil {
				err = m.perHostErr[host]
			}
			return out, err
		}
	}
	return m.out, m.err
}

func (m *mockRunner) SSHString() string { return "mock-ssh" }

type mockRow struct {
	scanFn func(dest ...any) error
}

func (r *mockRow) Scan(dest ...any) error { return r.scanFn(dest...) }

type mockPool struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) Row
	closed     bool
}

func (p *mockPool) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return p.queryRowFn(ctx, sql, args...)
}

func (p *mockPool) Close() { p.closed = true }

func fakePoolFactory(pool Pool) PoolFactory {
	return func(ctx context.Context, dsn string) (Pool, error) {
		return pool, nil
	}
}

func errPoolFactory(err error) PoolFactory {
	return func(ctx context.Context, dsn string) (Pool, error) {
		return nil, err
	}
}

// --- Existing tests (updated to new signatures) ---

func TestResultSummary(t *testing.T) {
	r := &Result{
		Checks: []Check{
			{Name: "a", Status: "PASS"},
			{Name: "b", Status: "FAIL"},
			{Name: "c", Status: "WARN"},
			{Name: "d", Status: "PASS"},
		},
	}
	for _, c := range r.Checks {
		switch c.Status {
		case "PASS":
			r.Pass++
		case "FAIL":
			r.Fail++
		case "WARN":
			r.Warn++
		}
	}
	if r.Pass != 2 || r.Fail != 1 || r.Warn != 1 {
		t.Errorf("summary mismatch: pass=%d fail=%d warn=%d", r.Pass, r.Fail, r.Warn)
	}
}

func TestS3BucketCheckConfigMissing(t *testing.T) {
	s3 := &config.S3Config{}
	c := s3BucketCheck(context.TODO(), "primary", s3)
	if !strings.Contains(c.Detail, "primary_bucket") {
		t.Errorf("expected missing primary_bucket detail, got: %s", c.Detail)
	}
}

func TestS3BucketCheckConfigOK(t *testing.T) {
	s3 := &config.S3Config{
		PrimaryBucket: "bucket-p",
		ReplicaBucket: "bucket-r",
		AccessKey:     "AK",
		SecretKey:     "SK",
	}
	c := s3BucketCheck(context.TODO(), "primary", s3)
	if c.Status != "PASS" {
		t.Errorf("expected PASS, got %s: %s", c.Status, c.Detail)
	}
}

func TestS3BucketCheckMissingKeys(t *testing.T) {
	s3 := &config.S3Config{
		PrimaryBucket: "bucket-p",
		ReplicaBucket: "bucket-r",
		AccessKey:     "",
		SecretKey:     "",
	}
	c := s3BucketCheck(context.TODO(), "primary", s3)
	if !strings.Contains(c.Detail, "access_key/secret_key") {
		t.Errorf("expected missing keys detail, got: %s", c.Detail)
	}
}

func TestPrintOutput(t *testing.T) {
	r := &Result{
		Checks: []Check{
			{Name: "test", Category: "cat", Status: "PASS", Detail: "detail"},
		},
		Pass: 1,
	}
	r.Print()
}

func TestSSHCheckEmptyHost(t *testing.T) {
	c := sshCheck(context.Background(), "primary", "", &mockRunner{})
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestSSHCheckPass(t *testing.T) {
	r := &mockRunner{out: []byte("ok")}
	c := sshCheck(context.Background(), "primary", "p:22", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
	if c.Detail != "p:22" {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestSSHCheckFail(t *testing.T) {
	r := &mockRunner{err: errors.New("conn refused")}
	c := sshCheck(context.Background(), "primary", "p:22", r)
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGControlCheckEmptyHost(t *testing.T) {
	c := pgControlCheck(context.Background(), "primary", config.PostgresConfig{}, fakePoolFactory(nil))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGControlCheckPoolError(t *testing.T) {
	c := pgControlCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h", Port: 5432, DB: "d", User: "u"},
		errPoolFactory(errors.New("dial error")))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "dial error") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGControlCheckQueryError(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("query failed") }}
	}}
	c := pgControlCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "query version") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGControlCheckPass(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			s := dest[0].(*string)
			*s = "PostgreSQL 15.4"
			return nil
		}}
	}}
	c := pgControlCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestPGControlCheckVersionShort(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			s := dest[0].(*string)
			*s = "PostgreSQL 15.4 (Ubuntu 15.4-1.pgdg) on x86_64"
			return nil
		}}
	}}
	c := pgControlCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS", c.Status)
	}
	if strings.Contains(c.Detail, "(") {
		t.Errorf("Detail should be short version, got %q", c.Detail)
	}
}

func TestPGReplicationRoleCheckConnectError(t *testing.T) {
	c := pgReplicationRoleCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h", ReplicationUser: "u"},
		errPoolFactory(errors.New("dial error")))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGReplicationRoleCheckNotFound(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("no rows") }}
	}}
	c := pgReplicationRoleCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h", ReplicationUser: "u"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "not found") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGReplicationRoleCheckNoPrivilege(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = false
			return nil
		}}
	}}
	c := pgReplicationRoleCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h", ReplicationUser: "u"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "lacks REPLICATION") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGReplicationRoleCheckPass(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	c := pgReplicationRoleCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h", ReplicationUser: "u"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestPGWalLevelCheckConnectError(t *testing.T) {
	c := pgWalLevelCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"},
		errPoolFactory(errors.New("dial error")))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGWalLevelCheckQueryError(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("no rows") }}
	}}
	c := pgWalLevelCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGWalLevelCheckBadValue(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = "minimal"
			return nil
		}}
	}}
	c := pgWalLevelCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "minimal") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGWalLevelCheckPass(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = "replica"
			return nil
		}}
	}}
	c := pgWalLevelCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestPGWalLevelCheckLogical(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = "logical"
			return nil
		}}
	}}
	c := pgWalLevelCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS", c.Status)
	}
}

func TestPGWalSendersCheckZero(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = "0"
			return nil
		}}
	}}
	c := pgWalSendersCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGWalSendersCheckPass(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = "10"
			return nil
		}}
	}}
	c := pgWalSendersCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestPGWalSendersCheckConnectError(t *testing.T) {
	c := pgWalSendersCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, errPoolFactory(errors.New("nope")))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGWalSendersCheckQueryError(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("no rows") }}
	}}
	c := pgWalSendersCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestPGInRecoveryCheckConnectError(t *testing.T) {
	c := pgInRecoveryCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, errPoolFactory(errors.New("nope")))
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestPGInRecoveryCheckQueryError(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error { return errors.New("no rows") }}
	}}
	c := pgInRecoveryCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestPGInRecoveryCheckNotInRecovery(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = false
			return nil
		}}
	}}
	c := pgInRecoveryCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
	if !strings.Contains(c.Detail, "NOT in recovery") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPGInRecoveryCheckPass(t *testing.T) {
	pool := &mockPool{queryRowFn: func(ctx context.Context, sql string, args ...any) Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*(dest[0].(*bool)) = true
			return nil
		}}
	}}
	c := pgInRecoveryCheck(context.Background(), "primary",
		config.PostgresConfig{Host: "h"}, fakePoolFactory(pool))
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestDBKeyPresentCheckEmptyHost(t *testing.T) {
	c := dbKeyPresentCheck(context.Background(), "primary", "", &mockRunner{})
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestDBKeyPresentCheckSSHErr(t *testing.T) {
	r := &mockRunner{err: errors.New("ssh failed")}
	c := dbKeyPresentCheck(context.Background(), "primary", "h:22", r)
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestDBKeyPresentCheckZeroCount(t *testing.T) {
	r := &mockRunner{out: []byte("0\n")}
	c := dbKeyPresentCheck(context.Background(), "primary", "h:22", r)
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
	if !strings.Contains(c.Detail, "not found") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestDBKeyPresentCheckPass(t *testing.T) {
	r := &mockRunner{out: []byte("1\n")}
	c := dbKeyPresentCheck(context.Background(), "primary", "h:22", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestDBKeyPresentCheckMultilineOutput(t *testing.T) {
	// Output may have multiple lines (sudo errors followed by count).
	r := &mockRunner{out: []byte("sudo: error\ngrep: error\n1\n")}
	c := dbKeyPresentCheck(context.Background(), "primary", "h:22", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestDBKeyParityCheckPass(t *testing.T) {
	runner := &mockRunner{perHost: map[string][]byte{
		"p:22": []byte("gitlab_rails['db_key_base'] = 'key"),
		"s:22": []byte("gitlab_rails['db_key_base'] = 'key"),
	}}
	c := dbKeyParityCheck(context.Background(), "p:22", "s:22", "secondary", runner)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestDBKeyParityCheckFail(t *testing.T) {
	runner := &mockRunner{perHost: map[string][]byte{
		"p:22": []byte("gitlab_rails['db_key_base'] = 'keyA"),
		"s:22": []byte("gitlab_rails['db_key_base'] = 'keyB"),
	}}
	c := dbKeyParityCheck(context.Background(), "p:22", "s:22", "secondary", runner)
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestRemoteBinaryCheckEmptyHost(t *testing.T) {
	c := remoteBinaryCheck(context.Background(), "primary", "rsync", "", "rsync --version", &mockRunner{})
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
	if c.Name != "rsync:primary" {
		t.Errorf("Name = %q", c.Name)
	}
}

func TestRemoteBinaryCheckNotFound(t *testing.T) {
	r := &mockRunner{err: errors.New("command not found")}
	c := remoteBinaryCheck(context.Background(), "primary", "rsync", "h:22", "rsync --version", r)
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestRemoteBinaryCheckPass(t *testing.T) {
	r := &mockRunner{out: []byte("rsync  version 3.2.7\nprotocol version 31\n")}
	c := remoteBinaryCheck(context.Background(), "primary", "rsync", "h:22", "rsync --version", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
	if c.Detail != "rsync  version 3.2.7" {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPathExistsCheckEmptyHost(t *testing.T) {
	c := pathExistsCheck(context.Background(), "primary", "", "/some/path", "repos", &mockRunner{})
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestPathExistsCheckMissing(t *testing.T) {
	r := &mockRunner{err: errors.New("exit 1")}
	c := pathExistsCheck(context.Background(), "primary", "h:22", "/var/repos", "repos", r)
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
	if !strings.Contains(c.Detail, "does not exist") {
		t.Errorf("Detail = %q", c.Detail)
	}
}

func TestPathExistsCheckPass(t *testing.T) {
	r := &mockRunner{out: []byte("")}
	c := pathExistsCheck(context.Background(), "primary", "h:22", "/var/repos", "repos", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestS3BucketCheckEmptyPrimaryBucket(t *testing.T) {
	c := s3BucketCheck(context.Background(), "primary", &config.S3Config{PrimaryBucket: ""})
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestS3BucketCheckEmptyReplicaBucket(t *testing.T) {
	c := s3BucketCheck(context.Background(), "primary", &config.S3Config{PrimaryBucket: "p", ReplicaBucket: ""})
	if c.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", c.Status)
	}
}

func TestRsyncCheckEmpty(t *testing.T) {
	c := rsyncCheck(context.Background(), "primary", "", &mockRunner{})
	if c.Status != "WARN" {
		t.Errorf("Status = %q, want WARN", c.Status)
	}
}

func TestGitCheckPass(t *testing.T) {
	r := &mockRunner{out: []byte("git version 2.43.0\n")}
	c := gitCheck(context.Background(), "primary", "h:22", r)
	if c.Status != "PASS" {
		t.Errorf("Status = %q, want PASS: %s", c.Status, c.Detail)
	}
}

func TestResultAddAndSummary(t *testing.T) {
	r := &Result{}
	r.add(Check{Name: "a", Status: "PASS"})
	r.add(Check{Name: "b", Status: "FAIL"})
	r.add(Check{Name: "c", Status: "WARN"})
	r.add(Check{Name: "d", Status: "PASS"})
	if len(r.Checks) != 4 {
		t.Errorf("len(Checks) = %d, want 4", len(r.Checks))
	}
}

func TestRunWithMinimalConfig(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{
			{Name: "s", SSHHost: "s:22"},
		},
	}
	runner := &mockRunner{out: []byte("ok")}
	// Pool factory that always returns a query-fail pool — exercises the
	// per-secondary PG checks without a real database.
	pf := errPoolFactory(errors.New("no db"))
	result := RunWith(context.Background(), cfg, runner, pf)
	if result == nil {
		t.Fatal("RunWith returned nil")
	}
	if len(result.Checks) == 0 {
		t.Error("expected at least one check")
	}
}

// silence unused imports if sshexec ever gets removed
var _ sshexec.Runner = (*mockRunner)(nil)

func TestPathExistsCheckQuotesThePath(t *testing.T) {
	runner := &mockRunner{out: []byte("")}
	pathExistsCheck(context.Background(), "secondary", "h", "/mnt/git data; rm -rf /", "repos_path", runner)
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	got := runner.calls[0].cmd
	want := `test -d '/mnt/git data; rm -rf /'`
	if got != want {
		t.Errorf("remote command = %q, want %q", got, want)
	}
}
func TestDBKeyPresentCheckReadsBothLocations(t *testing.T) {
	runner := &mockRunner{out: []byte("1")}
	dbKeyPresentCheck(context.Background(), "primary", "h", runner)
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	cmd := runner.calls[0].cmd
	for _, want := range []string{
		"/var/opt/gitlab/gitlab-rails/etc/secrets.yml",
		"/etc/gitlab/gitlab.rb",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("remote command must inspect %s, got: %s", want, cmd)
		}
	}
}

// A key found only in gitlab.rb must PASS, not FAIL.
func TestDBKeyPresentCheckPassesOnGitlabRbOnly(t *testing.T) {
	runner := &mockRunner{out: []byte("1")}
	got := dbKeyPresentCheck(context.Background(), "primary", "h", runner)
	if got.Status != "PASS" {
		t.Errorf("Status = %q, want PASS (detail: %s)", got.Status, got.Detail)
	}
}

func TestDBKeyPresentCheckFailsWhenAbsent(t *testing.T) {
	runner := &mockRunner{out: []byte("0")}
	got := dbKeyPresentCheck(context.Background(), "primary", "h", runner)
	if got.Status != "FAIL" {
		t.Errorf("Status = %q, want FAIL", got.Status)
	}
}
