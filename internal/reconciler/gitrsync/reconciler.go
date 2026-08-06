// Package gitrsync reconciles git repository data between primary and
// secondary filesystems using rsync over SSH. It copies the entire
// /var/opt/gitlab/git-data/repositories tree, preserving the hashed
// storage layout.
package gitrsync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/localcmd"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
	"github.com/lknappich/syncctl/internal/sshexec"
)

const name = "git_rsync"

// Reconciler rsyncs the primary git-data tree to the secondary.
type Reconciler struct {
	site    string
	sshHost string
	srcPath string
	dstPath string
	sshCfg  sshexec.Config
	dryRun  bool
	runner  localcmd.Runner
}

// New creates a git rsync reconciler from a primary/secondary config pair.
func New(primary, secondary *config.SiteConfig, dryRun bool, sshCfg sshexec.Config) *Reconciler {
	return &Reconciler{
		site:    secondary.Name,
		sshHost: primary.SSHHost,
		srcPath: primary.Git.ReposPath,
		dstPath: secondary.Git.ReposPath,
		sshCfg:  sshCfg,
		dryRun:  dryRun,
		runner:  localcmd.Default,
	}
}

// WithRunner returns a copy of r with the given localcmd.Runner.
// Used by tests to inject a mock; production callers leave r.runner as
// localcmd.Default.
func (r *Reconciler) WithRunner(runner localcmd.Runner) *Reconciler {
	cp := *r
	cp.runner = runner
	return &cp
}

func (r *Reconciler) Name() string { return reconciler.QualifyName(name, r.site) }

// Reconcile runs rsync over SSH from primary to secondary, with
// --delete --checksum to ensure the destination is an exact mirror.
// The remote side uses "sudo rsync" to read git-owned files.
// The local side also uses sudo (via rsync-path on the receiver) to
// write into git-owned directories.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	start := time.Now()
	ep, err := sshexec.ParseEndpoint(r.sshHost)
	if err != nil {
		metrics.DriftTotal.WithLabelValues(name, "critical").Inc()
		return reconciler.Result{OK: false, Detail: err.Error(), Remaining: 1}
	}
	sshCmd, err := r.sshCfg.SSHString(r.sshHost)
	if err != nil {
		metrics.DriftTotal.WithLabelValues(name, "critical").Inc()
		return reconciler.Result{OK: false, Detail: err.Error(), Remaining: 1}
	}
	args := []string{
		"-az", "--delete", "--checksum",
		// -s stops the remote shell from expanding the path, so a
		// repos_path
		// containing spaces or glob characters is taken literally.
		"-s",
		"-e", sshCmd,
		"--rsync-path", "sudo rsync",
	}
	if r.dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args,
		fmt.Sprintf("%s:%s/", ep.Destination(), r.srcPath),
		r.dstPath+"/",
	)

	out, err := localcmd.RunWith(ctx, r.runner, "rsync", args, nil)
	elapsed := time.Since(start)
	metrics.SyncDurationSeconds.WithLabelValues(r.Name(), errResult(err)).Observe(elapsed.Seconds())
	if err != nil {
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{
			OK:        false,
			Detail:    fmt.Sprintf("rsync failed: %s: %s", err, strings.TrimSpace(string(out))),
			Remaining: 1,
		}
	}
	metrics.LastSyncTimestamp.WithLabelValues(r.Name()).Set(float64(time.Now().Unix()))
	detail := fmt.Sprintf("rsync ok in %s", elapsed)
	if r.dryRun {
		detail += " (dry-run)"
	}
	return reconciler.Result{OK: true, Detail: detail}
}

func errResult(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
