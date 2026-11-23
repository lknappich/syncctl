package pgsetup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDataDirRejectsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := checkDataDir(dir)
	if err == nil {
		t.Fatal("expected error for non-empty data dir")
	}
}

func TestCheckDataDirAcceptsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := checkDataDir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckDataDirAcceptsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := checkDataDir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDryRunPrintsOnly(t *testing.T) {
	dir := t.TempDir()
	out := captureStdout(t, func() {
		err := Run(context.Background(), Options{
			PrimaryDSN:      "host=h user=u",
			DataDir:         dir,
			ApplicationName: "gl-b",
			DryRun:          true,
		})
		if err != nil {
			t.Fatalf("dry-run Run: %v", err)
		}
	})
	// Dry run should not create the data dir.
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create files, but PG_VERSION exists")
	}
	if !strings.Contains(out, "[dry-run] set application_name=gl-b in primary_conninfo") {
		t.Errorf("dry-run should report the application_name it would set, got: %s", out)
	}
}

func TestRunRejectsMissingDataDir(t *testing.T) {
	err := Run(context.Background(), Options{
		PrimaryDSN:      "host=h",
		DataDir:         "",
		ApplicationName: "gl-b",
		DryRun:          true,
	})
	if err == nil {
		t.Fatal("expected error for missing data_dir")
	}
}

func TestRunRejectsMissingPrimaryDSN(t *testing.T) {
	err := Run(context.Background(), Options{
		PrimaryDSN:      "",
		DataDir:         t.TempDir(),
		ApplicationName: "gl-b",
		DryRun:          true,
	})
	if err == nil {
		t.Fatal("expected error for missing primary_dsn")
	}
}

func TestRunRejectsMissingApplicationName(t *testing.T) {
	err := Run(context.Background(), Options{
		PrimaryDSN: "host=h",
		DataDir:    t.TempDir(),
		DryRun:     true,
	})
	if err == nil || !strings.Contains(err.Error(), "application_name") {
		t.Fatalf("expected an application_name error, got %v", err)
	}
}

func TestSetConnInfoAppNameRequiresPrimaryConnInfo(t *testing.T) {
	in := "# auto conf\n# primary_conninfo = 'host=h'\n"
	dir, path := writeAutoConf(t, in)
	err := setConnInfoAppName(dir, "gl-b")
	if err == nil || !strings.Contains(err.Error(), "no primary_conninfo") {
		t.Fatalf("expected a missing primary_conninfo error, got %v", err)
	}
	if got := readFile(t, path); got != in {
		t.Errorf("file changed on error:\n%s", got)
	}
}

