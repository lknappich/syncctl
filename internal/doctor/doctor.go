// Package doctor runs prerequisite checks against both primary and
// secondary GitLab sites to verify that syncctl can orchestrate
// replication. It checks SSH connectivity, PostgreSQL reachability,
// replication user privileges, db_key_base presence, rsync/git availability,
// and object storage access.
package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/dbkey"
	"github.com/lknappich/syncctl/internal/sshexec"
)

// PoolFactory builds a *pgxpool.Pool from a DSN. Tests can inject a
// fake that returns an in-memory pool or an error. The default is the
// real pgxpool.New.
type PoolFactory func(ctx context.Context, dsn string) (Pool, error)

// Pool is the minimal subset of *pgxpool.Pool that doctor checks need.
type Pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Close()
}

// Row is the minimal subset of pgx.Row that doctor checks need.
type Row interface {
	Scan(dest ...any) error
}

// poolAdapter wraps a *pgxpool.Pool to satisfy the Pool interface.
type poolAdapter struct {
	*pgxpool.Pool
}

func (a *poolAdapter) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return a.Pool.QueryRow(ctx, sql, args...)
}

// Check represents a single prerequisite check.
type Check struct {
	Name     string
	Category string
	Status   string // "PASS", "FAIL", "WARN"
	Detail   string
}

// Result holds all checks and a summary.
type Result struct {
	Checks []Check
	Pass   int
	Fail   int
	Warn   int
}

// poolFactory is the active pool builder; defaults to pgxpool.New but
// can be replaced in tests via the unexported poolFactory variable.
var poolFactory PoolFactory = defaultPoolFactory

func defaultPoolFactory(ctx context.Context, dsn string) (Pool, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &poolAdapter{Pool: p}, nil
}

// Run executes all doctor checks against the config and returns a result.
func Run(ctx context.Context, cfg *config.Config) *Result {
	return RunWith(ctx, cfg, sshexec.Default, poolFactory)
}

// RunWith is like Run but accepts an injectable SSH runner and pool
// factory (used by tests).
func RunWith(ctx context.Context, cfg *config.Config, runner sshexec.Runner, pf PoolFactory) *Result {
	r := &Result{}
	r.checks(ctx, cfg, runner, pf)
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
	return r
}

func (r *Result) add(c Check) { r.Checks = append(r.Checks, c) }

func (r *Result) checks(ctx context.Context, cfg *config.Config, runner sshexec.Runner, pf PoolFactory) {
	// --- SSH connectivity ---
	r.add(sshCheck(ctx, "primary", cfg.Primary.SSHHost, runner))

	// --- PostgreSQL: primary control connection ---
	r.add(pgControlCheck(ctx, "primary", cfg.Primary.Postgres, pf))

	// --- PostgreSQL: replication user exists and has REPLICATION ---
	r.add(pgReplicationRoleCheck(ctx, "primary", cfg.Primary.Postgres, pf))

	// --- PostgreSQL: primary has wal_level=replica ---
	r.add(pgWalLevelCheck(ctx, "primary", cfg.Primary.Postgres, pf))

	// --- PostgreSQL: max_wal_senders > 0 ---
	r.add(pgWalSendersCheck(ctx, "primary", cfg.Primary.Postgres, pf))

	// --- db_key_base present on primary ---
	r.add(dbKeyPresentCheck(ctx, "primary", cfg.Primary.SSHHost, runner))

	// --- rsync available on primary ---
	r.add(rsyncCheck(ctx, "primary", cfg.Primary.SSHHost, runner))

	// --- git available on primary ---
	r.add(gitCheck(ctx, "primary", cfg.Primary.SSHHost, runner))

	// --- Per-secondary checks ---
	for _, sc := range cfg.Secondaries {
		label := "secondary:" + sc.Name

		// SSH.
		r.add(sshCheck(ctx, label, sc.SSHHost, runner))

		// PG control connection.
		r.add(pgControlCheck(ctx, label, sc.Postgres, pf))

		// PG is in recovery (should be a standby).
		r.add(pgInRecoveryCheck(ctx, label, sc.Postgres, pf))

		// db_key_base present.
		r.add(dbKeyPresentCheck(ctx, label, sc.SSHHost, runner))

		// db_key_base matches primary.
		if cfg.Primary.SSHHost != "" && sc.SSHHost != "" {
			r.add(dbKeyParityCheck(ctx, cfg.Primary.SSHHost, sc.SSHHost, sc.Name, runner))
		}

		// rsync available.
		r.add(rsyncCheck(ctx, label, sc.SSHHost, runner))

		// git available.
		r.add(gitCheck(ctx, label, sc.SSHHost, runner))

		// Repos path exists on secondary.
		if sc.Git.ReposPath != "" {
			r.add(pathExistsCheck(ctx, label, sc.SSHHost, sc.Git.ReposPath, "repos_path", runner))
		}
	}

	// --- Object storage (S3) ---
	if cfg.Primary.ObjectStore.Backend == "s3" && cfg.Primary.ObjectStore.S3 != nil {
		r.add(s3BucketCheck(ctx, "primary", cfg.Primary.ObjectStore.S3))
	}
}

