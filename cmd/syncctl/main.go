// Package main is the syncctl command-line entry point.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/dbkey"
	"github.com/lknappich/syncctl/internal/doctor"
	"github.com/lknappich/syncctl/internal/failover"
	initcmd "github.com/lknappich/syncctl/internal/initcmd"
	"github.com/lknappich/syncctl/internal/logging"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/pgsetup"
	"github.com/lknappich/syncctl/internal/readonly"
	"github.com/lknappich/syncctl/internal/reconciler"
	"github.com/lknappich/syncctl/internal/reconciler/apivalidator"
	"github.com/lknappich/syncctl/internal/reconciler/consistency"
	"github.com/lknappich/syncctl/internal/reconciler/fsstorage"
	"github.com/lknappich/syncctl/internal/reconciler/gitfetch"
	"github.com/lknappich/syncctl/internal/reconciler/gitrsync"
	"github.com/lknappich/syncctl/internal/reconciler/objectstorage"
	pgreconciler "github.com/lknappich/syncctl/internal/reconciler/postgres"
	"github.com/lknappich/syncctl/internal/reconciler/registry"
	"github.com/lknappich/syncctl/internal/runbook"
	"github.com/lknappich/syncctl/internal/sla"
	"github.com/lknappich/syncctl/internal/version"
	"github.com/lknappich/syncctl/internal/webhook"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type globalFlags struct {
	configPath string
	logLevel   string
	logFormat  string
	dryRun     bool
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}

	root := &cobra.Command{
		Use:           "syncctl",
		Short:         "syncctl control plane",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&g.configPath, "config", "c", "config.yaml",
		"path to config YAML (default: config.yaml)")
	root.PersistentFlags().StringVar(&g.logLevel, "log-level", "",
		"log level (trace|debug|info|warn|error); overrides config")
	root.PersistentFlags().StringVar(&g.logFormat, "log-format", "",
		"log format (json|text); overrides config")
	root.PersistentFlags().BoolVar(&g.dryRun, "dry-run", false,
		"print actions without performing them (where supported)")

	root.AddCommand(
		newVersionCmd(),
		newConfigValidateCmd(g),
		newServeCmd(g),
		newPGCmd(g),
		newSyncCmd(g),
		newDBKeyCmd(g),
		newFailoverCmd(g),
		newAdoptCmd(g),
		newRunbookCmd(g),
		newSLACmd(g),
		newDoctorCmd(g),
		newInitCmd(),
	)
	return root
}

const longHelp = `syncctl orchestrates infrastructure-level (Postgres WAL + object/git
storage replication) one-to-one mirroring between self-hosted GitLab
instances. It does NOT use GitLab's proprietary Geo feature; all
replication is performed via documented Postgres/S3/git interfaces.`

func loadConfig(g *globalFlags) (*config.Config, error) {
	cfg, err := config.Load(g.configPath)
	if err != nil {
		return nil, err
	}
	lvl := g.logLevel
	if lvl == "" {
		lvl = cfg.Log.Level
	}
	fmt := g.logFormat
	if fmt == "" {
		fmt = cfg.Log.Format
	}
	logging.Configure(lvl, fmt)
	return cfg, nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print build version and exit",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println(version.Current().String())
			return nil
		},
	}
}

func newConfigValidateCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:           "config-validate",
		Short:         "Load and validate config, printing a summary",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			fmt.Printf("primary: %s (%s)\n", cfg.Primary.Name, cfg.Primary.ExternalURL)
			fmt.Printf("secondaries: %d\n", len(cfg.Secondaries))
			for _, s := range cfg.Secondaries {
				fmt.Printf("  - %s (%s)\n", s.Name, s.ExternalURL)
			}
			fmt.Printf("git mode: %s\n", cfg.Primary.Git.Mode)
			fmt.Printf("object storage backend: %s\n", cfg.Primary.ObjectStore.Backend)
			fmt.Printf("sweep interval: %s\n", cfg.Sync.SweepInterval)
			fmt.Printf("failover enabled: %t\n", cfg.Sync.FailoverEnabled)
			fmt.Printf("read-only secondary: %t\n", cfg.Sync.ReadOnlySecondary)
			if cfg.APIValidator != nil {
				fmt.Printf("api validator: enabled=%t\n", cfg.APIValidator.Enabled)
			}
			if cfg.Webhook != nil {
				fmt.Printf("webhook receiver: %s\n", cfg.Webhook.Addr)
			}
			if cfg.Failover != nil {
				fmt.Printf("failover: auto=%t quorum=%d dns=%s\n",
					cfg.Failover.AutoFailover, cfg.Failover.QuorumRequired, cfg.Failover.DNSPlugin)
			}
			fmt.Printf("metrics addr: %s\n", cfg.Metrics.Addr)
			fmt.Printf("control db: %s\n", cfg.ControlDB)
			return nil
		},
	}
}

func newServeCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:           "serve",
		Short:         "Run the sync engine: reconcilers + metrics + webhook + failover",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			metrics.Register(metrics.Registry)
			srv := metrics.NewServer(cfg.Metrics.Addr)
			ctx, cancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer cancel()

			recs, cleanup, err := buildReconcilers(ctx, cfg, g.dryRun)
			if err != nil {
				return fmt.Errorf("build reconcilers: %w", err)
			}
			defer cleanup()

			if cfg.Sync.ReadOnlySecondary {
				for _, s := range cfg.Secondaries {
					if err := readonly.Enable(ctx, s.SSHHost, g.dryRun); err != nil {
						return fmt.Errorf("enable readonly on %s: %w", s.Name, err)
					}
				}
			}

			errCh := make(chan error, 4)
			go func() { errCh <- srv.Start(ctx) }()

			runner := reconciler.NewRunner(cfg.Sync.SweepInterval, recs...)
			go func() { runner.Run(ctx); errCh <- nil }()

			// Webhook receiver (optional).
			var whServer *webhook.Server
			if cfg.Webhook != nil {
				var whErr error
				whServer, whErr = buildWebhookServer(cfg, recs, g.dryRun)
				if whErr != nil {
					return fmt.Errorf("webhook server: %w", whErr)
				}
				go func() { errCh <- whServer.Start(ctx) }()
			}

			// Failover controller (optional).
			if cfg.Sync.FailoverEnabled && cfg.Failover != nil {
				fc := failover.New(cfg, g.dryRun)
				go func() { fc.Run(ctx); errCh <- nil }()
			}

			cmd.Println("syncctl serving; reconcilers running")
			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				if err != nil {
					return err
				}
				<-ctx.Done()
				return nil
			}
		},
	}
}

// buildReconcilers constructs all enabled reconcilers and returns a
// cleanup function to close their resources.
func buildReconcilers(ctx context.Context, cfg *config.Config, dryRun bool) ([]reconciler.Reconciler, func(), error) {
	var recs []reconciler.Reconciler
	var cleanups []func()

	pgRec, err := pgreconciler.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("postgres reconciler: %w", err)
	}
	recs = append(recs, pgRec)
	cleanups = append(cleanups, pgRec.Close)

	cleanup := func() {
		for _, c := range cleanups {
			c()
		}
	}

	// Every secondary gets its own reconciler set. Building only for
	// Secondaries[0] left every replica after the first with nothing but
	// Postgres lag monitoring, diverging silently.
	for i := range cfg.Secondaries {
		s := &cfg.Secondaries[i]
		siteRecs, err := buildSiteReconcilers(ctx, cfg, s, pgRec, dryRun)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		recs = append(recs, siteRecs...)
	}

	return recs, cleanup, nil
}

