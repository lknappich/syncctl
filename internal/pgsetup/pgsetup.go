// Package pgsetup implements `syncctl pg setup` — bootstraps a secondary
// PostgreSQL as a physical streaming replica of the primary using
// pg_basebackup, then writes the standby configuration.
package pgsetup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	// ApplicationName replaces any application_name in the standby's
	// primary_conninfo. The lag query finds the standby's row in
	// pg_stat_replication by it, so it must be the secondary's site name.
	ApplicationName string
	DryRun          bool
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
	if opts.ApplicationName == "" {
		return fmt.Errorf("application_name is required")
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
		fmt.Printf("[dry-run] set application_name=%s in primary_conninfo\n", opts.ApplicationName)
		return nil
	}
	fmt.Printf("running pg_basebackup into %s ...\n", opts.DataDir)
	if err := runBasebackup(ctx, opts).Run(); err != nil {
		return fmt.Errorf("pg_basebackup: %w", err)
	}

	if err := setConnInfoAppName(opts.DataDir, opts.ApplicationName); err != nil {
		return err
	}
	fmt.Printf("primary_conninfo application_name set to %s\n", opts.ApplicationName)

	fmt.Printf("standby bootstrap complete. data_dir=%s\n", opts.DataDir)
	return nil
}

// checkDataDir ensures the target is empty/absent so we don't clobber
// an existing data directory.
func checkDataDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil:
		if len(entries) > 0 {
			return fmt.Errorf("data_dir %s is not empty (refusing to overwrite)", dir)
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		// pg_basebackup creates it.
		return nil
	default:
		// Permission denied, or a path that exists as a file. Treating
		// these as "empty" let pg_basebackup run against a directory we
		// could not actually inspect.
		return fmt.Errorf("inspect data_dir %s: %w", dir, err)
	}
}

// primaryConnInfoKeyRe finds primary_conninfo assignments; setting names
// are case-insensitive in Postgres config files.
var primaryConnInfoKeyRe = regexp.MustCompile(`(?i)^\s*primary_conninfo(?:\s|=|$)`)

// primaryConnInfoRe splits an assignment into its prefix, the body of the
// quoted value (doubled quotes and backslash escapes allowed), and any
// trailing comment.
var primaryConnInfoRe = regexp.MustCompile(`(?i)^(\s*primary_conninfo\s*=?\s*)'((?:[^'\\]|\\.|'')*)'(\s*(?:#.*)?)$`)

// setConnInfoAppName sets application_name in the primary_conninfo that
// pg_basebackup -R wrote into postgresql.auto.conf, replacing whatever
// value came from the DSN. Only the last assignment is rewritten: a
// backup of a primary that was once a standby still carries its old
// primary_conninfo, -R appends the new one after it, and the last one in
// the file is the one Postgres uses.
func setConnInfoAppName(dataDir, appName string) error {
	path := filepath.Join(dataDir, "postgresql.auto.conf")
	// #nosec G304 -- the file pg_basebackup just wrote into the PGDATA
	// the operator named with --data-dir.
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read postgresql.auto.conf: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	idx := -1
	for i, line := range lines {
		if primaryConnInfoKeyRe.MatchString(line) {
			idx = i
		}
	}
	// A conninfo holding only application_name would point the standby at
	// the local socket, so there is nothing sensible to add one to.
	if idx < 0 {
		return fmt.Errorf("%s has no primary_conninfo; pg_basebackup -R should have written one", path)
	}
	m := primaryConnInfoRe.FindStringSubmatch(lines[idx])
	if m == nil {
		return fmt.Errorf("%s:%d: cannot parse the primary_conninfo value", path, idx+1)
	}
	params, err := parseConnInfo(unescapeConfString(m[2]))
	if err != nil {
		return fmt.Errorf("%s:%d: primary_conninfo: %w", path, idx+1, err)
	}
	params = setConnParam(params, "application_name", appName)
	lines[idx] = m[1] + "'" + escapeConfString(formatConnInfo(params)) + "'" + m[3]
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	// #nosec G703 -- same operator-supplied PGDATA path as the read above.
	return os.WriteFile(path, []byte(out), 0o600)
}

// unescapeConfString decodes the body of a single-quoted Postgres config
// file value the way the server's DeescapeQuotedString does.
func unescapeConfString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// Accumulating in a byte truncates \400-\777 the way the
				// server's char cast does.
				var v byte
				j := 0
				for ; j < 3 && i+j < len(s) && s[i+j] >= '0' && s[i+j] <= '7'; j++ {
					v = v<<3 | (s[i+j] - '0')
				}
				i += j - 1
				b.WriteByte(v)
			default:
				b.WriteByte(s[i])
			}
		case c == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
			b.WriteByte('\'')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// escapeConfString is the inverse of unescapeConfString. Line breaks are
// escaped because a quoted config value cannot span lines.
var escapeConfString = strings.NewReplacer(`\`, `\\`, `'`, `''`, "\n", `\n`, "\r", `\r`).Replace

type connParam struct {
	key, val string
}

// parseConnInfo parses a libpq key=value connection string the way
// libpq's conninfo_parse does. Errors name keys only, never values: one
// of them may be a password.
func parseConnInfo(s string) ([]connParam, error) {
	if strings.HasPrefix(s, "postgresql://") || strings.HasPrefix(s, "postgres://") {
		return nil, errors.New("URI connection strings are not supported")
	}
	var params []connParam
	i := 0
	skipSpace := func() {
		for i < len(s) && isConnSpace(s[i]) {
			i++
		}
	}
	for {
		skipSpace()
		if i == len(s) {
			return params, nil
		}
		start := i
		for i < len(s) && s[i] != '=' && !isConnSpace(s[i]) {
			i++
		}
		key := s[start:i]
		skipSpace()
		if i == len(s) || s[i] != '=' {
			return nil, fmt.Errorf("missing \"=\" after %q", key)
		}
		i++
		skipSpace()
		var val strings.Builder
		if i < len(s) && s[i] == '\'' {
			i++
			for {
				if i == len(s) {
					return nil, fmt.Errorf("unterminated quoted value for %q", key)
				}
				c := s[i]
				i++
				if c == '\'' {
					break
				}
				if c == '\\' {
					if i == len(s) {
						return nil, fmt.Errorf("unterminated quoted value for %q", key)
					}
					c = s[i]
					i++
				}
				val.WriteByte(c)
			}
		} else {
			for i < len(s) && !isConnSpace(s[i]) {
				if s[i] == '\\' {
					i++
					if i == len(s) {
						break
					}
				}
				val.WriteByte(s[i])
				i++
			}
		}
		params = append(params, connParam{key, val.String()})
	}
}

func isConnSpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

// setConnParam sets key in place of its first occurrence and drops any
// later ones, since libpq lets the last occurrence win.
func setConnParam(params []connParam, key, val string) []connParam {
	out := params[:0]
	set := false
	for _, p := range params {
		if p.key == key {
			if set {
				continue
			}
			p.val, set = val, true
		}
		out = append(out, p)
	}
	if !set {
		out = append(out, connParam{key, val})
	}
	return out
}

// formatConnInfo quotes values exactly as libpq's appendConnStrVal does,
// which is what pg_basebackup -R uses, so parameters this package does not
// change are written back byte for byte.
func formatConnInfo(params []connParam) string {
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		if isPlainConnValue(p.val) {
			b.WriteString(p.val)
			continue
		}
		b.WriteByte('\'')
		for j := 0; j < len(p.val); j++ {
			if c := p.val[j]; c == '\'' || c == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(p.val[j])
		}
		b.WriteByte('\'')
	}
	return b.String()
}

func isPlainConnValue(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
