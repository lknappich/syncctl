// Package runbook generates operational runbooks from the actual config,
// so they match the user's environment. Covers failover, re-attach,
// split-brain recovery, runner re-pointing, and secret rotation.
package runbook

import (
	"fmt"
	"io"
	"text/template"

	"github.com/lknappich/syncctl/internal/config"
)

// Generate writes a Markdown runbook to w based on cfg.
func Generate(w io.Writer, cfg *config.Config) error {
	tmpl, err := template.New("runbook").Funcs(template.FuncMap{
		"pct": func(v float64) string { return fmt.Sprintf("%.1f%%", v*100) },
		"firstSecondaryName": func(c *config.Config) string {
			if len(c.Secondaries) > 0 {
				return c.Secondaries[0].Name
			}
			return ""
		},
	}).Parse(runbookTmpl)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, cfg)
}

const runbookTmpl = `# syncctl Operational Runbook

Generated from your configuration.

## Environment

- **Primary:** {{.Primary.Name}} ({{.Primary.ExternalURL}})
- **Secondaries:** {{len .Secondaries}}
{{range .Secondaries}}  - {{.Name}} ({{.ExternalURL}})
{{end}}

## 1. Failover: Primary → Secondary

When the primary is down and a secondary must be promoted:

### Automated (if auto_failover is enabled)

The failover controller will automatically promote the first secondary
after 3 consecutive health check failures ({{.Primary.ExternalURL}}).

### Manual

    syncctl failover --secondary {{firstSecondaryName .}}

### Post-failover checklist

1. Update DNS to point the primary hostname to the new primary's IP.
2. Re-point CI runners: update ` + "`config.toml`" + ` ` + "`url`" + ` to the new primary.
3. Update any external integrations (Slack, JIRA, etc.) with the new URL.
4. Verify webhook delivery by checking a sample project's webhook logs.
5. Verify access tokens work by making an authenticated API call.

## 2. Role-Swap: Re-attach Old Primary as Secondary

When the old primary comes back online:

    syncctl adopt-as-secondary --old-primary-ssh {{.Primary.SSHHost}}

This runs pg_basebackup from the new primary and reconfigures the old
primary as a read-only secondary.

## 3. Split-Brain Recovery

If both sites were writable simultaneously (rare; requires fencing
failure):

1. **Decide which site has the canonical data.** This is a human decision.
2. On the non-canonical site: stop GitLab, wipe PGDATA, re-basebackup.
3. On the non-canonical site: wipe object storage, re-sync from canonical.
4. On the non-canonical site: start GitLab as secondary.

## 4. db_key_base Rotation

If you need to rotate the db_key_base (e.g. after a suspected compromise):

1. On the primary: update ` + "`gitlab_rails['db_key_base']`" + ` in /etc/gitlab/gitlab.rb.
2. Run ` + "`gitlab-ctl reconfigure`" + ` on the primary.
3. Run the GitLab Rake task to re-encrypt affected columns (consult
   GitLab's public docs for the current task name).
4. Copy the new db_key_base to all secondaries.
5. Run ` + "`gitlab-ctl reconfigure`" + ` on each secondary.
6. Verify with: ` + "`syncctl dbkey`" + `

## 5. Monitoring

- Metrics endpoint: {{.Metrics.Addr}}/metrics
- Health endpoint: {{.Metrics.Addr}}/healthz
- Key alerts:
  - syncctl_pg_replay_lag_seconds > 30s → warning
  - syncctl_drift_total increasing → investigate component
  - syncctl_last_sync_timestamp_seconds stale → reconciler stuck

## 6. CI Runner Re-Pointing

After failover, runners registered against the old primary need updating:

1. SSH to each runner host.
2. Edit /etc/gitlab-runner/config.toml — change the ` + "`url`" + ` field.
3. Run ` + "`gitlab-runner restart`" + `.
4. Verify: ` + "`gitlab-runner verify`" + `.

## 7. Sync Interval & Lag Targets

- Sweep interval: {{.Sync.SweepInterval}}
- Lag warning threshold: {{.Sync.LagWarningThreshold}}
- Lag critical threshold: {{.Sync.LagCriticalThreshold}}
- Consistency sample: {{pct .Sync.ConsistencySamplePct}} of repos per sweep

## 8. Failover Configuration

- Failover enabled: {{.Sync.FailoverEnabled}}
- Auto-failover: {{if .Failover}}{{.Failover.AutoFailover}}{{else}}false{{end}}
- Health check interval: {{if .Failover}}{{.Failover.HealthCheckInterval}}{{else}}10s{{end}}
- Quorum required: {{if .Failover}}{{.Failover.QuorumRequired}}{{else}}1{{end}}
- DNS plugin: {{if .Failover}}{{.Failover.DNSPlugin}}{{else}}none{{end}}
`
