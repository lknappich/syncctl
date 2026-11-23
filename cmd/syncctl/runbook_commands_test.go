package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runbook commands get pasted into a shell mid-incident, so every one the
// CLI prints must parse against the real command tree. Checking against
// newRootCmd rather than a list of expected flags keeps the two from
// drifting apart.
func TestPrintedRunbookCommandsParseAgainstTheCLI(t *testing.T) {
	for _, tc := range []struct {
		name        string
		secondaries []string
		sshHost     string
	}{
		{"one secondary", []string{"s1"}, "p:22"},
		{"several secondaries", []string{"s1", "s2", "s3"}, "p:22"},
		{"no primary ssh_host", []string{"s1", "s2"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeRunbookCommandsConfig(t, tc.secondaries, tc.sshHost)
			promoted := tc.secondaries[len(tc.secondaries)-1]

			var printed []string
			for _, args := range [][]string{
				{"--config", cfgPath, "runbook"},
				{"--config", cfgPath, "--dry-run", "failover", "--secondary", promoted},
			} {
				out := captureRunbookStdout(t, func() error {
					root := newRootCmd()
					root.SetArgs(args)
					return root.Execute()
				})
				printed = append(printed, printedSyncctlCommands(out)...)
			}

			seen := map[string]bool{}
			for _, line := range printed {
				seen[checkPrintedCommandParses(t, line)] = true
			}
			for _, want := range []string{"failover", "adopt-as-secondary"} {
				if !seen[want] {
					t.Errorf("expected the runbooks to print a `syncctl %s` command; found %q", want, printed)
				}
			}
		})
	}
}

// printedSyncctlCommands returns every "syncctl <subcommand> ..." in out,
// up to a closing backtick or the end of the line. "syncctl" followed by
// a word that is not a subcommand is prose, not a command.
func printedSyncctlCommands(out string) []string {
	subcommands := map[string]bool{}
	for _, c := range newRootCmd().Commands() {
		subcommands[c.Name()] = true
	}
	var cmds []string
	for _, line := range strings.Split(out, "\n") {
		for {
			i := strings.Index(line, "syncctl ")
			if i < 0 {
				break
			}
			line = line[i+len("syncctl "):]
			cmd, _, _ := strings.Cut(line, "`")
			if f := strings.Fields(cmd); len(f) > 0 && subcommands[f[0]] {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// checkPrintedCommandParses reports an error unless line names a runnable
// subcommand whose flags all exist and leaves no stray arguments. It
// returns the subcommand's name.
func checkPrintedCommandParses(t *testing.T, line string) string {
	t.Helper()
	root := newRootCmd()
	cmd, rest, err := root.Find(strings.Fields(line))
	if err != nil || cmd == root || !cmd.Runnable() {
		t.Errorf("printed command `syncctl %s` is not a runnable subcommand (err=%v)", line, err)
		return ""
	}
	if err := cmd.ParseFlags(rest); err != nil {
		t.Errorf("printed command `syncctl %s` does not parse: %v", line, err)
		return cmd.Name()
	}
	if extra := cmd.Flags().Args(); len(extra) > 0 {
		t.Errorf("printed command `syncctl %s` has stray arguments %q", line, extra)
	}
	return cmd.Name()
}

func captureRunbookStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	runErr := fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("command failed: %v\noutput:\n%s", runErr, out)
	}
	return out
}

func writeRunbookCommandsConfig(t *testing.T, secondaries []string, primarySSH string) string {
	t.Helper()
	t.Setenv("RB_PG_PASSWORD", "pgpass")
	t.Setenv("RB_REPL_PASSWORD", "replpass")
	var b strings.Builder
	b.WriteString(`
primary:
  name: p
  external_url: https://p.example.com
  postgres:
    host: h
    port: 5432
    db: d
    user: u
    password: ${RB_PG_PASSWORD}
    replication_user: ru
    replication_password: ${RB_REPL_PASSWORD}
    sslmode: require
  git:
    mode: rsync
    repos_path: /repos
    hashed_storage: true
`)
	if primarySSH != "" {
		fmt.Fprintf(&b, "  ssh_host: %s\n", primarySSH)
	}
	b.WriteString("secondaries:\n")
	for i, name := range secondaries {
		fmt.Fprintf(&b, `  - name: %[1]s
    external_url: https://%[1]s.example.com
    postgres:
      host: h%[2]d
      port: 5432
      db: d
      user: u
      password: ${RB_PG_PASSWORD}
      replication_user: ru
      replication_password: ${RB_REPL_PASSWORD}
      sslmode: require
    git:
      mode: rsync
      repos_path: /repos
      hashed_storage: true
    ssh_host: %[1]s:22
`, name, i)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