// buildSiteReconcilers constructs the reconcilers that replicate the
// primary to one secondary.
func buildSiteReconcilers(ctx context.Context, cfg *config.Config, s *config.SiteConfig,
	pgRec *pgreconciler.Reconciler, dryRun bool) ([]reconciler.Reconciler, error) {
	var recs []reconciler.Reconciler

	// Git data sync.
	switch cfg.Primary.Git.Mode {
	case "rsync":
		recs = append(recs, gitrsync.New(&cfg.Primary, s, dryRun, cfg.SSHExecConfig()))
	case "fetch":
		recs = append(recs, gitfetch.New(cfg.Primary.SSHHost, s.Git.ReposPath, s.Name,
			pgRec.PrimaryPool(), dryRun, cfg.SSHExecConfig()))
	}

	// Object storage.
	switch cfg.Primary.ObjectStore.Backend {
	case "s3":
		osRec, err := objectstorage.New(ctx, s.Name, cfg.Primary.ObjectStore.S3, s.ObjectStore.S3)
		if err != nil {
			return nil, fmt.Errorf("object storage reconciler for %s: %w", s.Name, err)
		}
		recs = append(recs, osRec)
	case "fs":
		recs = append(recs, fsstorage.New(&cfg.Primary, s, dryRun, cfg.SSHExecConfig()))
	}

	// Registry. New returns nil when either side has no registry.url —
	// there is no endpoint to check, and a reconciler that probes the
	// wrong host reports on something other than the registry.
	if cfg.Primary.Registry != nil {
		if rec := registry.New(&cfg.Primary, s, dryRun); rec != nil {
			recs = append(recs, rec)
		} else {
			log.Warn().Msg("registry configured but registry.url is not set on both sites; registry parity will not be checked")
		}
	}

	// Consistency sweep.
	recs = append(recs, consistency.New(
		pgRec.PrimaryPool(),
		pgRec.SecondaryPool(s.Name),
		s.Name,
		s.Git.ReposPath,
		cfg.Sync.ConsistencySamplePct,
	))

	// API validator (optional).
	if cfg.APIValidator != nil && cfg.APIValidator.Enabled {
		recs = append(recs, apivalidator.New(cfg, s))
	}

	return recs, nil
}

// buildWebhookServer creates a webhook receiver wired to trigger
// immediate git fetch for the affected project. In fetch mode it calls
// the gitfetch reconciler's per-project FetchProject; in rsync mode or
// when no gitfetch reconciler is available, it logs and falls back to
// the next sweep.
func buildWebhookServer(cfg *config.Config, recs []reconciler.Reconciler, dryRun bool) (*webhook.Server, error) {
	var fetcher *gitfetch.Reconciler
	for _, r := range recs {
		if gf, ok := r.(*gitfetch.Reconciler); ok {
			fetcher = gf
			break
		}
	}
	trigger := func(ctx context.Context, projectPath, eventType string) error {
		if fetcher != nil {
			return fetcher.FetchProject(ctx, projectPath)
		}
		log.Info().Str("project", projectPath).Str("event", eventType).
			Msg("webhook received but no gitfetch reconciler; next sweep will sync")
		return nil
	}
	mgr := webhook.NewTriggerManager(trigger)
	srv, err := webhook.NewServer(cfg.Webhook.Addr, cfg.Webhook.SecretToken, mgr.Trigger)
	if err != nil {
		return nil, err
	}
	if cfg.Webhook.TLSCert != "" {
		srv = srv.WithTLS(cfg.Webhook.TLSCert, cfg.Webhook.TLSKey)
	}
	return srv, nil
}

// --- pg subcommand ---

func newPGCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pg",
		Short:         "PostgreSQL replication management",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newPGSetupCmd(g), newPGStatusCmd(g))
	return cmd
}