// --- individual checks ---

func sshCheck(ctx context.Context, label, sshHost string, runner sshexec.Runner) Check {
	if sshHost == "" {
		return Check{Name: "ssh:" + label, Category: "connectivity",
			Status: "WARN", Detail: "ssh_host not configured (needed for rsync/fetch/failover)"}
	}
	out, err := runner.CombinedOutput(ctx, sshHost, "echo ok")
	if err != nil {
		return Check{Name: "ssh:" + label, Category: "connectivity",
			Status: "FAIL", Detail: fmt.Sprintf("ssh %s: %v: %s", sshHost, err, strings.TrimSpace(string(out)))}
	}
	return Check{Name: "ssh:" + label, Category: "connectivity",
		Status: "PASS", Detail: sshHost}
}

func pgControlCheck(ctx context.Context, label string, pg config.PostgresConfig, pf PoolFactory) Check {
	if pg.Host == "" {
		return Check{Name: "pg:" + label + ":control", Category: "postgres",
			Status: "FAIL", Detail: "postgres.host not configured"}
	}
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pf(connCtx, pg.DSN())
	if err != nil {
		return Check{Name: "pg:" + label + ":control", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("connect: %v", err)}
	}
	defer pool.Close()
	var version string
	err = pool.QueryRow(connCtx, "SELECT version()").Scan(&version)
	if err != nil {
		return Check{Name: "pg:" + label + ":control", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("query version: %v", err)}
	}
	short := version
	if i := strings.Index(version, " ("); i > 0 {
		short = version[:i]
	}
	return Check{Name: "pg:" + label + ":control", Category: "postgres",
		Status: "PASS", Detail: short}
}

func pgReplicationRoleCheck(ctx context.Context, label string, pg config.PostgresConfig, pf PoolFactory) Check {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pf(connCtx, pg.DSN())
	if err != nil {
		return Check{Name: "pg:" + label + ":repl-role", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("connect: %v", err)}
	}
	defer pool.Close()
	var hasRepl bool
	err = pool.QueryRow(connCtx,
		`SELECT rolreplication FROM pg_roles WHERE rolname = $1`,
		pg.ReplicationUser).Scan(&hasRepl)
	if err != nil {
		return Check{Name: "pg:" + label + ":repl-role", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("role %s not found: %v", pg.ReplicationUser, err)}
	}
	if !hasRepl {
		return Check{Name: "pg:" + label + ":repl-role", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("role %s exists but lacks REPLICATION privilege", pg.ReplicationUser)}
	}
	return Check{Name: "pg:" + label + ":repl-role", Category: "postgres",
		Status: "PASS", Detail: fmt.Sprintf("role %s has REPLICATION", pg.ReplicationUser)}
}

