// Package versionparity compares GitLab and PostgreSQL versions between
// the primary and secondary sites. Physical streaming replication
// requires the same PostgreSQL major version, and a GitLab replica
// running a different release than its primary is not a safe failover
// target.
//
// GitLab's version is read over SSH from the Omnibus version manifest,
// /opt/gitlab/version-manifest.txt, whose first line has the form
// "gitlab-ce 16.5.1" (or "gitlab-ee ..."). The file is documented in
// GitLab's public docs (https://docs.gitlab.com/administration/package_information/,
// "Checking the versions of bundled software"); the first-line format is
// observed on installs, not documented. PostgreSQL's version comes from the standard
// `SHOW server_version_num` statement.
package versionparity

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lknappich/syncctl/internal/sshexec"
)

// Status is the outcome of a single check.
type Status string

const (
	StatusOK   Status = "OK"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// Check names.
const (
	CheckPostgres = "postgres-major-version"
	CheckGitLab   = "gitlab-version"
)

// Result is the outcome of one comparison.
type Result struct {
	Check   string
	Status  Status
	Message string
}

// Row is the minimal subset of pgx.Row needed here.
type Row interface {
	Scan(dest ...any) error
}

// Querier is the minimal subset of a pgx pool needed here. Callers adapt
// *pgxpool.Pool to it (see doctor.Pool for the same pattern).
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// VersionManifestPath is the Omnibus manifest read over SSH.
const VersionManifestPath = "/opt/gitlab/version-manifest.txt"

const gitlabVersionCmd = "head -n 1 " + VersionManifestPath

const pgVersionSQL = "SHOW server_version_num"

// GitLabVersion is a parsed GitLab release.
type GitLabVersion struct {
	Major, Minor, Patch int
	Edition             string // "ce", "ee" or ""
}

func (v GitLabVersion) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Edition != "" {
		s += "-" + v.Edition
	}
	return s
}

var gitlabLineRe = regexp.MustCompile(`^\s*(?:gitlab-(ce|ee)\s+)?v?(\d+)\.(\d+)\.(\d+)(?:[-+.]?(ce|ee)(?:\.\d+)?)?(?:\s|$)`)

// truncate trims s and shortens it to at most n bytes for error messages.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ParseGitLabVersion scans the SSH output for the first line that looks
// like the Omnibus manifest header, e.g. "gitlab-ce 16.5.1",
// "gitlab-ee 16.5.1" or a bare "16.5.1" / "16.5.1-ce". Other lines (such
// as ssh's "Warning: Permanently added ..." on stderr) are skipped.
func ParseGitLabVersion(out string) (GitLabVersion, error) {
	for _, line := range strings.Split(out, "\n") {
		m := gitlabLineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		v := GitLabVersion{Edition: m[1]}
		if v.Edition == "" {
			v.Edition = m[5]
		}
		v.Major, _ = strconv.Atoi(m[2])
		v.Minor, _ = strconv.Atoi(m[3])
		v.Patch, _ = strconv.Atoi(m[4])
		return v, nil
	}
	return GitLabVersion{}, fmt.Errorf("unparsable GitLab version output %q", truncate(out, 200))
}

// PostgresMajor reads server_version_num and returns the major version.
func PostgresMajor(ctx context.Context, q Querier) (int, error) {
	var raw string
	if err := q.QueryRow(ctx, pgVersionSQL).Scan(&raw); err != nil {
		return 0, fmt.Errorf("query server_version_num: %w", err)
	}
	num, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || num < 100000 {
		if err == nil {
			return 0, fmt.Errorf("PostgreSQL < 10 unsupported (server_version_num %d)", num)
		}
		return 0, fmt.Errorf("unparsable server_version_num %q", truncate(raw, 200))
	}
	return num / 10000, nil
}

