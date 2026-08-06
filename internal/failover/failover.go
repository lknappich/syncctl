// Package failover implements the failover controller: heartbeat-based
// primary failure detection, secondary promotion, and role-swap support.
package failover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/dbkey"
	"github.com/lknappich/syncctl/internal/readonly"
	"github.com/lknappich/syncctl/internal/shellquote"
	"github.com/lknappich/syncctl/internal/sshexec"
)

// Controller monitors primary health and orchestrates failover.
type Controller struct {
	cfg           *config.Config
	sshCfg        sshexec.Config
	primaryURL    string
	healthURLs    []string
	quorum        int
	checkInterval time.Duration
	autoFailover  bool
	dryRun        bool
	force         bool
	wipePGData    bool
	client        *http.Client

	// State.
	primaryDown      atomic.Bool
	consecutiveFails atomic.Int64
}

// New creates a failover controller from config.
func New(cfg *config.Config, dryRun bool) *Controller {
	fc := &Controller{
		cfg:           cfg,
		sshCfg:        cfg.SSHExecConfig(),
		primaryURL:    cfg.Primary.ExternalURL,
		healthURLs:    []string{cfg.Primary.ExternalURL},
		checkInterval: 10 * time.Second,
		autoFailover:  false,
		dryRun:        dryRun,
		client:        &http.Client{Timeout: 5 * time.Second},
	}
	if cfg.Failover != nil {
		fc.healthURLs = append(fc.healthURLs, cfg.Failover.HealthCheckURLs...)
		fc.quorum = cfg.Failover.QuorumRequired
		fc.checkInterval = cfg.Failover.HealthCheckInterval
		fc.autoFailover = cfg.Failover.AutoFailover
	} else {
		fc.quorum = 1
	}
	if fc.quorum < 1 {
		fc.quorum = 1
	}
	return fc
}

// SetForce allows promotion of a primary that still answers health
// checks. Split-brain territory; the caller is expected to have gated
// this behind an explicit operator flag.
func (c *Controller) SetForce(force bool) { c.force = force }

// SetWipePGData permits `adopt-as-secondary` to delete the old
// primary's PostgreSQL data directory before re-basing it. Destructive
// and irreversible; gate it behind an explicit operator flag.
func (c *Controller) SetWipePGData(wipe bool) { c.wipePGData = wipe }

// Run starts the health-check loop. Blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	log.Info().Dur("interval", c.checkInterval).Int("quorum", c.quorum).
		Bool("auto_failover", c.autoFailover).
		Msg("failover controller started")
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("failover controller stopped")
			return
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

// check polls all health URLs and determines if the primary is down.
func (c *Controller) check(ctx context.Context) {
	fails := c.countHealthFailures(ctx)
	if fails >= c.quorum {
		c.consecutiveFails.Add(1)
		down := c.consecutiveFails.Load()
		log.Warn().Int("fails", fails).Int("quorum", c.quorum).
			Int64("consecutive", down).
			Msg("primary health check failed")
		if down >= 3 {
			c.primaryDown.Store(true)
			if c.autoFailover {
				target, err := c.autoFailoverTarget()
				if err != nil {
					log.Error().Err(err).Msg("auto-failover cannot proceed")
					return
				}
				log.Error().Str("secondary", target).Msg("primary declared down; auto-failover triggered")
				if err := c.Promote(ctx, target); err != nil {
					log.Error().Err(err).Msg("auto-failover failed")
				}
			} else {
				log.Error().Msg("primary declared down; auto-failover disabled — run `syncctl failover` manually")
			}
		}
	} else {
		if c.consecutiveFails.Load() > 0 {
			log.Info().Msg("primary health check recovered")
		}
		c.consecutiveFails.Store(0)
		c.primaryDown.Store(false)
	}
}

// countHealthFailures polls every health URL and returns how many did
// not answer 200.
func (c *Controller) countHealthFailures(ctx context.Context) int {
	fails := 0
	for _, url := range c.healthURLs {
		if !c.pollURL(ctx, url) {
			fails++
		}
	}
	return fails
}

