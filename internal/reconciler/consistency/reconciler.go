// Package consistency implements a periodic full-audit reconciler that
// compares row counts of key GitLab tables between primary and secondary,
// and runs `git fsck` on a sample of secondary repositories. It observes
// drift; it does not auto-repair in v0 (Phase 2 will add repairs).
package consistency

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
)

const name = "consistency_sweep"

// rowQuerier is the minimal pool surface rowCount needs.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// tablesToCount are the key GitLab tables whose row counts we compare.
var tablesToCount = []string{
	"projects", "namespaces", "users", "merge_requests",
	"issues", "notes", "ci_builds", "ci_pipelines", "labels", "milestones",
}

// Reconciler compares row counts and samples git fsck.
type Reconciler struct {
	primary       rowQuerier
	secondary     rowQuerier
	secondaryName string
	reposPath     string
	samplePct     float64
}

// New creates a consistency sweep reconciler.
func New(primary, secondary *pgxpool.Pool, secondaryName, reposPath string, samplePct float64) *Reconciler {
	return &Reconciler{
		primary:       primary,
		secondary:     secondary,
		secondaryName: secondaryName,
		reposPath:     reposPath,
		samplePct:     samplePct,
	}
}

// WithPools returns a copy of r with the given rowQuerier pools (for tests).
func (r *Reconciler) WithPools(primary, secondary rowQuerier) *Reconciler {
	cp := *r
	cp.primary = primary
	cp.secondary = secondary
	return &cp
}

func (r *Reconciler) Name() string { return name }

// Reconcile runs the full audit.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	result := reconciler.Result{Detail: "sweep complete"}
	drifts := 0

	for _, table := range tablesToCount {
		pCount, err := rowCount(ctx, r.primary, table)
		if err != nil {
			result.Remaining++
			result.Detail = fmt.Sprintf("%s; %s primary count error: %v", result.Detail, table, err)
			continue
		}
		sCount, err := rowCount(ctx, r.secondary, table)
		if err != nil {
			result.Remaining++
			result.Detail = fmt.Sprintf("%s; %s secondary count error: %v", result.Detail, table, err)
			continue
		}
		if pCount != sCount {
			if isApproxEqual(pCount, sCount) {
				continue
			}
			drifts++
			result.Remaining++
			metrics.DriftTotal.WithLabelValues("db:"+table, "warning").Inc()
			result.Detail = fmt.Sprintf("%s; %s drift: primary=%d secondary=%d", result.Detail, table, pCount, sCount)
		}
	}

	if r.reposPath != "" {
		fsckDrifts := r.sampleGitFsck(ctx)
		drifts += fsckDrifts
	}

	result.OK = drifts == 0
	result.Repaired = 0
	return result
}

// rowCount returns the approximate row count for a table.
func rowCount(ctx context.Context, pool rowQuerier, table string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `
		SELECT reltuples::bigint
		FROM pg_class
		WHERE relname = $1 AND relkind = 'r'
		LIMIT 1`, table).Scan(&n)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// isApproxEqual returns true if two reltuples estimates are close enough
// that the difference is likely just planner-estimate noise rather than
// real drift. Uses a 10% tolerance band (minimum 5 rows). This avoids
// false drift alerts from ANALYZE timing differences across two databases.
func isApproxEqual(a, b int64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	tolerance := a / 10
	if b > a {
		tolerance = b / 10
	}
	if tolerance < 5 {
		tolerance = 5
	}
	return diff <= tolerance
}

// sampleGitFsck walks the repos path, picks a random sample of .git
// directories, and runs `git fsck --no-dangling` on each. Returns the
// number of repos that failed fsck.
func (r *Reconciler) sampleGitFsck(ctx context.Context) int {
	if r.samplePct <= 0 || r.reposPath == "" {
		return 0
	}
	var allRepos []string
	err := filepath.Walk(r.reposPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("walk error during git fsck sample")
			return nil
		}
		if info.IsDir() && (filepath.Base(path) == ".git" || strings.HasSuffix(path, ".git")) {
			allRepos = append(allRepos, path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		log.Warn().Err(err).Str("repos_path", r.reposPath).Msg("filepath.Walk failed")
	}
	if len(allRepos) == 0 {
		return 0
	}
	n := int(float64(len(allRepos)) * r.samplePct)
	if n < 1 {
		n = 1
	}
	// #nosec G404 -- picks which repos to spot-check for corruption on a
	// replica we own. Nothing authenticates or authorizes on this value.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	failed := 0
	for i := 0; i < n; i++ {
		repo := allRepos[rng.Intn(len(allRepos))]
		if !gitFsck(ctx, repo) {
			failed++
			metrics.DriftTotal.WithLabelValues("git_fsck", "critical").Inc()
		}
	}
	return failed
}

// gitFsck runs `git fsck --no-dangling` on a repo and returns true if ok.
func gitFsck(ctx context.Context, repoPath string) bool {
	out, err := execGitFsck(ctx, repoPath)
	if err != nil {
		log.Warn().Err(err).Str("repo", repoPath).
			Str("output", strings.TrimSpace(out)).
			Msg("git fsck failed")
		return false
	}
	return true
}

func execGitFsck(ctx context.Context, repoPath string) (string, error) {
	// Inline exec to keep the package self-contained.
	return execCommand(ctx, "git", "-C", repoPath, "fsck", "--no-dangling")
}

func execCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := execCmd(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// execCmd is a variable so tests can stub it.
var execCmd = newExecCmd

// cmdRunner is the interface for running external commands; *exec.Cmd
// satisfies it natively.
type cmdRunner interface {
	CombinedOutput() ([]byte, error)
}

func newExecCmd(ctx context.Context, name string, args ...string) cmdRunner {
	// #nosec G204 -- argv vector, not a shell string; the only caller
	// passes a fixed `git fsck` invocation plus a repo path.
	return exec.CommandContext(ctx, name, args...)
}