func newPGSetupCmd(g *globalFlags) *cobra.Command {
	var dataDir string
	var secondaryName string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Bootstrap a secondary as a physical streaming replica via pg_basebackup",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			sc, err := findSecondary(cfg, secondaryName)
			if err != nil {
				return err
			}
			if dataDir == "" {
				return fmt.Errorf("--data-dir is required")
			}
			ctx, cancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer cancel()
			dsn, password := cfg.Primary.Postgres.ReplicationConnInfo()
			return pgsetup.Run(ctx, pgsetup.Options{
				PrimaryDSN: dsn,
				Password:   password,
				DataDir:    dataDir,
				SlotName:   sc.Postgres.SlotName,
				DryRun:     g.dryRun,
			})
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "secondary PGDATA path (required)")
	cmd.Flags().StringVar(&secondaryName, "secondary", "", "name of the secondary in config (required)")
	_ = cmd.MarkFlagRequired("secondary")
	return cmd
}

func newPGStatusCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current PostgreSQL replication lag and standby state",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 10*time.Second)
			defer cancel()
			pgRec, err := pgreconciler.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer pgRec.Close()
			result := pgRec.Reconcile(ctx)
			fmt.Printf("postgres sync: ok=%t lag=%s remaining=%d\n",
				result.OK, result.Lag, result.Remaining)
			fmt.Printf("detail: %s\n", result.Detail)
			return nil
		},
	}
	return cmd
}

// --- sync subcommand ---

func newSyncCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "sync",
		Short:         "Run a single reconciliation sweep and print results",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			metrics.Register(metrics.Registry)
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 5*time.Minute)
			defer cancel()
			recs, cleanup, err := buildReconcilers(ctx, cfg, g.dryRun)
			if err != nil {
				return err
			}
			defer cleanup()
			for _, r := range recs {
				result := r.Reconcile(ctx)
				status := "DRIFT"
				if result.OK {
					status = "OK"
				}
				fmt.Printf("[%s] %s: %s (lag=%s, repaired=%d, remaining=%d)\n",
					status, r.Name(), result.Detail, result.Lag, result.Repaired, result.Remaining)
			}
			return nil
		},
	}
	return cmd
}

// --- dbkey subcommand ---

func newDBKeyCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "dbkey",
		Short:         "Verify db_key_base parity between primary and secondary",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			if len(cfg.Secondaries) == 0 {
				return fmt.Errorf("no secondaries configured")
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 30*time.Second)
			defer cancel()
			for _, s := range cfg.Secondaries {
				fmt.Printf("checking %s ... ", s.Name)
				if err := dbkey.Check(ctx, cfg.Primary.SSHHost, s.SSHHost); err != nil {
					fmt.Printf("MISMATCH: %v\n", err)
				} else {
					fmt.Println("OK")
				}
			}
			return nil
		},
	}
	return cmd
}

// --- failover subcommand ---

func newFailoverCmd(g *globalFlags) *cobra.Command {
	var secondaryName string
	var confirm bool
	var force bool
	cmd := &cobra.Command{
		Use:           "failover",
		Short:         "Promote a secondary to primary (human-gated)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			if secondaryName == "" && len(cfg.Secondaries) > 0 {
				secondaryName = cfg.Secondaries[0].Name
			}
			if secondaryName == "" {
				return fmt.Errorf("--secondary is required (no secondaries in config)")
			}
			if !confirm && !g.dryRun {
				return fmt.Errorf("failover requires --yes or --dry-run")
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 10*time.Minute)
			defer cancel()
			fc := failover.New(cfg, g.dryRun)
			fc.SetForce(force)
			return fc.Promote(ctx, secondaryName)
		},
	}
	cmd.Flags().StringVar(&secondaryName, "secondary", "", "name of the secondary to promote")
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm failover (required without --dry-run)")
	cmd.Flags().BoolVar(&force, "force", false,
		"promote even if the primary still answers health checks (risks split-brain)")
	return cmd
}

// --- adopt-as-secondary subcommand ---

