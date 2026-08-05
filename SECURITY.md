# Security Policy

## Supported versions

Only the latest released version is supported. Security fixes are
backported to the current release branch only.

## Reporting a vulnerability

Report security vulnerabilities privately via GitHub's built-in
private vulnerability reporting:

  https://github.com/lknappich/syncctl/security/advisories/new

Do not open a public issue. Include:
- A description of the vulnerability and its impact
- Steps to reproduce (proof of concept if possible)
- Suggested fix (optional)

You will receive a response within 72 hours. Responsible disclosure is
appreciated — we will credit reporters in release notes.

## SSH and sudo trust model

`syncctl` executes SSH commands on the primary and secondary GitLab hosts.
Some of these commands use `sudo` to perform privileged operations.
The following table enumerates every `sudo` command the tool runs:

| Command | Host | Purpose |
|---|---|---|
| `sudo gitlab-ctl deploy-registry-readonly start/stop` | secondary | Enable/disable registry read-only mode |
| `sudo gitlab-ctl sidekiq pause/resume` | secondary | Pause/resume background job processing |
| `sudo gitlab-ctl stop/start` | secondary/old-primary | Stop/start GitLab services during failover |
| `sudo -u gitlab-psql pg_ctl promote` | secondary | Promote standby to primary during failover |
| `sudo -u gitlab-psql pg_basebackup` | old-primary | Re-bootstrap as secondary after role-swap |
| `sudo gitlab-rails runner ...` | secondary | Set/clear repository_storages for maintenance mode |
| `sudo rsync` | primary (remote) | Read git-owned files during rsync |
| `sudo grep` | both | Read root-owned secrets.yml for db_key_base parity check |

### Recommended sudoers configuration

Configure a locked-down sudoers allowlist on each host rather than
granting blanket `NOPASSWD: ALL`. Example for the secondary:

```
syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl deploy-registry-readonly *
syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl sidekiq *
syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl stop
syncctl ALL=(root) NOPASSWD: /usr/bin/gitlab-ctl start
syncctl ALL=(gitlab-psql) NOPASSWD: /opt/gitlab/embedded/bin/pg_ctl promote *
syncctl ALL=(gitlab-psql) NOPASSWD: /opt/gitlab/embedded/bin/pg_basebackup *
syncctl ALL=(root) NOPASSWD: /opt/gitlab/embedded/bin/gitlab-rails runner *
```

### Host key verification

By default `syncctl` uses `StrictHostKeyChecking=accept-new` (TOFU). For
production deployments, pin host keys by setting:

```yaml
ssh:
  known_hosts_file: /etc/syncctl/known_hosts
```

This switches the default to `StrictHostKeyChecking=yes`, refusing
connections to hosts whose key is not in the pinned known_hosts file.

## db_key_base sharing

The `db_key_base` is GitLab's Rails secret used to encrypt webhook
secrets, access tokens, 2FA seeds, and other sensitive columns. For a
true 1:1 replica, the secondary must share the primary's `db_key_base`
so the GitLab application itself can decrypt these columns on the
secondary.

This tool copies and verifies the key parity via SSH (`syncctl dbkey`).
It never decrypts anything — it only compares the key bytes. Sharing
`db_key_base` across sites is legitimate when you own both servers and
is the only way to achieve a functional 1:1 replica.

## PostgreSQL TLS

PostgreSQL connections default to `sslmode=require`. Setting
`sslmode: disable` logs a warning and should only be used for local
development. For production, use `verify-ca` or `verify-full` with a
pinned CA certificate:

```yaml
postgres:
  sslmode: verify-full
  ssl_root_cert: /etc/ssl/certs/pg-ca.pem
```

## No secrets in config files or logs

All secrets are read from environment variables via `${VAR}` expansion.
The config loader rejects any `${VAR}` reference that is unset or empty.
Secret values are never logged or printed.