// Package gitfetch reconciles git repository data using per-project
// `git fetch --prune +refs/*:refs/*` instead of filesystem rsync. This
// mode is used when the primary's filesystem is not directly accessible
// (e.g. different cloud provider). It pulls the project list from the
// local replicated PostgreSQL DB (which is already 1:1 via WAL streaming)
// so no GitLab API calls are needed.
package gitfetch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/lknappich/syncctl/internal/localcmd"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/projectpath"
	"github.com/lknappich/syncctl/internal/reconciler"
	"github.com/lknappich/syncctl/internal/sshexec"
)

const name = "git_fetch"

// projectQuerier is the minimal pool surface listProjects needs.
type projectQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Reconciler fetches all project repos from the primary's Gitaly via SSH.
type Reconciler struct {
	primarySSHHost string
	reposPath      string
	secondaryName  string
	primaryPool    projectQuerier
	sshCfg         sshexec.Config
	dryRun         bool
	maxParallel    int
	runner         localcmd.Runner
}

// New creates a git fetch reconciler.
func New(primarySSHHost, reposPath, secondaryName string, primaryPool *pgxpool.Pool, dryRun bool, sshCfg sshexec.Config) *Reconciler {
	return &Reconciler{
		primarySSHHost: primarySSHHost,
		reposPath:      reposPath,
		secondaryName:  secondaryName,
		primaryPool:    primaryPool,
		sshCfg:         sshCfg,
		dryRun:         dryRun,
		maxParallel:    8,
		runner:         localcmd.Default,
	}
}

// WithRunner returns a copy of r with the given localcmd.Runner.
func (r *Reconciler) WithRunner(runner localcmd.Runner) *Reconciler {
	cp := *r
	cp.runner = runner
	return &cp
}

// WithPool returns a copy of r with the given projectQuerier (for tests).
func (r *Reconciler) WithPool(pool projectQuerier) *Reconciler {
	cp := *r
	cp.primaryPool = pool
	return &cp
}

func (r *Reconciler) Name() string { return name }

// Reconcile queries the DB for all project repository paths, then runs
// `git fetch --prune` on each local repo against the primary's SSH URL.
// Fetching is bounded-parallel via a worker pool sized by maxParallel.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	start := time.Now()

	projects, err := r.listProjects(ctx)
	if err != nil {
		metrics.DriftTotal.WithLabelValues(name, "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("list projects: %v", err), Remaining: 1}
	}

	if len(projects) == 0 {
		return reconciler.Result{OK: true, Detail: "no projects to sync"}
	}

	parallel := r.maxParallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(projects) {
		parallel = len(projects)
	}

	var failed, repaired int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)

