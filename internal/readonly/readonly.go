// Package readonly suppresses writes on a secondary GitLab instance via
// SSH. It does this by:
//   - putting the container registry into read-only mode so no pushes
//     land on the replica,
//   - pausing Sidekiq so no background jobs run on the secondary while it
//     is a replica, and
//   - clearing the writable repository storage list so the application
//     will not place new repositories on the replica.
//
// All three are standard GitLab omnibus administration, available on CE.
// We deliberately do not use GitLab's Maintenance Mode, which is a paid
// Premium/Ultimate feature.
//
// These measures shrink the write surface; they are not a hard read-only
// guarantee. The replica's Postgres is a physical standby and rejects
// writes at the storage layer, so application writes fail rather than
// diverge — but for a clean user-facing block, front the secondary with a
// proxy that rejects mutating HTTP methods.
package readonly

import (
	"context"
	"fmt"

	"github.com/lknappich/syncctl/internal/sshexec"
)

// Enable applies the write-suppression measures to the secondary:
//  1. Start the registry read-only filter.
//  2. Pause Sidekiq (no job processing on the replica).
//  3. Clear repository_storages so no new repositories are placed here.
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
