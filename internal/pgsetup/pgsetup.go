// Package pgsetup implements `syncctl pg setup` — bootstraps a secondary
// PostgreSQL as a physical streaming replica of the primary using
// pg_basebackup, then writes the standby configuration.
package pgsetup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// basebackupRunner is the minimal interface for running pg_basebackup.
// The default uses exec.CommandContext; tests inject a mock.
type basebackupRunner interface {
	Run() error
}

// Options controls a pg_basebackup-based standby bootstrap.
type Options struct {
	// PrimaryDSN is the libpq conninfo for the primary. It MUST NOT
	// contain a password — command lines are readable by every local
	// user via /proc/<pid>/cmdline and ps. Supply the password in
	// Password instead; it is passed to the child via PGPASSWORD.
	PrimaryDSN string
	Password   string
	DataDir    string
	SlotName   string
	DryRun     bool
}

// runBasebackup is the factory that builds a basebackupRunner; tests
// can replace it.
var runBasebackup = defaultBasebackupFactory

func defaultBasebackupFactory(ctx context.Context, opts Options) basebackupRunner {
	// #nosec G204 -- argv vector, not a shell string; the binary name is
	// fixed and the arguments come from validated config.
	cmd := exec.CommandContext(ctx, "pg_basebackup",
		buildBasebackupArgs(opts)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if opts.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+opts.Password)
	}
	return cmd
}

func buildBasebackupArgs(opts Options) []string {
	args := []string{"-D", opts.DataDir, "-d", opts.PrimaryDSN, "-X", "stream", "-c", "fast", "-R", "-P"}
	if opts.SlotName != "" {
		args = append(args, "-S", opts.SlotName, "--create-slot")
	}
	return args
}

// dsnPasswordRe matches a libpq password field, quoted or bare.
var dsnPasswordRe = regexp.MustCompile(`password=('(?:[^'\\]|\\.)*'|[^\s]*)`)

// RedactDSN replaces every libpq password value with ***. It is a
// backstop for printing: callers should keep passwords out of DSNs in
// the first place, but nothing that reaches a terminal or a log should
// depend on that having been done correctly.
func RedactDSN(s string) string {
	return dsnPasswordRe.ReplaceAllString(s, "password=***")
}

// Run performs the setup: validates the data dir is empty or absent,
// runs pg_basebackup, writes standby.signal and primary_conninfo into
// postgresql.auto.conf.
func Run(ctx context.Context, opts Options) error {
	if opts.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if opts.PrimaryDSN == "" {
		return fmt.Errorf("primary_dsn is required")
	}

	if err := checkDataDir(opts.DataDir); err != nil {
		return err
	}

	args := buildBasebackupArgs(opts)
	if opts.DryRun {
		fmt.Printf("[dry-run] pg_basebackup %s\n", RedactDSN(strings.Join(args, " ")))
		if opts.Password != "" {
			fmt.Println("[dry-run] PGPASSWORD supplied via environment")
		}
		return nil
	}
	fmt.Printf("running pg_basebackup into %s ...\n", opts.DataDir)
	if err := runBasebackup(ctx, opts).Run(); err != nil {
		return fmt.Errorf("pg_basebackup: %w", err)
	}

	if err := appendConnInfoAppname(opts.DataDir, opts.SlotName); err != nil {
		return err
	}

	fmt.Printf("standby bootstrap complete. data_dir=%s\n", opts.DataDir)
	return nil
}

// checkDataDir ensures the target is empty/absent so we don't clobber
// an existing data directory.
func checkDataDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("data_dir %s is not empty (refusing to overwrite)", dir)
	}
	return nil
}

// appendConnInfoAppname appends/updates the primary_conninfo line in
// postgresql.auto.conf so application_name is set for pg_stat_replication.
// It parses the file line-by-line, handling missing/malformed lines and
// ensuring a trailing newline.
func appendConnInfoAppname(dataDir, appName string) error {
	if appName == "" {
		return nil
	}
	path := filepath.Join(dataDir, "postgresql.auto.conf")
	// #nosec G304 -- the file pg_basebackup just wrote into the PGDATA
	// the operator named with --data-dir.
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read postgresql.auto.conf: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "primary_conninfo = ") {
			if !strings.Contains(line, "application_name=") {
				lines[i] = injectAppName(line, appName)
			}
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("primary_conninfo = 'application_name=%s'", appName))
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	// #nosec G703 -- same operator-supplied PGDATA path as the read above.
	return os.WriteFile(path, []byte(out), 0o600)
}

// injectAppName inserts application_name=appName before the closing
// quote of a primary_conninfo line. Handles both single-quoted and
// unquoted values.
func injectAppName(line, appName string) string {
	idx := strings.LastIndex(line, "'")
	if idx < 0 {
		return line + " application_name=" + appName + "'"
	}
	return line[:idx] + " application_name=" + appName + "'"
}