func TestSetConnInfoAppNameNoTrailingNewline(t *testing.T) {
	dir, path := writeAutoConf(t, "primary_conninfo = 'host=h user=u'")
	if err := setConnInfoAppName(dir, "sec"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	if got, want := readFile(t, path), "primary_conninfo = 'host=h user=u application_name=sec'\n"; got != want {
		t.Errorf("postgresql.auto.conf = %q, want %q", got, want)
	}
}

func TestRunDryRunNoDuplicateSlotFlag(t *testing.T) {
	dir := t.TempDir()
	err := Run(context.Background(), Options{
		PrimaryDSN:      "host=h user=u",
		DataDir:         dir,
		SlotName:        "my_slot",
		ApplicationName: "gl-b",
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
}

// stubBasebackup is a test basebackupRunner.
type stubBasebackup struct {
	err error
}

func (s *stubBasebackup) Run() error { return s.err }

// After a standard bootstrap the standby must report the secondary's site
// name, not the DSN's "syncctl" or the slot name, or the lag query never
// finds its pg_stat_replication row.
func TestRunSetsApplicationNameToSiteName(t *testing.T) {
	orig := runBasebackup
	runBasebackup = func(ctx context.Context, opts Options) basebackupRunner {
		// Simulate pg_basebackup -R writing postgresql.auto.conf.
		_ = os.WriteFile(filepath.Join(opts.DataDir, "postgresql.auto.conf"),
			[]byte(basebackupAutoConf), 0o600)
		return &stubBasebackup{}
	}
	defer func() { runBasebackup = orig }()

	dir := t.TempDir()
	captureStdout(t, func() {
		err := Run(context.Background(), Options{
			PrimaryDSN:      "host=h user=u application_name=syncctl",
			DataDir:         dir,
			SlotName:        "gitlab_syncctl",
			ApplicationName: "gl-b",
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	got := readFile(t, filepath.Join(dir, "postgresql.auto.conf"))
	want := strings.Replace(basebackupAutoConf, "application_name=syncctl", "application_name=''gl-b''", 1)
	if got != want {
		t.Errorf("postgresql.auto.conf =\n%s\nwant\n%s", got, want)
	}
}

func TestRunBasebackupError(t *testing.T) {
	orig := runBasebackup
	runBasebackup = func(ctx context.Context, opts Options) basebackupRunner {
		return &stubBasebackup{err: errors.New("basebackup failed")}
	}
	defer func() { runBasebackup = orig }()

	dir := t.TempDir()
	err := Run(context.Background(), Options{
		PrimaryDSN:      "host=h",
		DataDir:         dir,
		ApplicationName: "gl-b",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pg_basebackup") {
		t.Errorf("err = %v", err)
	}
}

func TestRunBasebackupErrorWithSlot(t *testing.T) {
	orig := runBasebackup
	var capturedArgs []string
	_ = capturedArgs
	runBasebackup = func(ctx context.Context, opts Options) basebackupRunner {
		return &stubBasebackup{err: errors.New("slot exists")}
	}
	defer func() { runBasebackup = orig }()

	dir := t.TempDir()
	err := Run(context.Background(), Options{
		PrimaryDSN:      "host=h",
		DataDir:         dir,
		SlotName:        "myslot",
		ApplicationName: "gl-b",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildBasebackupArgs(t *testing.T) {
	args := buildBasebackupArgs(Options{
		PrimaryDSN: "host=h",
		DataDir:    "/data",
		SlotName:   "slot1",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-S slot1") {
		t.Errorf("args should include -S slot1: %s", joined)
	}
	if !strings.Contains(joined, "--create-slot") {
		t.Errorf("args should include --create-slot: %s", joined)
	}
}

func TestBuildBasebackupArgsNoSlot(t *testing.T) {
	args := buildBasebackupArgs(Options{
		PrimaryDSN: "host=h",
		DataDir:    "/data",
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-S") {
		t.Errorf("args should not include -S when no slot: %s", joined)
	}
}

func TestBuildBasebackupArgsCarriesNoPassword(t *testing.T) {
	args := buildBasebackupArgs(Options{
		PrimaryDSN: "host=h port=5432 user=repl dbname=replication sslmode=require",
		Password:   "s3cr3t",
		DataDir:    "/data",
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "s3cr3t") {
		t.Errorf("password leaked onto the command line: %s", joined)
	}
	if strings.Contains(joined, "password=") {
		t.Errorf("password field must not appear in argv: %s", joined)
	}
}

func TestDryRunDoesNotPrintPassword(t *testing.T) {
	out := captureStdout(t, func() {
		err := Run(context.Background(), Options{
			PrimaryDSN:      "host=h user=repl password=leaked",
			Password:        "s3cr3t",
			DataDir:         t.TempDir(),
			ApplicationName: "gl-b",
			DryRun:          true,
		})
		if err != nil {
			t.Fatalf("dry-run Run: %v", err)
		}
	})
	for _, secret := range []string{"s3cr3t", "leaked"} {
		if strings.Contains(out, secret) {
			t.Errorf("dry-run output leaked %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "password=***") {
		t.Errorf("expected a redacted password field, got: %s", out)
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct{ in, want string }{
		{"host=h password=plain dbname=d", "host=h password=*** dbname=d"},
		{"host=h password='with space' dbname=d", "host=h password=*** dbname=d"},
		{`host=h password='it\'s' dbname=d`, "host=h password=*** dbname=d"},
		{"host=h password=''", "host=h password=***"},
		{"host=h dbname=d", "host=h dbname=d"},
	}
	for _, tc := range tests {
		if got := RedactDSN(tc.in); got != tc.want {
			t.Errorf("RedactDSN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	var sb strings.Builder
	if _, err := io.Copy(&sb, r); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}
func TestCheckDataDirRejectsUninspectableDir(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "pgdata")
	if err := os.WriteFile(target, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkDataDir(target); err == nil {
		t.Fatal("expected an error for a path that is not a directory")
	}
}

// basebackupAutoConf is postgresql.auto.conf as pg_basebackup -R (PG 16)
// writes it from syncctl's replication DSN: libpq values quoted by
// appendConnStrVal, then the whole conninfo escaped for the config file.
const basebackupAutoConf = `# Do not edit this file manually!
# It will be overwritten by the ALTER SYSTEM command.
primary_conninfo = 'user=gitlab_repl passfile=''/var/opt/gitlab/postgresql/.pgpass'' channel_binding=prefer host=10.0.0.10 port=5432 application_name=syncctl sslmode=require sslcompression=0 sslcertmode=allow sslsni=1 ssl_min_protocol_version=TLSv1.2 gssencmode=disable krbsrvname=postgres gssdelegation=0 target_session_attrs=any load_balance_hosts=disable'
primary_slot_name = 'gitlab_syncctl'
`

func writeAutoConf(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "postgresql.auto.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The lag query matches pg_stat_replication.application_name against the
// secondary's site name, so the DSN's application_name=syncctl must be
// replaced, not kept.
func TestSetConnInfoAppNameReplacesExisting(t *testing.T) {
	dir, path := writeAutoConf(t, basebackupAutoConf)
	if err := setConnInfoAppName(dir, "gl-b"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	want := strings.Replace(basebackupAutoConf, "application_name=syncctl", "application_name=''gl-b''", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("postgresql.auto.conf =\n%s\nwant\n%s", got, want)
	}
}

func TestSetConnInfoAppNameAddsMissing(t *testing.T) {
	in := "primary_conninfo = 'user=gitlab_repl passfile=''/var/opt/gitlab/postgresql/.pgpass'' host=10.0.0.10 port=5432 sslmode=require'\n"
	dir, path := writeAutoConf(t, in)
	if err := setConnInfoAppName(dir, "gl-b"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	want := "primary_conninfo = 'user=gitlab_repl passfile=''/var/opt/gitlab/postgresql/.pgpass'' host=10.0.0.10 port=5432 sslmode=require application_name=''gl-b'''\n"
	if got := readFile(t, path); got != want {
		t.Errorf("postgresql.auto.conf =\n%s\nwant\n%s", got, want)
	}
}

// A base backup of a primary that was once a standby carries its old
// primary_conninfo; -R appends the new one after it, and the last
// assignment in the file is the one Postgres uses.
func TestSetConnInfoAppNameRewritesEffectiveLine(t *testing.T) {
	stale := "primary_conninfo = 'host=10.9.9.9 application_name=old-site'\n"
	dir, path := writeAutoConf(t, stale+basebackupAutoConf)
	if err := setConnInfoAppName(dir, "gl-b"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	want := stale + strings.Replace(basebackupAutoConf, "application_name=syncctl", "application_name=''gl-b''", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("postgresql.auto.conf =\n%s\nwant\n%s", got, want)
	}
}

// libpq lets the last occurrence of a key win, so every application_name
// must go, not just the first.
func TestSetConnInfoAppNameReplacesDuplicates(t *testing.T) {
	dir, path := writeAutoConf(t, "primary_conninfo = 'host = h  application_name=a port=5432 application_name = ''b'''\n")
	if err := setConnInfoAppName(dir, "gl-b"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	want := "primary_conninfo = 'host=h application_name=''gl-b'' port=5432'\n"
	if got := readFile(t, path); got != want {
		t.Errorf("postgresql.auto.conf = %q, want %q", got, want)
	}
}

// pg_basebackup escapes ' and \ twice: once inside the libpq value, once
// for the config file. Both layers must survive the rewrite.
func TestSetConnInfoAppNamePreservesEscapedValues(t *testing.T) {
	in := `primary_conninfo = 'user=repl password=''it\\''s a\\\\b'' passfile=''/p/.pgpass'' host=h application_name=syncctl'  # set by -R` + "\n"
	dir, path := writeAutoConf(t, in)
	if err := setConnInfoAppName(dir, "gl-b"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}
	want := strings.Replace(in, "application_name=syncctl", "application_name=''gl-b''", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("postgresql.auto.conf =\n%s\nwant\n%s", got, want)
	}
}

func TestSetConnInfoAppNameRejectsUnparseable(t *testing.T) {
	for _, in := range []string{
		"primary_conninfo = 'password=s3cr3t host'\n",
		"primary_conninfo = 'password=''s3cr3t host=h'\n",
		"primary_conninfo = 'postgresql://repl:s3cr3t@h/db'\n",
		"primary_conninfo = s3cr3t\n",
	} {
		dir, path := writeAutoConf(t, in)
		err := setConnInfoAppName(dir, "gl-b")
		if err == nil {
			t.Errorf("%q: expected an error", in)
			continue
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%q: error leaks a conninfo value: %v", in, err)
		}
		if got := readFile(t, path); got != in {
			t.Errorf("%q: file changed on error:\n%s", in, got)
		}
	}
}

func TestConfStringEscaping(t *testing.T) {
	tests := []struct {
		escaped, raw string
		decodeOnly   bool
	}{
		{escaped: `plain`, raw: "plain"},
		{escaped: `it''s`, raw: "it's"},
		{escaped: `a\\b`, raw: `a\b`},
		{escaped: `line\nbreak`, raw: "line\nbreak"},
		{escaped: `\'x`, raw: "'x", decodeOnly: true},
		{escaped: `tab\there`, raw: "tab\there", decodeOnly: true},
		{escaped: `\101\60x`, raw: "A0x", decodeOnly: true},
	}
	for _, tc := range tests {
		if got := unescapeConfString(tc.escaped); got != tc.raw {
			t.Errorf("unescapeConfString(%q) = %q, want %q", tc.escaped, got, tc.raw)
		}
		if tc.decodeOnly {
			continue
		}
		if got := escapeConfString(tc.raw); got != tc.escaped {
			t.Errorf("escapeConfString(%q) = %q, want %q", tc.raw, got, tc.escaped)
		}
	}
}
