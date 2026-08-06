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
	err := Run(context.Background(), Options{
		PrimaryDSN: "host=h user=u",
		DataDir:    dir,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("dry-run Run: %v", err)
	}
	// Dry run should not create the data dir.
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create files, but PG_VERSION exists")
	}
}

func TestRunRejectsMissingDataDir(t *testing.T) {
	err := Run(context.Background(), Options{
		PrimaryDSN: "host=h",
		DataDir:    "",
		DryRun:     true,
	})
	if err == nil {
		t.Fatal("expected error for missing data_dir")
	}
}

func TestRunRejectsMissingPrimaryDSN(t *testing.T) {
	err := Run(context.Background(), Options{
		PrimaryDSN: "",
		DataDir:    t.TempDir(),
		DryRun:     true,
	})
	if err == nil {
		t.Fatal("expected error for missing primary_dsn")
	}
}

func TestAppendConnInfoAppnameCreatesLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postgresql.auto.conf")
	if err := os.WriteFile(path, []byte("# auto conf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendConnInfoAppname(dir, "secondary-1"); err != nil {
		t.Fatalf("appendConnInfoAppname: %v", err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "application_name=secondary-1") {
		t.Errorf("expected application_name=secondary-1 in:\n%s", content)
	}
	if !strings.HasSuffix(string(content), "\n") {
		t.Errorf("expected trailing newline in:\n%s", content)
	}
}

func TestAppendConnInfoAppnameUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postgresql.auto.conf")
	initial := "primary_conninfo = 'host=10.0.0.1 user=repl password=secret'\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendConnInfoAppname(dir, "secondary-1"); err != nil {
		t.Fatalf("appendConnInfoAppname: %v", err)
	}
	content, _ := os.ReadFile(path)
	s := string(content)
	if !strings.Contains(s, "application_name=secondary-1") {
		t.Errorf("expected application_name in:\n%s", s)
	}
	if strings.Count(s, "primary_conninfo") != 1 {
		t.Errorf("expected exactly one primary_conninfo line in:\n%s", s)
	}
}

func TestAppendConnInfoAppnameNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postgresql.auto.conf")
	initial := "primary_conninfo = 'host=h user=u password=p'"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendConnInfoAppname(dir, "sec"); err != nil {
		t.Fatalf("appendConnInfoAppname: %v", err)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(content), "\n") {
		t.Errorf("expected trailing newline in:\n%s", content)
	}
}

func TestAppendConnInfoAppnameEmptyAppName(t *testing.T) {
	dir := t.TempDir()
	if err := appendConnInfoAppname(dir, ""); err != nil {
		t.Errorf("expected nil error for empty appName, got %v", err)
	}
}

func TestRunDryRunNoDuplicateSlotFlag(t *testing.T) {
	dir := t.TempDir()
	err := Run(context.Background(), Options{
		PrimaryDSN: "host=h user=u",
		DataDir:    dir,
		SlotName:   "my_slot",
		DryRun:     true,
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

func TestRunSuccess(t *testing.T) {
	orig := runBasebackup
	runBasebackup = func(ctx context.Context, opts Options) basebackupRunner {
		// Simulate pg_basebackup -R writing postgresql.auto.conf.
		_ = os.WriteFile(filepath.Join(opts.DataDir, "postgresql.auto.conf"),
			[]byte("primary_conninfo = 'host=h'\n"), 0o600)
		return &stubBasebackup{}
	}
	defer func() { runBasebackup = orig }()

	dir := t.TempDir()
	err := Run(context.Background(), Options{
		PrimaryDSN: "host=h user=u",
		DataDir:    dir,
		SlotName:   "myslot",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
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
		PrimaryDSN: "host=h",
		DataDir:    dir,
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
		PrimaryDSN: "host=h",
		DataDir:    dir,
		SlotName:   "myslot",
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

func TestInjectAppNameNoQuote(t *testing.T) {
	line := "primary_conninfo = host=h"
	got := injectAppName(line, "myapp")
	if !strings.Contains(got, "application_name=myapp") {
		t.Errorf("got %q", got)
	}
}

func TestInjectAppNameWithQuote(t *testing.T) {
	line := "primary_conninfo = 'host=h'"
	got := injectAppName(line, "myapp")
	if !strings.Contains(got, "application_name=myapp") {
		t.Errorf("got %q", got)
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
			PrimaryDSN: "host=h user=repl password=leaked",
			Password:   "s3cr3t",
			DataDir:    t.TempDir(),
			DryRun:     true,
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