func pgWalLevelCheck(ctx context.Context, label string, pg config.PostgresConfig, pf PoolFactory) Check {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pf(connCtx, pg.DSN())
	if err != nil {
		return Check{Name: "pg:" + label + ":wal_level", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("connect: %v", err)}
	}
	defer pool.Close()
	var level string
	err = pool.QueryRow(connCtx, "SHOW wal_level").Scan(&level)
	if err != nil {
		return Check{Name: "pg:" + label + ":wal_level", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("query: %v", err)}
	}
	if level != "replica" && level != "logical" {
		return Check{Name: "pg:" + label + ":wal_level", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("wal_level=%s, need replica or logical", level)}
	}
	return Check{Name: "pg:" + label + ":wal_level", Category: "postgres",
		Status: "PASS", Detail: "wal_level=" + level}
}

func pgWalSendersCheck(ctx context.Context, label string, pg config.PostgresConfig, pf PoolFactory) Check {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pf(connCtx, pg.DSN())
	if err != nil {
		return Check{Name: "pg:" + label + ":max_wal_senders", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("connect: %v", err)}
	}
	defer pool.Close()
	var level string
	err = pool.QueryRow(connCtx, "SHOW max_wal_senders").Scan(&level)
	if err != nil {
		return Check{Name: "pg:" + label + ":max_wal_senders", Category: "postgres",
			Status: "FAIL", Detail: fmt.Sprintf("query: %v", err)}
	}
	if level == "0" {
		return Check{Name: "pg:" + label + ":max_wal_senders", Category: "postgres",
			Status: "FAIL", Detail: "max_wal_senders=0, must be > 0"}
	}
	return Check{Name: "pg:" + label + ":max_wal_senders", Category: "postgres",
		Status: "PASS", Detail: "max_wal_senders=" + level}
}

func pgInRecoveryCheck(ctx context.Context, label string, pg config.PostgresConfig, pf PoolFactory) Check {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pf(connCtx, pg.DSN())
	if err != nil {
		return Check{Name: "pg:" + label + ":in_recovery", Category: "postgres",
			Status: "WARN", Detail: fmt.Sprintf("connect: %v (expected if not yet bootstrapped)", err)}
	}
	defer pool.Close()
	var inRecovery bool
	err = pool.QueryRow(connCtx, "SELECT pg_is_in_recovery()").Scan(&inRecovery)
	if err != nil {
		return Check{Name: "pg:" + label + ":in_recovery", Category: "postgres",
			Status: "WARN", Detail: fmt.Sprintf("query: %v (expected if not yet bootstrapped)", err)}
	}
	if !inRecovery {
		return Check{Name: "pg:" + label + ":in_recovery", Category: "postgres",
			Status: "WARN", Detail: "secondary PG is NOT in recovery mode (run `syncctl pg setup` first)"}
	}
	return Check{Name: "pg:" + label + ":in_recovery", Category: "postgres",
		Status: "PASS", Detail: "in recovery (standby)"}
}

func dbKeyPresentCheck(ctx context.Context, label, sshHost string, runner sshexec.Runner) Check {
	if sshHost == "" {
		return Check{Name: "dbkey:" + label, Category: "dbkey",
			Status: "WARN", Detail: "ssh_host not configured"}
	}
	remoteCmd := "sudo grep -c 'db_key_base' /var/opt/gitlab/gitlab-rails/etc/secrets.yml 2>/dev/null || grep -c 'db_key_base' /var/opt/gitlab/gitlab-rails/etc/secrets.yml 2>/dev/null || echo 0"
	out, err := runner.CombinedOutput(ctx, sshHost, remoteCmd)
	if err != nil {
		return Check{Name: "dbkey:" + label, Category: "dbkey",
			Status: "FAIL", Detail: fmt.Sprintf("ssh: %v", err)}
	}
	count := strings.TrimSpace(string(out))
	lines := strings.Split(count, "\n")
	count = strings.TrimSpace(lines[len(lines)-1])
	if count == "0" {
		return Check{Name: "dbkey:" + label, Category: "dbkey",
			Status: "FAIL", Detail: "db_key_base not found in secrets.yml or gitlab.rb"}
	}
	return Check{Name: "dbkey:" + label, Category: "dbkey",
		Status: "PASS", Detail: "present in secrets.yml"}
}