// ComparePostgres compares the PostgreSQL major version of both sites.
// A mismatch (or an unreadable version) is FAIL.
func ComparePostgres(ctx context.Context, primary, secondary Querier) Result {
	p, err := PostgresMajor(ctx, primary)
	if err != nil {
		return fail(CheckPostgres, "primary: %v", err)
	}
	s, err := PostgresMajor(ctx, secondary)
	if err != nil {
		return fail(CheckPostgres, "secondary: %v", err)
	}
	if p != s {
		return fail(CheckPostgres, "PostgreSQL major version mismatch: primary %d, secondary %d (physical replication requires the same major)", p, s)
	}
	return Result{CheckPostgres, StatusOK, fmt.Sprintf("PostgreSQL major version matches: primary %d, secondary %d", p, s)}
}

// FetchGitLabVersion reads and parses the installed GitLab version on host.
func FetchGitLabVersion(ctx context.Context, host string, runner sshexec.Runner) (GitLabVersion, error) {
	if err := sshexec.CheckHost(host); err != nil {
		return GitLabVersion{}, err
	}
	out, err := runner.CombinedOutput(ctx, host, gitlabVersionCmd)
	if err != nil {
		return GitLabVersion{}, fmt.Errorf("ssh %s: %w (output: %q)", host, err, truncate(string(out), 200))
	}
	return ParseGitLabVersion(string(out))
}

// CompareGitLab compares GitLab versions of both sites over SSH. A
// different major.minor (or unreadable version) is FAIL; only a different
// patch level is WARN.
func CompareGitLab(ctx context.Context, primarySSH, secondarySSH string, runner sshexec.Runner) Result {
	p, err := FetchGitLabVersion(ctx, primarySSH, runner)
	if err != nil {
		return fail(CheckGitLab, "primary: %v", err)
	}
	s, err := FetchGitLabVersion(ctx, secondarySSH, runner)
	if err != nil {
		return fail(CheckGitLab, "secondary: %v", err)
	}
	switch {
	case p.Major != s.Major || p.Minor != s.Minor:
		return fail(CheckGitLab, "GitLab version mismatch: primary %s, secondary %s (major.minor must match)", p, s)
	case p.Edition != "" && s.Edition != "" && p.Edition != s.Edition:
		return fail(CheckGitLab, "GitLab edition mismatch: primary %s, secondary %s", p, s)
	case (p.Edition == "") != (s.Edition == ""):
		return Result{CheckGitLab, StatusWarn, fmt.Sprintf("GitLab edition could not be compared: primary %s, secondary %s", p, s)}
	case p.Patch != s.Patch:
		return Result{CheckGitLab, StatusWarn, fmt.Sprintf("GitLab patch version differs: primary %s, secondary %s", p, s)}
	}
	return Result{CheckGitLab, StatusOK, fmt.Sprintf("GitLab version matches: primary %s, secondary %s", p, s)}
}

// Report is the set of results of a full parity check.
type Report struct {
	Results []Result
}

// Worst returns the most severe status in the report (OK when empty).
func (r Report) Worst() Status {
	w := StatusOK
	for _, res := range r.Results {
		switch res.Status {
		case StatusFail:
			return StatusFail
		case StatusWarn:
			w = StatusWarn
		}
	}
	return w
}

// Sites bundles what Run needs to reach both sites.
type Sites struct {
	PrimarySSH, SecondarySSH string
	PrimaryDB, SecondaryDB   Querier
	Runner                   sshexec.Runner
}

// Run performs the PostgreSQL and GitLab comparisons.
func Run(ctx context.Context, s Sites) Report {
	if s.PrimaryDB == nil || s.SecondaryDB == nil || s.Runner == nil {
		return Report{Results: []Result{
			fail(CheckPostgres, "version parity not run: missing database connection or SSH runner"),
			fail(CheckGitLab, "version parity not run: missing database connection or SSH runner"),
		}}
	}
	return Report{Results: []Result{
		ComparePostgres(ctx, s.PrimaryDB, s.SecondaryDB),
		CompareGitLab(ctx, s.PrimarySSH, s.SecondarySSH, s.Runner),
	}}
}

func fail(check, format string, args ...any) Result {
	return Result{check, StatusFail, fmt.Sprintf(format, args...)}
}