loop:
	for _, p := range projects {
		p := p
		select {
		case <-gctx.Done():
			break loop
		default:
		}
		g.Go(func() error {
			if r.fetchOne(gctx, p) {
				atomic.AddInt64(&repaired, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
			return nil
		})
	}
	_ = g.Wait()

	elapsed := time.Since(start)
	resultStr := "ok"
	if failed > 0 {
		resultStr = "error"
	}
	metrics.SyncDurationSeconds.WithLabelValues(name, resultStr).Observe(elapsed.Seconds())

	if failed > 0 {
		metrics.DriftTotal.WithLabelValues(name, "warning").Inc()
		return reconciler.Result{
			OK:        false,
			Detail:    fmt.Sprintf("fetched %d/%d projects in %s (%d failed, parallel=%d)", repaired, len(projects), elapsed, failed, parallel),
			Repaired:  int(repaired),
			Remaining: int(failed),
		}
	}
	metrics.LastSyncTimestamp.WithLabelValues(name).Set(float64(time.Now().Unix()))
	return reconciler.Result{
		OK:       true,
		Detail:   fmt.Sprintf("fetched %d projects in %s (parallel=%d)", len(projects), elapsed, parallel),
		Repaired: int(repaired),
	}
}

// projectRow holds the minimal fields needed to locate and fetch a repo.
type projectRow struct {
	ID         int32
	RepoPath   string // e.g. "group/subgroup/project.git"
	HashedPath string // hashed storage path (if enabled)
}

// listProjects queries the replicated DB for all project repository paths.
// GitLab CE stores the storage shard in projects.repository_storage and
// the relative disk path in routes.path (which mirrors namespace/project
// for legacy storage) or in the hashed layout derived from the project
// id. We use the routes table — a public CE table — to resolve the
// human-readable path_with_namespace, then map it to the on-disk relative
// path the way Gitaly does: @hashed/XX/YYYY... for hashed storage,
// <namespace>/<project>.git for legacy.
func (r *Reconciler) listProjects(ctx context.Context) ([]projectRow, error) {
	rows, err := r.primaryPool.Query(ctx, `
		SELECT p.id,
		       p.repository_storage,
		       p.project_namespace_id,
		       p.hashed_storage,
		       r.path
		FROM projects p
		LEFT JOIN routes r
		  ON r.source_id = p.id
		 AND r.source_type = 'Project'
		WHERE p.repository_storage IS NOT NULL
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []projectRow
	for rows.Next() {
		var (
			p           projectRow
			storage     string
			namespaceID sql.NullInt64
			hashed      bool
			routePath   sql.NullString
		)
		if err := rows.Scan(&p.ID, &storage, &namespaceID, &hashed, &routePath); err != nil {
			return nil, err
		}
		p.RepoPath = repoDiskPath(storage, hashed, routePath.String, p.ID)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// repoDiskPath maps a project to its on-disk relative path under
// <storage>/repositories/, following the layout GitLab documents publicly
// in "Repository storage paths":
//   - hashed storage: @hashed/AA/BB/<hash>.git, where <hash> is the
//     lowercase hex SHA-256 of the project ID and AA/BB are its first
//     and second character pairs.
//   - legacy storage: <path_with_namespace>.git, taken from the route path.
func repoDiskPath(_ string, hashed bool, routePath string, projectID int32) string {
	if hashed {
		h := hashedStorageHash(projectID)
		return fmt.Sprintf("@hashed/%s/%s/%s.git", h[0:2], h[2:4], h)
	}
	if routePath == "" {
		return ""
	}
	return routePath + ".git"
}

// hashedStorageHash returns the 64-char lowercase hex SHA-256 of the
// project ID rendered as a decimal string — the digest GitLab's hashed
// storage layout is keyed on.
func hashedStorageHash(projectID int32) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d", projectID)
	return hex.EncodeToString(h.Sum(nil))
}

// fetchOne runs `git fetch --prune +refs/*:refs/* --no-tags +refs/tags/*:refs/tags/*`
// on a single local repo against the primary SSH URL. Returns true on
// success, false on failure.
func (r *Reconciler) fetchOne(ctx context.Context, p projectRow) bool {
	if p.RepoPath == "" {
		return false
	}

	localPath := filepath.Join(r.reposPath, p.RepoPath)
	remoteURL := fmt.Sprintf("ssh://%s/var/opt/gitlab/git-data/repositories/%s",
		r.primarySSHHost, p.RepoPath)

	args := []string{
		"-C", localPath,
		"fetch", "--prune", "--no-tags",
		remoteURL,
		"+refs/*:refs/*",
		"+refs/tags/*:refs/tags/*",
	}

	if r.dryRun {
		return true
	}

	projectTimeout, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := localcmd.RunWith(projectTimeout, r.runner, "git", args,
		[]string{"GIT_SSH_COMMAND=" + r.sshCfg.SSHString()})
	if err != nil {
		log.Warn().Err(err).Int32("project_id", p.ID).
			Str("repo", p.RepoPath).
			Str("output", strings.TrimSpace(string(out))).
			Msg("git fetch failed")
		return false
	}
	return true
}

// FetchProject fetches a single project by its path_with_namespace (e.g.
// "group/subgroup/project"). It resolves the repo disk path and runs a
// one-off git fetch. This is used by the webhook trigger for near-real-time
// per-project sync. Returns an error if the project cannot be found or
// the fetch fails.
func (r *Reconciler) FetchProject(ctx context.Context, projectPath string) error {
	if err := projectpath.Validate(projectPath); err != nil {
		return fmt.Errorf("invalid project path: %w", err)
	}
	p, err := r.resolveProject(ctx, projectPath)
	if err != nil {
		return err
	}
	if r.fetchOne(ctx, p) {
		return nil
	}
	return fmt.Errorf("git fetch failed for %s", projectPath)
}

// resolveProject looks up a project by its route path so the on-disk
// layout is derived the same way as in Reconcile — hashed storage needs
// the project ID, which the path alone does not carry. When no DB pool is
// available the legacy layout is the only thing we can infer.
func (r *Reconciler) resolveProject(ctx context.Context, projectPath string) (projectRow, error) {
	if r.primaryPool == nil {
		return projectRow{RepoPath: projectPath + ".git"}, nil
	}
	rows, err := r.primaryPool.Query(ctx, `
		SELECT p.id, p.repository_storage, p.hashed_storage
		FROM projects p
		JOIN routes r
		  ON r.source_id = p.id
		 AND r.source_type = 'Project'
		WHERE r.path = $1
		LIMIT 1`, projectPath)
	if err != nil {
		return projectRow{}, fmt.Errorf("look up project %s: %w", projectPath, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return projectRow{}, fmt.Errorf("look up project %s: %w", projectPath, err)
		}
		return projectRow{}, fmt.Errorf("project %s not found in replicated DB", projectPath)
	}
	var (
		p       projectRow
		storage string
		hashed  bool
	)
	if err := rows.Scan(&p.ID, &storage, &hashed); err != nil {
		return projectRow{}, fmt.Errorf("scan project %s: %w", projectPath, err)
	}
	p.RepoPath = repoDiskPath(storage, hashed, projectPath, p.ID)
	return p, nil
}

// maxParallel is exposed for future concurrent fetching; currently sequential.
func (r *Reconciler) SetMaxParallel(n int) { r.maxParallel = n }
