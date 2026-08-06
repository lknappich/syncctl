package fsstorage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/sshexec"
)

func TestNewCollectsFSPaths(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost: "primary:22",
		ObjectStore: config.ObjectStoreConfig{
			Backend: "fs",
			FSPaths: []string{"/var/opt/gitlab/uploads", "/var/opt/gitlab/artifacts"},
		},
		Registry: &config.RegistryConfig{
			Mode:   "fs",
			FSPath: "/var/opt/gitlab/registry",
		},
	}
	secondary := &config.SiteConfig{}

	r := New(primary, secondary, "", true, sshexec.Default)
	if len(r.pathPairs) != 3 {
		t.Errorf("expected 3 path pairs, got %d", len(r.pathPairs))
	}
}

func TestNewNoRegistry(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost: "primary:22",
		ObjectStore: config.ObjectStoreConfig{
			Backend: "fs",
			FSPaths: []string{"/uploads"},
		},
	}
	secondary := &config.SiteConfig{}

	r := New(primary, secondary, "", true, sshexec.Default)
	if len(r.pathPairs) != 1 {
		t.Errorf("expected 1 path pair, got %d", len(r.pathPairs))
	}
}

func TestNewNoPaths(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost: "primary:22",
		ObjectStore: config.ObjectStoreConfig{
			Backend: "fs",
		},
	}
	secondary := &config.SiteConfig{}

	r := New(primary, secondary, "", true, sshexec.Default)
	if len(r.pathPairs) != 0 {
		t.Errorf("expected 0 path pairs, got %d", len(r.pathPairs))
	}
}

func TestReconcileNoPaths(t *testing.T) {
	r := New(&config.SiteConfig{SSHHost: "p:22"}, &config.SiteConfig{}, "", false, sshexec.Default)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK with no paths, got: %s", res.Detail)
	}
	if res.Detail != "no fs paths configured" {
		t.Errorf("Detail = %q", res.Detail)
	}
}

func TestReconcileSuccess(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:     "p:22",
		ObjectStore: config.ObjectStoreConfig{FSPaths: []string{"/uploads", "/artifacts"}},
	}
	r := New(primary, &config.SiteConfig{}, "", false, sshexec.Default)
	r = r.WithRunner(&fsMockRunner{out: []byte("")})
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
	if res.Repaired != 2 {
		t.Errorf("Repaired = %d, want 2", res.Repaired)
	}
}

func TestReconcilePartialFailure(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:     "p:22",
		ObjectStore: config.ObjectStoreConfig{FSPaths: []string{"/uploads", "/artifacts"}},
	}
	r := New(primary, &config.SiteConfig{}, "", false, sshexec.Default)
	r = r.WithRunner(&fsMockRunner{err: errors.New("rsync failed"), out: []byte("oops")})
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on failure")
	}
	if res.Remaining != 2 {
		t.Errorf("Remaining = %d, want 2", res.Remaining)
	}
}

func TestReconcileNoSuchFileSkipped(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:     "p:22",
		ObjectStore: config.ObjectStoreConfig{FSPaths: []string{"/uploads"}},
	}
	r := New(primary, &config.SiteConfig{}, "", false, sshexec.Default)
	r = r.WithRunner(&fsMockRunner{err: errors.New("exit 1"), out: []byte("rsync: change_dir \"/uploads\" failed: No such file or directory (2)")})
	res := r.Reconcile(context.Background())
	// A missing path is not fatal — some installs legitimately lack an
	// lfs-objects directory — but it is not work done either. Counting
	// it as repaired made a misconfigured fs_paths report "rsynced 4
	// paths" while replicating three.
	if !res.OK {
		t.Errorf("a missing path should not fail the sweep, got: %s", res.Detail)
	}
	if res.Repaired != 0 {
		t.Errorf("Repaired = %d, want 0 — a skipped path was not replicated", res.Repaired)
	}
	if !strings.Contains(res.Detail, "do not exist on the primary") {
		t.Errorf("Detail should surface the skip, got: %s", res.Detail)
	}
}

func TestReconcileCancelled(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:     "p:22",
		ObjectStore: config.ObjectStoreConfig{FSPaths: []string{"/a", "/b"}},
	}
	r := New(primary, &config.SiteConfig{}, "", false, sshexec.Default)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := r.Reconcile(ctx)
	if res.OK {
		t.Error("expected not-OK on cancelled context")
	}
	if !strings.Contains(res.Detail, "cancelled") {
		t.Errorf("Detail = %q", res.Detail)
	}
}

func TestNewRegistrySecondaryPath(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:  "p:22",
		Registry: &config.RegistryConfig{Mode: "fs", FSPath: "/primary/registry"},
	}
	secondary := &config.SiteConfig{
		Registry: &config.RegistryConfig{FSPath: "/secondary/registry"},
	}
	r := New(primary, secondary, "", false, sshexec.Default)
	pairs := r.PathPairs()
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(pairs))
	}
	if pairs[0].Src != "/primary/registry" {
		t.Errorf("Src = %q", pairs[0].Src)
	}
	if pairs[0].Dst != "/secondary/registry" {
		t.Errorf("Dst = %q", pairs[0].Dst)
	}
}

func TestNewRegistryPrimaryPathFallback(t *testing.T) {
	primary := &config.SiteConfig{
		SSHHost:  "p:22",
		Registry: &config.RegistryConfig{Mode: "fs", FSPath: "/registry"},
	}
	secondary := &config.SiteConfig{}
	r := New(primary, secondary, "", false, sshexec.Default)
	pairs := r.PathPairs()
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(pairs))
	}
	if pairs[0].Dst != "/registry" {
		t.Errorf("Dst should fall back to primary path: %q", pairs[0].Dst)
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "fs_storage" {
		t.Errorf("Name() = %q, want fs_storage", r.Name())
	}
}

// fsMockRunner implements localcmd.Runner.
type fsMockRunner struct {
	out []byte
	err error
}

func (m *fsMockRunner) Run(ctx context.Context, name string, args, env []string) ([]byte, error) {
	return m.out, m.err
}