// autoFailoverTarget resolves which secondary auto-failover promotes.
//
// It used to be Secondaries[0] unconditionally, so with several replicas
// the choice of which one became the primary was made by config-file
// ordering rather than by anything about the replicas. Refusing is the
// safer failure: a primary that stays down is recoverable, a promotion
// of the wrong replica is not.
func (c *Controller) autoFailoverTarget() (string, error) {
	if c.cfg.Failover != nil && c.cfg.Failover.PromoteSecondary != "" {
		name := c.cfg.Failover.PromoteSecondary
		if _, err := c.findSecondary(name); err != nil {
			return "", fmt.Errorf("failover.promote_secondary: %w", err)
		}
		return name, nil
	}
	switch len(c.cfg.Secondaries) {
	case 0:
		return "", errors.New("no secondaries configured")
	case 1:
		return c.cfg.Secondaries[0].Name, nil
	default:
		return "", fmt.Errorf(
			"%d secondaries configured and failover.promote_secondary is unset; "+
				"refusing to choose one by config order — set it, or promote manually with `syncctl failover --secondary <name> --yes`",
			len(c.cfg.Secondaries))
	}
}

// ResolveNewPrimary picks the secondary that is now serving as primary,
// for the role-swap. Same rule as autoFailoverTarget: unambiguous when
// there is one candidate, explicit otherwise.
func (c *Controller) ResolveNewPrimary(name string) (*config.SiteConfig, error) {
	if name != "" {
		return c.findSecondary(name)
	}
	switch len(c.cfg.Secondaries) {
	case 0:
		return nil, errors.New("no secondaries configured; cannot determine the new primary to re-base from")
	case 1:
		return &c.cfg.Secondaries[0], nil
	default:
		return nil, fmt.Errorf(
			"%d secondaries configured; pass --new-primary <name> to say which one was promoted "+
				"(re-basing from the wrong site would replicate a stale replica over this host)",
			len(c.cfg.Secondaries))
	}
}

// pollURL returns true if the URL returns HTTP 200 within the timeout.
func (c *Controller) pollURL(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// IsPrimaryDown returns the current health state.
func (c *Controller) IsPrimaryDown() bool { return c.primaryDown.Load() }

// Promote orchestrates the secondary promotion sequence.
// Steps:
//  1. Verify primary is down (if not auto, warn).
//  2. Stop GitLab services on secondary.
//  3. pg_ctl promote on secondary PG.
//  4. Make secondary's object storage writable.
//  5. Restart GitLab on secondary.
//  6. Disable read-only mode.
//  7. Print runbook for re-pointing runners.
func (c *Controller) Promote(ctx context.Context, secondaryName string) error {
	secondary, err := c.findSecondary(secondaryName)
	if err != nil {
		return err
	}

	if !c.dryRun && !c.cfg.Sync.FailoverEnabled {
		return fmt.Errorf("failover is disabled in config (sync.failover_enabled=false)")
	}

	log.Info().Str("secondary", secondaryName).Bool("dry_run", c.dryRun).
		Msg("starting failover promotion")

	steps := c.promotionSteps(secondary)

	for _, step := range steps {
		log.Info().Str("step", step.name).Msg("failover step")
		if c.dryRun {
			fmt.Printf("[dry-run] step: %s\n", step.name)
			continue
		}
		if err := step.fn(ctx); err != nil {
			log.Error().Err(err).Str("step", step.name).Msg("failover step failed")
			return fmt.Errorf("step %q: %w", step.name, err)
		}
	}

	log.Info().Str("secondary", secondaryName).
		Msg("failover complete — secondary is now primary")
	fmt.Println("\n=== POST-FAILOVER RUNBOOK ===")
	fmt.Println("1. Update DNS to point to the new primary:", secondary.ExternalURL)
	fmt.Println("2. Re-point CI runners to the new primary coordinator URL")
	fmt.Println("3. Update any integrations that reference the old primary URL")
	fmt.Println("4. When the old primary recovers, run: syncctl adopt-as-secondary --secondary", c.cfg.Primary.Name)
	fmt.Println("5. Verify webhook secrets and access tokens work (behavioral check)")

	return nil
}

// promotionSteps is the ordered promotion sequence.
//
// Parity is a precondition, not a postcondition. Verified after
// promotion it necessarily SSHes to the primary we just declared dead,
// so it failed — and Promote returned an error for a promotion that had
// already succeeded, skipping the post-failover runbook. Verified here
// it can still reach the old primary, and a mismatch costs nothing
// because nothing destructive has run yet.
func (c *Controller) promotionSteps(secondary *config.SiteConfig) []promotionStep {
	return []promotionStep{
		{"verify primary down", c.verifyPrimaryDown},
		{"verify db_key_base parity", c.verifyDBKeyParity(secondary)},
		{"stop gitlab services on secondary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost, "sudo gitlab-ctl stop")
		}},
		{"promote postgres", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost,
				"sudo -u gitlab-psql /opt/gitlab/embedded/bin/pg_ctl promote "+
					"-D "+shellquote.Quote(remotePGData))
		}},
		{"disable read-only mode", func(ctx context.Context) error {
			return readonly.DisableWithConfig(ctx, secondary.SSHHost, c.dryRun, c.sshCfg)
		}},
		{"start gitlab services on secondary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost, "sudo gitlab-ctl start")
		}},
	}
}

