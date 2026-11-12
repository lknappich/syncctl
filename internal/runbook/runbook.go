// Package runbook generates operational runbooks from the actual config,
// so they match the user's environment. Covers failover, re-attach,
// split-brain recovery, runner re-pointing, and secret rotation.
package runbook

import (
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/lknappich/syncctl/internal/config"
)

// Generate writes a Markdown runbook to w based on cfg.
func Generate(w io.Writer, cfg *config.Config) error {
	tmpl, err := template.New("runbook").Funcs(template.FuncMap{
		"pct": func(v float64) string { return fmt.Sprintf("%.1f%%", v*100) },
		"secondaryNames": func(c *config.Config) string {
			names := make([]string, len(c.Secondaries))
			for i, s := range c.Secondaries {
				names[i] = s.Name
			}
			return strings.Join(names, ", ")
		},
		"autoFailoverTarget": autoFailoverTarget,
	}).Parse(runbookTmpl)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, cfg)
}

// autoFailoverTarget mirrors the failover controller's choice so the
// runbook does not promise a promotion the controller will refuse: it
// never picks a secondary by config order.
func autoFailoverTarget(c *config.Config) string {
	if c.Failover != nil && c.Failover.PromoteSecondary != "" {
		return c.Failover.PromoteSecondary
	}
	if len(c.Secondaries) == 1 {
		return c.Secondaries[0].Name
	}
	return ""
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

{{with autoFailoverTarget .}}After 3 consecutive health check failures against
{{$.Primary.ExternalURL}}, the failover controller promotes {{.}}.
{{- else}}After 3 consecutive health check failures against
{{.Primary.ExternalURL}}, the failover controller declares the primary down
but promotes nothing: ` + "`failover.promote_secondary`" + ` is unset and it will
not choose a secondary by config order. Set it, or fail over manually.
{{- end}}

### Manual
{{if eq (len .Secondaries) 1}}
    syncctl failover --secondary {{(index .Secondaries 0).Name}} --yes
{{else}}
    syncctl failover --secondary <name> --yes

Replace <name> with the secondary to promote, one of: {{secondaryNames .}}.
syncctl will not choose one for you.
{{end}}
Rehearse it first with --dry-run in place of --yes. If the primary is
still answering health checks and you are promoting deliberately, add
--force — that is the split-brain case, so be sure.

### Post-failover checklist

1. Update DNS to point the primary hostname to the new primary's IP.
2. Re-point CI runners: update ` + "`config.toml`" + ` ` + "`url`" + ` to the new primary.
3. Update any external integrations (Slack, JIRA, etc.) with the new URL.
4. Verify webhook delivery by checking a sample project's webhook logs.
5. Verify access tokens work by making an authenticated API call.
{{- if gt (len .Secondaries) 1}}
6. Each secondary you did not promote (of {{secondaryNames .}}) still
   replicates from {{.Primary.Name}}; syncctl does not re-point it.
   Re-point each to the new primary, or re-base it with pg_basebackup.
{{- end}}

## 2. Role-Swap: Re-attach Old Primary as Secondary

When the old primary comes back online:

    syncctl adopt-as-secondary --old-primary-ssh {{or .Primary.SSHHost "<old-primary-host:port>"}}{{if gt (len .Secondaries) 1}} --new-primary <promoted-secondary>{{end}}

This runs pg_basebackup from the new primary and reconfigures the old
primary as a read-only secondary.
{{if gt (len .Secondaries) 1}}
With more than one secondary configured, ` + "`--new-primary`" + ` is required:
re-basing from the wrong site would replicate a stale replica over this
host. Pass the name of the secondary you promoted.
{{end}}

The old primary's PGDATA still holds its own cluster, and pg_basebackup
will not write into a populated directory. Back that cluster up, then
re-run with ` + "`--wipe-pgdata`" + ` to replace it.

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