func newAdoptCmd(g *globalFlags) *cobra.Command {
	var oldPrimarySSH string
	var wipePGData bool
	cmd := &cobra.Command{
		Use:           "adopt-as-secondary",
		Short:         "Convert the old primary into a secondary of the new primary (role-swap)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			if oldPrimarySSH == "" {
				oldPrimarySSH = cfg.Primary.SSHHost
			}
			if oldPrimarySSH == "" {
				return fmt.Errorf("--old-primary-ssh is required (or set primary.ssh_host in config)")
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 30*time.Minute)
			defer cancel()
			fc := failover.New(cfg, g.dryRun)
			fc.SetWipePGData(wipePGData)
			return fc.AdoptAsSecondary(ctx, oldPrimarySSH)
		},
	}
	cmd.Flags().StringVar(&oldPrimarySSH, "old-primary-ssh", "", "SSH host:port of the old primary")
	cmd.Flags().BoolVar(&wipePGData, "wipe-pgdata", false,
		"delete the old primary's PostgreSQL data directory before re-basing (destructive)")
	return cmd
}

// --- runbook subcommand ---

func newRunbookCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "runbook",
		Short:         "Generate an operational runbook from your config",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			return runbook.Generate(os.Stdout, cfg)
		},
	}
	return cmd
}

// --- sla subcommand ---

func newSLACmd(g *globalFlags) *cobra.Command {
	var metricsURL string
	cmd := &cobra.Command{
		Use:           "sla",
		Short:         "Print RPO/RTO and lag summary from a running syncctl's metrics",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if metricsURL == "" {
				cfg, err := loadConfig(g)
				if err != nil {
					return err
				}
				metricsURL = sla.DefaultMetricsURL(cfg.Metrics.Addr)
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 15*time.Second)
			defer cancel()
			return sla.Generate(ctx, os.Stdout, metricsURL)
		},
	}
	cmd.Flags().StringVar(&metricsURL, "metrics-url", "",
		"metrics endpoint of the running syncctl (default: derived from metrics.addr)")
	return cmd
}

func findSecondary(cfg *config.Config, name string) (*config.SiteConfig, error) {
	for i := range cfg.Secondaries {
		if cfg.Secondaries[i].Name == name {
			return &cfg.Secondaries[i], nil
		}
	}
	return nil, fmt.Errorf("secondary %q not found in config", name)
}

// --- doctor subcommand ---

func newDoctorCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "doctor",
		Short:         "Check prerequisites on primary and secondary sites",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(g)
			if err != nil {
				return err
			}
			sigCtx, sigCancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer sigCancel()
			ctx, cancel := context.WithTimeout(sigCtx, 2*time.Minute)
			defer cancel()
			result := doctor.Run(ctx, cfg)
			result.Print()
			if result.Fail > 0 {
				return fmt.Errorf("doctor: %d checks failed", result.Fail)
			}
			return nil
		},
	}
	return cmd
}

// --- init subcommand ---

func newInitCmd() *cobra.Command {
	var outputPath string
	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Generate a config.yaml via interactive wizard",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			answers, err := initcmd.Run(os.Stdout)
			if err != nil {
				return err
			}
			if outputPath == "" {
				outputPath = "config.yaml"
			}
			// #nosec G304 -- writing the generated config to the path the
			// operator gave with -o is the command's purpose.
			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create %s: %w", outputPath, err)
			}
			defer func() { _ = f.Close() }()
			if err := initcmd.GenerateYAML(answers, f); err != nil {
				return err
			}
			fmt.Printf("\nConfig written to %s\n", outputPath)
			fmt.Println("\nNext steps:")
			fmt.Println("  1. Export the required environment variables:")
			fmt.Println("     export PG_CTRL_PASSWORD=...")
			fmt.Println("     export PG_REPL_PASSWORD=...")
			fmt.Println("     export SEC_REPL_PASSWORD=...")
			fmt.Println("     export S3_AK=...")
			fmt.Println("     export S3_SK=...")
			fmt.Println("  2. Run: syncctl doctor -c", outputPath)
			fmt.Println("  3. Run: syncctl pg setup --secondary <name> --data-dir /var/opt/gitlab/postgresql/data")
			fmt.Println("  4. Run: syncctl serve -c", outputPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "config.yaml", "output config path")
	return cmd
}
