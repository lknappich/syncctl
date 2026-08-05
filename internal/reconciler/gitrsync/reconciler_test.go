package gitrsync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/localcmd"
	"github.com/lknappich/syncctl/internal/sshexec"
)

// mockRunner records calls and returns canned output.
type mockRunner struct {
	out    []byte
	err    error
	calls  []mockCall
	perCmd map[string][]byte
	perErr map[string]error
}

type mockCall struct {
	name string
	args []string
	env  []string
}

func (m *mockRunner) Run(ctx context.Context, name string, args, env []string) ([]byte, error) {
	m.calls = append(m.calls, mockCall{name, args, env})
	if m.perErr != nil {
		if err, ok := m.perErr[name]; ok {
			if m.perCmd != nil {
				if out, ok := m.perCmd[name]; ok {
					return out, err
				}
			}
			return m.out, err
		}
	}
	if m.perCmd != nil {
		if out, ok := m.perCmd[name]; ok {
			return out, m.err
		}
	}
	return m.out, m.err
}

var _ localcmd.Runner = (*mockRunner)(nil)

func TestNewConstructorFields(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost: "primary.example.com:22",
		Git:     config.GitStorage{ReposPath: "/var/opt/gitlab/git-data/repositories"},
	}
	secondary := &config.SiteConfig{
		Git: config.GitStorage{ReposPath: "/var/opt/gitlab/git-data/repositories"},
	}
	sshCfg := sshexec.Config{KnownHostsFile: "/etc/known_hosts", StrictHostKeyChecking: "yes"}
	r := New(primary, secondary, true, sshCfg)
	if r.sshHost != "primary.example.com:22" {
		t.Errorf("sshHost = %q", r.sshHost)
	}
	if r.srcPath != "/var/opt/gitlab/git-data/repositories" {
		t.Errorf("srcPath = %q", r.srcPath)
	}
	if r.dstPath != "/var/opt/gitlab/git-data/repositories" {
		t.Errorf("dstPath = %q", r.dstPath)
	}
	if !r.dryRun {
		t.Error("dryRun should be true")
	}
	if r.sshCfg.KnownHostsFile != "/etc/known_hosts" {
		t.Errorf("sshCfg.KnownHostsFile = %q", r.sshCfg.KnownHostsFile)
	}
}

func TestNewEmptyPaths(t *testing.T) {
	primary := &config.SiteConfig{SSHHost: "host:22"}
	secondary := &config.SiteConfig{}
	r := New(primary, secondary, false, sshexec.Config{})
	if r.sshHost != "host:22" {
		t.Errorf("sshHost = %q", r.sshHost)
	}
	if r.srcPath != "" {
		t.Errorf("srcPath = %q, want empty", r.srcPath)
	}
	if r.dstPath != "" {
		t.Errorf("dstPath = %q, want empty", r.dstPath)
	}
	if r.dryRun {
		t.Error("dryRun should be false")
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "git_rsync" {
		t.Errorf("Name() = %q, want git_rsync", r.Name())
	}
}

func TestErrResultOK(t *testing.T) {
	got := errResult(nil)
	if got != "ok" {
		t.Errorf("errResult(nil) = %q, want ok", got)
	}
}

func TestErrResultError(t *testing.T) {
	got := errResult(errors.New("oops"))
	if got != "error" {
		t.Errorf("errResult(err) = %q, want error", got)
	}
}

func TestReconcileSuccess(t *testing.T) {
	r := New(&config.SiteConfig{SSHHost: "p:22", Git: config.GitStorage{ReposPath: "/src"}},
		&config.SiteConfig{Git: config.GitStorage{ReposPath: "/dst"}}, false, sshexec.Config{})
	r = r.WithRunner(&mockRunner{out: []byte("")})
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
}

func TestReconcileDryRun(t *testing.T) {
	r := New(&config.SiteConfig{SSHHost: "p:22", Git: config.GitStorage{ReposPath: "/src"}},
		&config.SiteConfig{Git: config.GitStorage{ReposPath: "/dst"}}, true, sshexec.Config{})
	r = r.WithRunner(&mockRunner{out: []byte("")})
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
	if !strings.Contains(res.Detail, "dry-run") {
		t.Errorf("Detail should mention dry-run: %s", res.Detail)
	}
}

func TestReconcileRsyncError(t *testing.T) {
	r := New(&config.SiteConfig{SSHHost: "p:22", Git: config.GitStorage{ReposPath: "/src"}},
		&config.SiteConfig{Git: config.GitStorage{ReposPath: "/dst"}}, false, sshexec.Config{})
	r = r.WithRunner(&mockRunner{err: errors.New("rsync failed"), out: []byte("permission denied")})
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on rsync error")
	}
	if !strings.Contains(res.Detail, "permission denied") {
		t.Errorf("Detail = %q", res.Detail)
	}
}

func TestReconcileBuildsCorrectArgs(t *testing.T) {
	runner := &mockRunner{out: []byte("")}
	r := New(&config.SiteConfig{SSHHost: "p:22", Git: config.GitStorage{ReposPath: "/src"}},
		&config.SiteConfig{Git: config.GitStorage{ReposPath: "/dst"}}, false, sshexec.Config{})
	r = r.WithRunner(runner)
	_ = r.Reconcile(context.Background())
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "rsync" {
		t.Errorf("name = %q, want rsync", call.name)
	}
	joined := strings.Join(call.args, " ")
	if !strings.Contains(joined, "--delete") {
		t.Errorf("args should include --delete: %s", joined)
	}
	if !strings.Contains(joined, "p:22:/src/") {
		t.Errorf("args should include source: %s", joined)
	}
	if !strings.Contains(joined, "/dst/") {
		t.Errorf("args should include dest: %s", joined)
	}
}