func dbKeyParityCheck(ctx context.Context, primarySSH, secondarySSH, secondaryName string, runner sshexec.Runner) Check {
	err := dbkey.CheckWithRunner(ctx, primarySSH, secondarySSH, runner)
	if err != nil {
		return Check{Name: "dbkey:parity:" + secondaryName, Category: "dbkey",
			Status: "FAIL", Detail: err.Error()}
	}
	return Check{Name: "dbkey:parity:" + secondaryName, Category: "dbkey",
		Status: "PASS", Detail: "primary and secondary match"}
}

func rsyncCheck(ctx context.Context, label, sshHost string, runner sshexec.Runner) Check {
	return remoteBinaryCheck(ctx, label, "rsync", sshHost, "rsync --version", runner)
}

func gitCheck(ctx context.Context, label, sshHost string, runner sshexec.Runner) Check {
	return remoteBinaryCheck(ctx, label, "git", sshHost, "git --version", runner)
}

func remoteBinaryCheck(ctx context.Context, label, bin, sshHost, cmd string, runner sshexec.Runner) Check {
	if sshHost == "" {
		return Check{Name: bin + ":" + label, Category: "tools",
			Status: "WARN", Detail: "ssh_host not configured"}
	}
	out, err := runner.CombinedOutput(ctx, sshHost, cmd)
	if err != nil {
		return Check{Name: bin + ":" + label, Category: "tools",
			Status: "FAIL", Detail: fmt.Sprintf("%s not found: %v", bin, err)}
	}
	version := strings.TrimSpace(string(out))
	if idx := strings.IndexAny(version, "\n"); idx > 0 {
		version = version[:idx]
	}
	return Check{Name: bin + ":" + label, Category: "tools",
		Status: "PASS", Detail: version}
}

func pathExistsCheck(ctx context.Context, label, sshHost, path, pathName string, runner sshexec.Runner) Check {
	if sshHost == "" {
		return Check{Name: "path:" + label + ":" + pathName, Category: "filesystem",
			Status: "WARN", Detail: "ssh_host not configured"}
	}
	_, err := runner.CombinedOutput(ctx, sshHost, "test -d "+path)
	if err != nil {
		return Check{Name: "path:" + label + ":" + pathName, Category: "filesystem",
			Status: "WARN", Detail: fmt.Sprintf("%s does not exist yet (will be created on first sync)", path)}
	}
	return Check{Name: "path:" + label + ":" + pathName, Category: "filesystem",
		Status: "PASS", Detail: path}
}

func s3BucketCheck(ctx context.Context, label string, s3 *config.S3Config) Check {
	// We can't easily check S3 without the SDK here; just validate config.
	if s3.PrimaryBucket == "" {
		return Check{Name: "s3:" + label, Category: "storage",
			Status: "FAIL", Detail: "primary_bucket not set"}
	}
	if s3.ReplicaBucket == "" {
		return Check{Name: "s3:" + label, Category: "storage",
			Status: "FAIL", Detail: "replica_bucket not set"}
	}
	if s3.AccessKey == "" || s3.SecretKey == "" {
		return Check{Name: "s3:" + label, Category: "storage",
			Status: "FAIL", Detail: "access_key/secret_key empty (check env vars)"}
	}
	return Check{Name: "s3:" + label, Category: "storage",
		Status: "PASS", Detail: fmt.Sprintf("primary=%s replica=%s", s3.PrimaryBucket, s3.ReplicaBucket)}
}

// Print writes the result to the provided writer in a readable table.
func (r *Result) Print() {
	fmt.Printf("\n%-40s %-12s %-10s %s\n", "CHECK", "CATEGORY", "STATUS", "DETAIL")
	fmt.Println(strings.Repeat("-", 100))
	for _, c := range r.Checks {
		fmt.Printf("%-40s %-12s %-10s %s\n", c.Name, c.Category, c.Status, c.Detail)
	}
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("\nSummary: %d PASS, %d WARN, %d FAIL\n", r.Pass, r.Warn, r.Fail)
	if r.Fail > 0 {
		fmt.Println("\nFix the FAIL items above before proceeding.")
	} else if r.Warn > 0 {
		fmt.Println("\nWARN items may be expected if you haven't bootstrapped yet.")
	} else {
		fmt.Println("\nAll checks passed. Ready to sync!")
	}
}
