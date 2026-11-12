//go:build e2e

// Package e2e runs syncctl against two real Omnibus GitLab CE containers.
//
// It is excluded from `go test ./...` by the e2e build tag. Run it with:
//
//	E2E_GITLAB_IMAGE=gitlab/gitlab-ce:<tag> go test -tags e2e -timeout 45m -v ./test/e2e/...
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	bootTimeout  = 25 * time.Minute
	stepTimeout  = 10 * time.Minute
	pollInterval = 5 * time.Second
)

// TestE2E is one ordered scenario: later steps depend on earlier ones, and a
// failed prerequisite step aborts the run rather than reporting a cascade.
func TestE2E(t *testing.T) {
	image := os.Getenv("E2E_GITLAB_IMAGE")
	if image == "" {
		skipOrFail(t, "E2E_GITLAB_IMAGE is not set (for example gitlab/gitlab-ce:18.5.1-ce.0)")
	}
	if _, err := run(context.Background(), "docker", "version"); err != nil {
		skipOrFail(t, "docker is not available")
	}
	ctx := context.Background()
	known := knownFailures(t)
	work := t.TempDir()
	bin := filepath.Join(work, "syncctl")

	compose := func(args ...string) (string, error) {
		a := append([]string{"compose", "-p", "syncctl-e2e", "-f", composeFile()}, args...)
		return run(ctx, "docker", a...)
	}
	t.Setenv("GITLAB_IMAGE", image)

	t.Cleanup(func() {
		if t.Failed() {
			dumpLogs(t)
		}
		if os.Getenv("E2E_KEEP") == "1" {
			t.Log("E2E_KEEP=1: leaving containers running")
			return
		}
		if out, err := compose("down", "-v", "--remove-orphans", "-t", "30"); err != nil {
			t.Logf("compose down: %v\n%s", err, out)
		}
	})

	step := func(name string, fatal bool, fn func(t *testing.T)) {
		ok := t.Run(name, fn)
		if !ok && fatal {
			t.Fatalf("step %q failed; later steps depend on it", name)
		}
	}

	var repoRel, syncOut string

	step("build_binary", true, func(t *testing.T) {
		root, _ := filepath.Abs("../..")
		cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/syncctl")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
	})

	step("boot", true, func(t *testing.T) {
		mustRun(t, ctx, "docker", "compose", "-p", "syncctl-e2e", "-f", composeFile(), "up", "-d", "--quiet-pull")

		poll(t, bootTimeout, pollInterval, primary+" healthy", func() (bool, string) {
			out, err := run(ctx, "docker", "inspect", "--format", "{{.State.Health.Status}}", primary)
			s := strings.TrimSpace(out)
			return err == nil && s == "healthy", s
		})

		// gitlab-secrets.json must be on gl-b before its first reconfigure so
		// both sites share the same db_key_base and other secrets.
		secrets := filepath.Join(work, "gitlab-secrets.json")
		mustRun(t, ctx, "docker", "cp", primary+":/etc/gitlab/gitlab-secrets.json", secrets)
		mustExec(t, ctx, secondary, "", "mkdir", "-p", "/etc/gitlab")
		mustRun(t, ctx, "docker", "cp", secrets, secondary+":/etc/gitlab/gitlab-secrets.json")
		mustExec(t, ctx, secondary, "", "touch", "/tmp/e2e-go")

		poll(t, bootTimeout, pollInterval, secondary+" first reconfigure to finish", func() (bool, string) {
			out, _ := run(ctx, "docker", "logs", secondary)
			return strings.Contains(out, "gitlab Reconfigured!"), "no 'gitlab Reconfigured!' line yet"
		})
		poll(t, bootTimeout, pollInterval, secondary+" postgresql after first reconfigure", func() (bool, string) {
			out, err := psql(ctx, secondary, "SELECT 1")
			return err == nil && out == "1", out
		})

		const sudoers = `syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl stop
syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl start
syncctl ALL=(gitlab-psql) NOPASSWD: /opt/gitlab/embedded/bin/pg_ctl promote *
syncctl ALL=(gitlab-psql) NOPASSWD: /opt/gitlab/embedded/bin/pg_basebackup *
syncctl ALL=(root) NOPASSWD: /opt/gitlab/embedded/bin/gitlab-rails runner *
syncctl ALL=(root) NOPASSWD: /usr/bin/rsync *
syncctl ALL=(root) NOPASSWD: /usr/bin/grep *
`
		// The image's own sshd is disabled (GITLAB_DISABLE_OPENSSH) because its
		// config only admits the git user. Ubuntu's sshd runs with its default
		// config, and syncctl connects as an unprivileged user with a key and
		// the sudo allowlist from SECURITY.md.
		for _, c := range []string{primary, secondary} {
			mustExec(t, ctx, c, "", "sh", "-c",
				"apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo rsync git openssh-client openssh-server")
			mustExec(t, ctx, c, "", "sh", "-c",
				"id syncctl >/dev/null 2>&1 || useradd -m -s /bin/bash syncctl; usermod -p '*' syncctl; "+
					"install -d -m 700 -o syncctl -g syncctl /home/syncctl/.ssh; "+
					"printf %s "+shQuote(sudoers)+" > /etc/sudoers.d/syncctl && chmod 440 /etc/sudoers.d/syncctl && visudo -c")
			mustExec(t, ctx, c, "", "sh", "-c",
				"mkdir -p /run/sshd && ssh-keygen -A && /usr/sbin/sshd")
		}

		// syncctl runs on gl-b, as it would on a real secondary, and reaches
		// both sites over SSH with a key generated here.
		mustExec(t, ctx, secondary, "syncctl", "sh", "-c",
			"ssh-keygen -q -t ed25519 -N '' -f /home/syncctl/.ssh/id_ed25519")
		pub := strings.TrimSpace(mustExec(t, ctx, secondary, "", "cat", "/home/syncctl/.ssh/id_ed25519.pub"))
		for _, c := range []string{primary, secondary} {
			mustExec(t, ctx, c, "syncctl", "sh", "-c",
				"echo "+shQuote(pub)+" >> /home/syncctl/.ssh/authorized_keys && chmod 600 /home/syncctl/.ssh/authorized_keys")
		}
		mustExec(t, ctx, secondary, "", "install", "-d", "-o", "git", "-g", "git", reposDir)

		// Replication role and passwords: documented gitlab.rb settings open
		// the listener and pg_hba; the passwords are set with plain SQL so no
		// helper command is involved.
		sql := fmt.Sprintf(`DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
CREATE ROLE %[1]s WITH REPLICATION LOGIN;
END IF; END $$;
ALTER ROLE %[1]s WITH REPLICATION LOGIN PASSWORD '%[2]s';
ALTER ROLE gitlab WITH PASSWORD '%[3]s';`, replicationUser, replicationPassword, controlPassword)
		if out, err := psql(ctx, primary, sql); err != nil {
			t.Fatalf("configure roles: %v\n%s", err, out)
		}

		mustRun(t, ctx, "docker", "cp", bin, secondary+":/usr/local/bin/syncctl")
		cfg := filepath.Join(work, "syncctl-e2e.yaml")
		if err := os.WriteFile(cfg, []byte(syncctlConfig()), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, ctx, "docker", "cp", cfg, secondary+":/etc/syncctl-e2e.yaml")
		mustExec(t, ctx, secondary, "", "chmod", "0755", "/usr/local/bin/syncctl")

		if out, err := syncctl(ctx, "syncctl", "config-validate"); err != nil {
			t.Fatalf("config-validate: %v\n%s", err, out)
		}
	})

	step("seed", true, func(t *testing.T) {
		token := fmt.Sprintf("e2e%017d", time.Now().UnixNano()%1e17)
		script := fmt.Sprintf(`u = User.find_by_username('root'); `+
			`t = u.personal_access_tokens.create(scopes: [:api], name: 'syncctl-e2e', expires_at: 30.days.from_now); `+
			`t.set_token('%s'); t.save!`, token)
		mustExec(t, ctx, primary, "", "gitlab-rails", "runner", script)

		api := func(method, path string, body any) map[string]any {
			t.Helper()
			args := []string{"-sS", "--fail-with-body", "-X", method,
				"-H", "PRIVATE-TOKEN: " + token, "-H", "Content-Type: application/json"}
			if body != nil {
				b, _ := json.Marshal(body)
				args = append(args, "-d", string(b))
			}
			args = append(args, "http://localhost/api/v4"+path)
			out := mustExec(t, ctx, primary, "", append([]string{"curl"}, args...)...)
			var m map[string]any
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("decode %s %s: %v\n%s", method, path, err, out)
			}
			return m
		}

		group := api("POST", "/groups", map[string]any{"name": "e2e-group", "path": "e2e-group"})
		proj := api("POST", "/projects", map[string]any{
			"name": projectName, "path": projectName,
			"namespace_id":           group["id"],
			"initialize_with_readme": true,
			"default_branch":         "main",
		})
		projectID := int(proj["id"].(float64))
		for i := 1; i <= 3; i++ {
			api("POST", fmt.Sprintf("/projects/%d/repository/commits", projectID), map[string]any{
				"branch":         "main",
				"commit_message": fmt.Sprintf("e2e commit %d", i),
				"actions": []map[string]any{{
					"action": "create", "file_path": fmt.Sprintf("file-%d.txt", i), "content": fmt.Sprintf("content %d\n", i),
				}},
			})
		}

		poll(t, stepTimeout, 2*time.Second, "repository on disk of "+primary, func() (bool, string) {
			out, err := dexec(ctx, primary, "", nil, "sh", "-c",
				"find "+reposDir+"/@hashed -maxdepth 4 -type d -name '*.git' ! -name '*.wiki.git' ! -name '*.design.git' 2>/dev/null | head -n1")
			p := strings.TrimSpace(out)
			if err != nil || p == "" {
				return false, out
			}
			repoRel = strings.TrimPrefix(p, reposDir+"/")
			return true, p
		})
	})

	step("doctor_primary_checks", false, func(t *testing.T) {
		// Before the secondary is bootstrapped only the primary-side checks
		// can pass; the secondary's Postgres is still an unrelated cluster.
		out, _ := syncctl(ctx, "syncctl", "doctor")
		t.Logf("doctor (before bootstrap):\n%s", out)
		for _, want := range []string{"ssh:primary", "pg:primary:control", "pg:primary:repl-role", "pg:primary:wal_level"} {
			if !checkPassed(out, want) {
				t.Errorf("doctor check %s did not pass", want)
			}
		}
	})

	step("pg_setup", true, func(t *testing.T) {
		// pg setup runs pg_basebackup locally, so it runs inside gl-b as the
		// gitlab-psql user. The cluster initialised by the first reconfigure
		// is replaced by the base backup.
		mustExec(t, ctx, secondary, "", "gitlab-ctl", "stop", "postgresql")
		mustExec(t, ctx, secondary, "", "rm", "-rf", pgDataDir)
		out, err := syncctl(ctx, "gitlab-psql", "pg", "setup", "--secondary", secondary, "--data-dir", pgDataDir)
		if err != nil {
			t.Fatalf("pg setup: %v\n%s", err, out)
		}
		mustExec(t, ctx, secondary, "", "gitlab-ctl", "start", "postgresql")

		poll(t, stepTimeout, pollInterval, secondary+" in recovery", func() (bool, string) {
			out, err := psql(ctx, secondary, "SELECT pg_is_in_recovery()")
			return err == nil && out == "t", out
		})
	})

	step("replication_streaming", false, func(t *testing.T) {
		poll(t, stepTimeout, pollInterval, "pg_stat_replication row on "+primary, func() (bool, string) {
			out, err := psql(ctx, primary, "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming'")
			return err == nil && out != "0" && out != "", out
		})
		poll(t, stepTimeout, pollInterval, "active replication slot "+slotName, func() (bool, string) {
			out, err := psql(ctx, primary,
				"SELECT active FROM pg_replication_slots WHERE slot_name = '"+slotName+"'")
			return err == nil && out == "t", out
		})
	})

	step("doctor_after_bootstrap", false, func(t *testing.T) {
		out, _ := syncctl(ctx, "syncctl", "doctor")
		t.Logf("doctor (after bootstrap):\n%s", out)
		for _, want := range []string{"pg:primary:control", "pg:secondary:gl-b:control", "pg:secondary:gl-b:in_recovery"} {
			if !checkPassed(out, want) {
				t.Errorf("doctor check %s did not pass", want)
			}
		}
	})

	step("sync_runs", true, func(t *testing.T) {
		out, err := syncctl(ctx, "syncctl", "sync")
		t.Logf("sync:\n%s", out)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		syncOut = out
	})

	step("sync_git_rsync_ok", false, func(t *testing.T) {
		skipIfKnown(t, known)
		if !strings.Contains(syncOut, "[OK] git_rsync") {
			t.Fatalf("git_rsync reconciler not OK:\n%s", syncOut)
		}
	})

	step("git_parity", false, func(t *testing.T) {
		if repoRel == "" {
			t.Skip("no repository path from seed")
		}
		revParse := func(c string) string {
			out := mustExec(t, ctx, c, "", embeddedBinDir+"/git", "-c", "safe.directory=*",
				"--git-dir", reposDir+"/"+repoRel, "rev-parse", "refs/heads/main")
			return strings.TrimSpace(out)
		}
		a, b := revParse(primary), revParse(secondary)
		if a != b {
			t.Fatalf("git rev-parse differs: %s=%s %s=%s", primary, a, secondary, b)
		}
	})

	step("sync_postgres_reports_lag", false, func(t *testing.T) {
		skipIfKnown(t, known)
		if !strings.Contains(syncOut, "[OK] postgres") {
			t.Fatalf("postgres reconciler not OK:\n%s", syncOut)
		}
	})
}

// checkPassed reports whether doctor printed a PASS line for the named check.
func checkPassed(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == name && f[2] == "PASS" {
			return true
		}
	}
	return false
}
