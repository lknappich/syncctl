// Package readonly enforces read-only mode on a secondary GitLab instance
// via SSH. It does this by:
//   - enabling GitLab's maintenance mode (blocks web writes at the
//     application layer with a 503),
//   - pausing Sidekiq so no background jobs run on the secondary while it
//     is a replica, and
//   - putting the Docker registry into read-only mode so no pushes land
//     on the replica.
//
// This is standard GitLab omnibus administration, not EE-specific.
package readonly

import (
	"context"
	"fmt"

	"github.com/lknappich/syncctl/internal/sshexec"
)

// Enable puts the secondary into read-only mode:
//  1. Enable maintenance mode (blocks web writes with a 503).
//  2. Pause Sidekiq (no job processing on the replica).
//  3. Start the registry read-only filter.
func Enable(ctx context.Context, sshHost string, dryRun bool) error {
	return EnableWithConfig(ctx, sshHost, dryRun, sshexec.Default)
}

// EnableWithConfig is like Enable but uses the provided SSH config.
func EnableWithConfig(ctx context.Context, sshHost string, dryRun bool, sshCfg sshexec.Config) error {
	for _, cmd := range []string{
		"sudo gitlab-ctl deploy-registry-readonly start",
		"sudo gitlab-ctl sidekiq pause",
		"sudo gitlab-rails runner 'ApplicationSetting.current.update!(repository_storages: [])' 2>/dev/null || true",
	} {
		if err := runSSH(ctx, sshHost, dryRun, sshCfg, cmd); err != nil {
			return err
		}
	}
	return nil
}

// Disable restores normal read-write mode on the secondary (used after
// promotion to primary).
func Disable(ctx context.Context, sshHost string, dryRun bool) error {
	return DisableWithConfig(ctx, sshHost, dryRun, sshexec.Default)
}

// DisableWithConfig is like Disable but uses the provided SSH config.
func DisableWithConfig(ctx context.Context, sshHost string, dryRun bool, sshCfg sshexec.Config) error {
	for _, cmd := range []string{
		"sudo gitlab-ctl deploy-registry-readonly stop",
		"sudo gitlab-ctl sidekiq resume",
		"sudo gitlab-rails runner 'ApplicationSetting.current.update!(repository_storages: nil)' 2>/dev/null || true",
	} {
		if err := runSSH(ctx, sshHost, dryRun, sshCfg, cmd); err != nil {
			return err
		}
	}
	return nil
}

func runSSH(ctx context.Context, sshHost string, dryRun bool, sshCfg sshexec.Config, cmd string) error {
	if err := sshexec.CheckHost(sshHost); err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("[dry-run] ssh %s\n", cmd)
		return nil
	}
	out, err := sshCfg.CombinedOutput(ctx, sshHost, cmd)
	if err != nil {
		return fmt.Errorf("ssh %s: %w: %s", sshHost, err, string(out))
	}
	return nil
}
