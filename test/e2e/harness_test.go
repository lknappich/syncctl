//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	primary   = "gl-a"
	secondary = "gl-b"

	projectName = "syncctl-e2e"

	// Passwords are for throwaway containers that exist only for the test.
	controlPassword     = "e2e-control-pw-1"
	replicationPassword = "e2e-repl-pw-1"
	replicationUser     = "gitlab_repl"
	slotName            = "e2e_slot"

	psqlBin        = "/opt/gitlab/embedded/bin/psql"
	pgSocketDir    = "/var/opt/gitlab/postgresql"
	pgDataDir      = "/var/opt/gitlab/postgresql/data"
	reposDir       = "/var/opt/gitlab/git-data/repositories"
	embeddedBinDir = "/opt/gitlab/embedded/bin"
)

func composeFile() string {
	p, err := filepath.Abs("docker-compose.yml")
	if err != nil {
		panic(err)
	}
	return p
}

// run executes a command on the CI host and returns combined output.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func mustRun(t *testing.T, ctx context.Context, name string, args ...string) string {
	t.Helper()
	out, err := run(ctx, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// dexec runs a command inside a container.
func dexec(ctx context.Context, container, user string, env map[string]string, argv ...string) (string, error) {
	args := []string{"exec"}
	if user != "" {
		args = append(args, "-u", user)
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, container)
	args = append(args, argv...)
	return run(ctx, "docker", args...)
}

func mustExec(t *testing.T, ctx context.Context, container, user string, argv ...string) string {
	t.Helper()
	out, err := dexec(ctx, container, user, nil, argv...)
	if err != nil {
		t.Fatalf("exec in %s: %s: %v\n%s", container, strings.Join(argv, " "), err, out)
	}
	return out
}

// psql runs one statement against a site's Postgres over the local socket as
// the gitlab-psql OS user and returns unaligned, tuples-only output.
func psql(ctx context.Context, container, sql string) (string, error) {
	out, err := dexec(ctx, container, "gitlab-psql", nil,
		psqlBin, "-h", pgSocketDir, "-d", "gitlabhq_production", "-v", "ON_ERROR_STOP=1", "-Atc", sql)
	return strings.TrimSpace(out), err
}

// poll retries fn until it reports done or the timeout expires. It never
// sleeps for a fixed period: the interval only paces retries.
func poll(t *testing.T, timeout, interval time.Duration, what string, fn func() (done bool, detail string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var detail string
	for {
		var done bool
		done, detail = fn()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s; last state: %s", timeout, what, detail)
		}
		time.Sleep(interval)
	}
}

func syncctl(ctx context.Context, user string, args ...string) (string, error) {
	argv := append([]string{"/usr/local/bin/syncctl", "-c", "/etc/syncctl-e2e.yaml", "--log-format", "text"}, args...)
	return dexec(ctx, secondary, user, map[string]string{
		"PATH":             embeddedBinDir + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"PG_CTRL_PASSWORD": controlPassword,
		"PG_REPL_PASSWORD": replicationPassword,
		"HOME":             homeFor(user),
	}, argv...)
}

func homeFor(user string) string {
	switch user {
	case "gitlab-psql":
		return pgSocketDir
	case "syncctl":
		return "/home/syncctl"
	}
	return "/root"
}

// requireEnv reports whether a missing prerequisite must fail the run instead
// of skipping it, so CI cannot go green without having tested anything.
func requireEnv() bool {
	return os.Getenv("CI") == "true" || os.Getenv("E2E_REQUIRE") == "1"
}

func skipOrFail(t *testing.T, msg string) {
	t.Helper()
	if requireEnv() {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// knownFailures maps a subtest name to the roadmap reference explaining why
// it is expected to fail today.
func knownFailures(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("known_failures.txt")
	if err != nil {
		t.Fatalf("known_failures.txt: %v", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, _ := strings.Cut(line, " ")
		out[name] = strings.TrimSpace(reason)
	}
	return out
}

// skipIfKnown skips the calling subtest when it is listed in
// known_failures.txt. Set E2E_RUN_KNOWN_FAILURES=1 to run those anyway, for
// example to confirm that a fix made one pass.
func skipIfKnown(t *testing.T, known map[string]string) {
	t.Helper()
	if os.Getenv("E2E_RUN_KNOWN_FAILURES") == "1" {
		return
	}
	name := strings.TrimPrefix(t.Name(), "TestE2E/")
	if reason, ok := known[name]; ok {
		t.Skipf("known failure: %s", reason)
	}
}

func dumpLogs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, _ := run(ctx, "docker", "compose", "-p", "syncctl-e2e", "-f", composeFile(), "logs", "--no-color", "--tail", "150")
	t.Logf("container logs:\n%s", out)
	for _, c := range []string{primary, secondary} {
		out, _ := dexec(ctx, c, "", nil, "gitlab-ctl", "status")
		t.Logf("%s gitlab-ctl status:\n%s", c, out)
	}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func syncctlConfig() string {
	site := func(indent, name, host, sshHost string) string {
		lines := []string{
			"name: " + name,
			"external_url: http://" + name,
			"postgres:",
			"  host: " + host,
			"  port: 5432",
			"  db: gitlabhq_production",
			"  user: gitlab",
			"  password: ${PG_CTRL_PASSWORD}",
			"  replication_user: " + replicationUser,
			"  replication_password: ${PG_REPL_PASSWORD}",
			"  slot_name: " + slotName,
			"  sslmode: disable",
			"git:",
			"  mode: rsync",
			"  repos_path: " + reposDir,
			"  hashed_storage: true",
			"object_storage:",
			"  backend: fs",
			"ssh_host: " + sshHost,
		}
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(indent + l + "\n")
		}
		return b.String()
	}
	secondaryBlock := site("    ", secondary, "127.0.0.1", "syncctl@127.0.0.1:22")
	secondaryBlock = "  -" + secondaryBlock[3:]
	return "primary:\n" + site("  ", primary, primary, "syncctl@"+primary+":22") +
		"secondaries:\n" + secondaryBlock +
		`ssh:
  strict_host_key_checking: "no"
metrics:
  addr: 127.0.0.1:19101
log:
  level: info
  format: text
`
}
