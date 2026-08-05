// Package failover implements the failover controller: heartbeat-based
// primary failure detection, secondary promotion, and role-swap support.
package failover

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/dbkey"
	"github.com/lknappich/syncctl/internal/readonly"
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
	return fc
}

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
	fails := 0
	for _, url := range c.healthURLs {
		if !c.pollURL(ctx, url) {
			fails++
		}
	}
	if fails >= c.quorum {
		c.consecutiveFails.Add(1)
		down := c.consecutiveFails.Load()
		log.Warn().Int("fails", fails).Int("quorum", c.quorum).
			Int64("consecutive", down).
			Msg("primary health check failed")
		if down >= 3 {
			c.primaryDown.Store(true)
			if c.autoFailover {
				log.Error().Msg("primary declared down; auto-failover triggered")
				if err := c.Promote(ctx, c.cfg.Secondaries[0].Name); err != nil {
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

	steps := []promotionStep{
		{"verify primary down", c.verifyPrimaryDown},
		{"stop gitlab services on secondary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost, "sudo gitlab-ctl stop")
		}},
		{"promote postgres", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost,
				"sudo -u gitlab-psql /opt/gitlab/embedded/bin/pg_ctl promote "+
					"-D /var/opt/gitlab/postgresql/data")
		}},
		{"disable read-only mode", func(ctx context.Context) error {
			return readonly.DisableWithConfig(ctx, secondary.SSHHost, c.dryRun, c.sshCfg)
		}},
		{"start gitlab services on secondary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, secondary.SSHHost, "sudo gitlab-ctl start")
		}},
		{"verify db_key_base parity", func(ctx context.Context) error {
			return dbkey.CheckWithConfig(ctx, c.cfg.Primary.SSHHost, secondary.SSHHost, c.sshCfg)
		}},
	}

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

// AdoptAsSecondary converts the old primary into a secondary of the new
// primary. This is the role-swap step.
func (c *Controller) AdoptAsSecondary(ctx context.Context, oldPrimarySSH string) error {
	if !c.dryRun && !c.cfg.Sync.FailoverEnabled {
		return fmt.Errorf("failover is disabled in config")
	}

	log.Info().Str("old_primary_ssh", oldPrimarySSH).Bool("dry_run", c.dryRun).
		Msg("starting role-swap: adopting old primary as secondary")

	steps := []promotionStep{
		{"stop gitlab on old primary", func(ctx context.Context) error {
			return c.sshSecondary(ctx, oldPrimarySSH, "sudo gitlab-ctl stop")
		}},
		{"pg_basebackup from new primary", func(ctx context.Context) error {
			newPrimary := c.cfg.Secondaries[0]
			return c.sshSecondary(ctx, oldPrimarySSH,
				fmt.Sprintf("sudo -u gitlab-psql /opt/gitlab/embedded/bin/pg_basebackup "+
					"-h %s -U %s -D /var/opt/gitlab/postgresql/data -X stream -c fast -R -P",
					newPrimary.Postgres.Host, newPrimary.Postgres.ReplicationUser))
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

func (c *Controller) verifyPrimaryDown(ctx context.Context) error {
	if !c.primaryDown.Load() {
		return fmt.Errorf("primary is not declared down; refusing to promote (use --force to override)")
	}
	return nil
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