// AdoptAsSecondary converts the old primary into a secondary of the new
// primary. This is the role-swap step.
func (c *Controller) AdoptAsSecondary(ctx context.Context, oldPrimarySSH, newPrimaryName string) error {
	if !c.dryRun && !c.cfg.Sync.FailoverEnabled {
		return fmt.Errorf("failover is disabled in config")
	}

	newPrimary, err := c.ResolveNewPrimary(newPrimaryName)
	if err != nil {
		return err
	}

	log.Info().Str("old_primary_ssh", oldPrimarySSH).Str("new_primary", newPrimary.Name).
		Bool("dry_run", c.dryRun).
		Msg("starting role-swap: adopting old primary as secondary")

	defer func() {
		if err := c.removeRemotePassfile(context.WithoutCancel(ctx), oldPrimarySSH); err != nil {
			log.Warn().Err(err).Msg("failed to remove the staged replication passfile; remove " + remotePassfile + " manually")
		}
	}()

	steps := []promotionStep{
		{"stop gitlab on old primary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, oldPrimarySSH, "sudo gitlab-ctl stop")
		}},
		{"check old primary PGDATA is clear", func(ctx context.Context) error {
			return c.checkRemotePGDataEmpty(ctx, oldPrimarySSH)
		}},
		{"stage replication credentials on old primary", func(ctx context.Context) error {
			return c.writeRemotePassfile(ctx, oldPrimarySSH, newPrimary.Postgres)
		}},
		{"pg_basebackup from new primary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, oldPrimarySSH,
				fmt.Sprintf("sudo -u gitlab-psql PGPASSFILE=%s "+
					"/opt/gitlab/embedded/bin/pg_basebackup "+
					"-h %s -p %d -U %s -D %s -X stream -c fast -R -P",
					shellquote.Quote(remotePassfile),
					shellquote.Quote(newPrimary.Postgres.Host),
					newPrimary.Postgres.Port,
					shellquote.Quote(newPrimary.Postgres.ReplicationUser),
					shellquote.Quote(remotePGData)))
		}},
		{"enable read-only mode on old primary", func(ctx context.Context) error {
			return readonly.EnableWithConfig(ctx, oldPrimarySSH, c.dryRun, c.sshCfg)
		}},
		{"start gitlab on old primary as secondary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, oldPrimarySSH, "sudo gitlab-ctl start")
		}},
	}

	for _, step := range steps {
		log.Info().Str("step", step.name).Msg("role-swap step")
		if c.dryRun {
			fmt.Printf("[dry-run] step: %s\n", step.name)
			continue
		}
		if err := step.fn(ctx); err != nil {
			return fmt.Errorf("role-swap step %q: %w", step.name, err)
		}
	}

	log.Info().Msg("role-swap complete — old primary is now a secondary")
	return nil
}

type promotionStep struct {
	name string
	fn   func(context.Context) error
}

// verifyDBKeyParity compares db_key_base between the primary and the
// secondary about to be promoted. A secondary whose key differs cannot
// decrypt webhook secrets, access tokens, or 2FA seeds, so promoting it
// produces a running GitLab with unusable credentials.
//
// The primary is usually unreachable by the time we get here — that is
// why we are failing over. An unreachable primary is not grounds to
// block promotion, so it downgrades to a warning; only a definite
// mismatch stops the sequence.
func (c *Controller) verifyDBKeyParity(secondary *config.SiteConfig) func(context.Context) error {
	return func(ctx context.Context) error {
		if c.cfg.Primary.SSHHost == "" || secondary.SSHHost == "" {
			log.Warn().Msg("db_key_base parity not verified: ssh_host is not configured for both sites")
			return nil
		}
		err := dbkey.CheckWithConfig(ctx, c.cfg.Primary.SSHHost, secondary.SSHHost, c.sshCfg)
		if err == nil {
			return nil
		}
		if errors.Is(err, dbkey.ErrKeyMismatch) {
			return err
		}
		log.Warn().Err(err).
			Msg("db_key_base parity could not be verified (the primary is likely down); continuing — confirm the keys match before trusting encrypted columns")
		return nil
	}
}

