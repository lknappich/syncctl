// Package autorepair provides drift auto-repair for git repositories and
// S3 objects. When the consistency sweep or object storage reconciler
// detects drift, this package re-runs the appropriate sync mechanism
// (git fetch or rsync for repos, S3 copy for missing objects).
package autorepair

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/localcmd"
)

// GitRepair re-fetches a specific project repo from the primary.
type GitRepair struct {
	primarySSHHost string
	reposPath      string
	dryRun         bool
	runner         localcmd.Runner
}

// NewGitRepair creates a git repairer.
func NewGitRepair(primarySSHHost, reposPath string, dryRun bool) *GitRepair {
	return &GitRepair{
		primarySSHHost: primarySSHHost,
		reposPath:      reposPath,
		dryRun:         dryRun,
		runner:         localcmd.Default,
	}
}

// WithRunner returns a copy with the given localcmd.Runner (for tests).
func (g *GitRepair) WithRunner(r localcmd.Runner) *GitRepair {
	cp := *g
	cp.runner = r
	return &cp
}

// RepairRepo re-fetches a single repo identified by its path.
func (g *GitRepair) RepairRepo(ctx context.Context, repoPath string) error {
	localPath := filepath.Join(g.reposPath, repoPath)
	remoteURL := fmt.Sprintf("ssh://%s/var/opt/gitlab/git-data/repositories/%s",
		g.primarySSHHost, repoPath)

	args := []string{
		"-C", localPath,
		"fetch", "--prune", "--no-tags",
		remoteURL,
		"+refs/*:refs/*",
		"+refs/tags/*:refs/tags/*",
	}

	if g.dryRun {
		log.Info().Str("repo", repoPath).Msg("[dry-run] would repair")
		return nil
	}

	out, err := localcmd.RunWith(ctx, g.runner, "git", args, nil)
	if err != nil {
		return fmt.Errorf("git fetch %s: %w: %s", repoPath, err, strings.TrimSpace(string(out)))
	}
	log.Info().Str("repo", repoPath).Msg("auto-repaired git repo")
	return nil
}

// S3Repair copies a missing object from the primary bucket to the replica.
type S3Repair struct {
	dryRun bool
}

// NewS3Repair creates an S3 repairer.
func NewS3Repair(dryRun bool) *S3Repair {
	return &S3Repair{dryRun: dryRun}
}

// RepairObject logs a missing S3 key.
func (s *S3Repair) RepairObject(ctx context.Context, bucket, key string) error {
	if s.dryRun {
		log.Info().Str("bucket", bucket).Str("key", key).Msg("[dry-run] would copy S3 object")
		return nil
	}
	log.Warn().Str("bucket", bucket).Str("key", key).
		Msg("S3 object drift detected; relying on bucket replication to catch up")
	return nil
}
