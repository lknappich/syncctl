// Package dbkey checks that the secondary's db_key_base matches the
// primary's. The db_key_base is the GitLab Rails secret used to encrypt
// webhook secrets, access tokens, 2FA seeds, etc. For a true 1:1 replica
// the secondary MUST share this key so the GitLab application itself
// (not our tool) can decrypt columns on the secondary.
//
// This package reads the key from each site's gitlab.rb config file via
// SSH. It never decrypts anything — it only compares the key bytes.
package dbkey

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lknappich/syncctl/internal/sshexec"
)

var dbKeyRe = regexp.MustCompile(`^\s*gitlab_rails\['db_key_base'\]\s*=\s*['"]?([A-Za-z0-9_-]+)`)

// Check fetches the db_key_base from primary and secondary via SSH and
// compares them. Returns nil if they match, an error describing the
// mismatch otherwise. The key value itself is never logged.
func Check(ctx context.Context, primarySSH, secondarySSH string) error {
	return CheckWithRunner(ctx, primarySSH, secondarySSH, sshexec.Default)
}

// CheckWithConfig is like Check but uses the provided SSH config for
// host-key verification.
func CheckWithConfig(ctx context.Context, primarySSH, secondarySSH string, sshCfg sshexec.Config) error {
	return CheckWithRunner(ctx, primarySSH, secondarySSH, sshCfg)
}

// CheckWithRunner accepts any sshexec.Runner (e.g. a mock in tests).
func CheckWithRunner(ctx context.Context, primarySSH, secondarySSH string, runner sshexec.Runner) error {
	pKey, err := fetchKey(ctx, primarySSH, runner)
	if err != nil {
		return fmt.Errorf("primary db_key_base: %w", err)
	}
	sKey, err := fetchKey(ctx, secondarySSH, runner)
	if err != nil {
		return fmt.Errorf("secondary db_key_base: %w", err)
	}
	if pKey != sKey {
		return fmt.Errorf("%w: update the secondary's /etc/gitlab/gitlab.rb and run `gitlab-ctl reconfigure`", ErrKeyMismatch)
	}
	return nil
}

// ErrKeyMismatch reports that both keys were read successfully and
// differ. Callers use it to tell a definite mismatch apart from a key
// that could not be read at all — during a failover the primary is
// usually unreachable, and that must not be mistaken for a mismatch.
var ErrKeyMismatch = errors.New("db_key_base mismatch: primary and secondary keys differ")

// fetchKey SSHes to host and extracts the db_key_base. It checks both
// /etc/gitlab/gitlab.rb (when explicitly set) and the generated
// /var/opt/gitlab/gitlab-rails/etc/secrets.yml (Omnibus default location).
// Returns the key value or an error. The key is never printed by callers.
func fetchKey(ctx context.Context, sshHost string, runner sshexec.Runner) (string, error) {
	if err := sshexec.CheckHost(sshHost); err != nil {
		return "", err
	}

	key, err := tryFetchKey(ctx, sshHost, runner, true)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		key, err = tryFetchKey(ctx, sshHost, runner, false)
		if err != nil {
			return "", fmt.Errorf("ssh %s: %w", sshHost, err)
		}
	}
	m := dbKeyRe.FindSubmatch([]byte(key))
	if m == nil {
		yamlRe := regexp.MustCompile(`db_key_base:\s*['"]?([A-Za-z0-9_-]+)`)
		m = yamlRe.FindSubmatch([]byte(key))
	}
	if m == nil {
		return "", fmt.Errorf("db_key_base not found in secrets.yml or gitlab.rb on %s", sshHost)
	}
	return strings.TrimSpace(string(m[1])), nil
}

func tryFetchKey(ctx context.Context, sshHost string, runner sshexec.Runner, withSudo bool) (string, error) {
	cmd := "grep 'db_key_base' /var/opt/gitlab/gitlab-rails/etc/secrets.yml 2>/dev/null || grep \"gitlab_rails\\['db_key_base'\\]\" /etc/gitlab/gitlab.rb 2>/dev/null || true"
	if withSudo {
		cmd = "sudo grep 'db_key_base' /var/opt/gitlab/gitlab-rails/etc/secrets.yml 2>/dev/null || " + cmd
	}
	out, err := runner.CombinedOutput(ctx, sshHost, cmd)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