// verifyPrimaryDown gates promotion on the primary actually being down.
//
// primaryDown is only ever set by the health-check loop in Run, which a
// one-shot `syncctl failover` never starts — so reading that field alone
// made manual promotion impossible. Probe directly when the loop has not
// established the state.
func (c *Controller) verifyPrimaryDown(ctx context.Context) error {
	if c.force {
		log.Warn().Msg("--force: skipping the primary liveness check; promoting a primary that is still serving risks split-brain")
		return nil
	}
	if c.primaryDown.Load() {
		return nil
	}
	if fails := c.countHealthFailures(ctx); fails >= c.quorum {
		c.primaryDown.Store(true)
		log.Warn().Int("fails", fails).Int("quorum", c.quorum).
			Msg("primary failed its health checks; proceeding with promotion")
		return nil
	}
	return fmt.Errorf("primary at %s still answers health checks; refusing to promote (pass --force to override)",
		c.primaryURL)
}

const (
	remotePGData = "/var/opt/gitlab/postgresql/data"
	// #nosec G101 -- a path on the remote host, not a credential. The
	// password is written to this file over stdin at run time.
	remotePassfile = "/var/opt/gitlab/postgresql/.syncctl-pgpass"
)

// checkRemotePGDataEmpty refuses to re-base onto a populated data
// directory. pg_basebackup exits rather than write into a non-empty
// PGDATA, so without this the step failed with a message about the
// directory rather than about the old cluster still being there.
// WipePGData opts into clearing it — an explicit, destructive choice.
func (c *Controller) checkRemotePGDataEmpty(ctx context.Context, sshHost string) error {
	if c.wipePGData {
		log.Warn().Str("path", remotePGData).
			Msg("--wipe-pgdata: deleting the old primary's PostgreSQL data directory")
		return c.sshSecondary(ctx, sshHost,
			fmt.Sprintf("sudo rm -rf -- %s && sudo -u gitlab-psql mkdir -p %s",
				shellquote.Quote(remotePGData), shellquote.Quote(remotePGData)))
	}
	out, err := c.sshCfg.CombinedOutput(ctx, sshHost,
		fmt.Sprintf("sudo ls -A %s 2>/dev/null | head -1", shellquote.Quote(remotePGData)))
	if err != nil {
		return fmt.Errorf("inspect %s on %s: %w: %s", remotePGData, sshHost, err, string(out))
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("%s on %s is not empty: pg_basebackup will not write into an existing "+
			"data directory. Back up the old cluster and re-run with --wipe-pgdata to replace it",
			remotePGData, sshHost)
	}
	return nil
}

// writeRemotePassfile stages the replication password in a libpq
// passfile on the remote host, piped over stdin. Putting it in the
// pg_basebackup command line instead would expose it to every local user
// on that host via /proc/<pid>/cmdline for the duration of the backup.
func (c *Controller) writeRemotePassfile(ctx context.Context, sshHost string, pg config.PostgresConfig) error {
	if pg.ReplicationPassword == "" {
		log.Warn().Msg("no replication password configured; pg_basebackup will rely on the remote host's own .pgpass or trust authentication")
		return nil
	}
	// host:port:database:user:password — * matches any database.
	line := fmt.Sprintf("%s:%d:*:%s:%s\n",
		pg.Host, pg.Port, pg.ReplicationUser, pg.ReplicationPassword)
	cmd := fmt.Sprintf("umask 077 && sudo -u gitlab-psql tee %s >/dev/null",
		shellquote.Quote(remotePassfile))
	out, err := c.sshCfg.CombinedOutputStdin(ctx, sshHost, cmd, strings.NewReader(line))
	if err != nil {
		return fmt.Errorf("stage passfile on %s: %w: %s", sshHost, err, string(out))
	}
	return nil
}

func (c *Controller) removeRemotePassfile(ctx context.Context, sshHost string) error {
	if c.dryRun {
		return nil
	}
	_, err := c.sshCfg.CombinedOutput(ctx, sshHost,
		fmt.Sprintf("sudo rm -f -- %s", shellquote.Quote(remotePassfile)))
	return err
}

func (c *Controller) sshSecondary(ctx context.Context, sshHost, command string) error {
	if err := sshexec.CheckHost(sshHost); err != nil {
		return err
	}
	out, err := c.sshCfg.CombinedOutput(ctx, sshHost, command)
	if err != nil {
		return fmt.Errorf("ssh %s: %w: %s", sshHost, err, string(out))
	}
	return nil
}

func (c *Controller) findSecondary(name string) (*config.SiteConfig, error) {
	for i := range c.cfg.Secondaries {
		if c.cfg.Secondaries[i].Name == name {
			return &c.cfg.Secondaries[i], nil
		}
	}
	return nil, fmt.Errorf("secondary %q not found", name)
}
