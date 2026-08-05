// Package fsstorage reconciles filesystem-backed blob storage directories
// (uploads, CI artifacts, LFS objects, packages, registry) via rsync over
// SSH. Used when object storage backend is "fs" rather than S3.
package fsstorage

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

const name = "fs_storage"

// Reconciler rsyncs multiple filesystem paths from primary to secondary.
type Reconciler struct {
	sshHost   string
	sshCfg    sshexec.Config
	pathPairs []PathPair
	dryRun    bool
	runner    localcmd.Runner
}

// PathPair describes a source path on the primary and its corresponding
// destination on the secondary.
type PathPair struct {
	Src string
	Dst string
}

// New creates an FS storage reconciler from the primary/secondary configs.
func New(primary, secondary *config.SiteConfig, dryRun bool, sshCfg sshexec.Config) *Reconciler {
	r := &Reconciler{
		sshHost: primary.SSHHost,
		sshCfg:  sshCfg,
		dryRun:  dryRun,
		runner:  localcmd.Default,
	}

	for _, p := range primary.ObjectStore.FSPaths {
		r.pathPairs = append(r.pathPairs, PathPair{Src: p, Dst: p})
	}

	if primary.Registry != nil && primary.Registry.Mode == "fs" && primary.Registry.FSPath != "" {
		dst := primary.Registry.FSPath
		if secondary.Registry != nil && secondary.Registry.FSPath != "" {
			dst = secondary.Registry.FSPath
		}
		r.pathPairs = append(r.pathPairs, PathPair{
			Src: primary.Registry.FSPath,
			Dst: dst,
		})
	}

	return r
}

// WithRunner returns a copy of r with the given localcmd.Runner.
func (r *Reconciler) WithRunner(runner localcmd.Runner) *Reconciler {
	cp := *r
	cp.runner = runner
	return &cp
}

// PathPairs returns the configured path pairs (for test inspection).
func (r *Reconciler) PathPairs() []PathPair { return r.pathPairs }

func (r *Reconciler) Name() string { return name }

// Reconcile rsyncs each path pair sequentially.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	if len(r.pathPairs) == 0 {
		return reconciler.Result{OK: true, Detail: "no fs paths configured"}
	}

	start := time.Now()
	failed := 0
	repaired := 0

	for _, pair := range r.pathPairs {
		select {
		case <-ctx.Done():
			return reconciler.Result{OK: false, Detail: "cancelled", Remaining: len(r.pathPairs)}
		default:
		}
		if err := r.rsyncPath(ctx, pair); err != nil {
			failed++
			metrics.DriftTotal.WithLabelValues(name+":"+pair.Src, "critical").Inc()
		} else {
			repaired++
		}
	}

	elapsed := time.Since(start)
	resultStr := "ok"
	if failed > 0 {
		resultStr = "error"
	}
	metrics.SyncDurationSeconds.WithLabelValues(name, resultStr).Observe(elapsed.Seconds())

	if failed > 0 {
		return reconciler.Result{
			OK:        false,
			Detail:    fmt.Sprintf("rsynced %d/%d paths in %s (%d failed)", repaired, len(r.pathPairs), elapsed, failed),
			Repaired:  repaired,
			Remaining: failed,
		}
	}
	metrics.LastSyncTimestamp.WithLabelValues(name).Set(float64(time.Now().Unix()))
	return reconciler.Result{
		OK:       true,
		Detail:   fmt.Sprintf("rsynced %d paths in %s", len(r.pathPairs), elapsed),
		Repaired: repaired,
	}
}

func (r *Reconciler) rsyncPath(ctx context.Context, pair PathPair) error {
	ep, err := sshexec.ParseEndpoint(r.sshHost)
	if err != nil {
		return err
	}
	sshCmd, err := r.sshCfg.SSHString(r.sshHost)
	if err != nil {
		return err
	}
	args := []string{
		"-az", "--delete", "--checksum",
		"-e", sshCmd,
		"--rsync-path", "sudo rsync",
	}
	if r.dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args,
		fmt.Sprintf("%s:%s/", ep.Destination(), pair.Src),
		pair.Dst+"/",
	)

	out, err := localcmd.RunWith(ctx, r.runner, "rsync", args, nil)
	if err != nil {
		errStr := strings.TrimSpace(string(out))
		if strings.Contains(errStr, "No such file or directory") ||
			strings.Contains(errStr, "change_dir") {
			return nil
		}
		return fmt.Errorf("rsync %s: %w: %s", pair.Src, err, errStr)
	}
	return nil
}
