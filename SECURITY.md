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
| `sudo gitlab-rails runner ...` | secondary | Set/clear repository_storages to suppress writes on the replica |
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

> Privacy consequence: sharing this key makes the secondary able to
> decrypt every credential the primary holds. See
> [`PRIVACY.md`](PRIVACY.md#the-secondary-is-not-a-lower-trust-environment).

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

## Network exposure

`syncctl` opens two listeners and makes outbound calls to each site's
`external_url`. None of them authenticate beyond what is described here.

| Listener | Default | Auth | Notes |
|---|---|---|---|
| `/metrics`, `/healthz` | `:9101` (all interfaces) | none | Exposes site names, replication lag, drift counters. Binds all interfaces by default and warns when it does — set `metrics.addr: 127.0.0.1:9101` and scrape over a private network. |
| `/webhook`, `/healthz` | `webhook.addr` | GitLab secret token | Serve over TLS. |

### Webhook receiver

GitLab sends the shared secret in the `X-Gitlab-Token` header on **every**
delivery. Over plain HTTP that token crosses the network in cleartext,
and anyone who observes it can trigger syncs at will. Either terminate
TLS in the receiver:

```yaml
webhook:
  addr: ":9102"
  secret_token: ${WEBHOOK_SECRET_TOKEN}
  tls_cert: /etc/syncctl/webhook.crt
  tls_key: /etc/syncctl/webhook.key
```

or put a TLS-terminating reverse proxy in front of it. Running the
receiver on plaintext HTTP logs a warning at startup.

The token is compared as a SHA-256 digest in constant time, so neither
its value nor its length is observable from response timing.

### external_url scheme

The API validator sends a GitLab personal access token in a
`PRIVATE-TOKEN` header to `<external_url>/api/v4/...`, and the registry
reconciler derives its endpoint the same way. Use `https`. An `http`
URL loads with a warning; any other scheme is rejected.

## No secrets in config files or logs

All secrets should be read from environment variables via `${VAR}`
expansion. The config loader rejects any `${VAR}` reference that is unset
or empty.

A **literal** secret in a field tagged `env:"required"` is reported as a
warning naming the field, and the load continues:

```
WARN primary.postgres.password must be an environment reference such as ${MY_SECRET}, not a literal value
WARN secrets are stored as literals in the config file; move them to
     environment variables — this will become a load error in the next major version
```

Refusing to load would break a running deployment on upgrade, and for a
disaster-recovery tool an unstartable binary is its own incident. The
rejection lands in the next major version; move your secrets to the
environment before then.

Secret values are never logged or printed.

## Data protection

[`PRIVACY.md`](PRIVACY.md) covers what personal data replication moves,
the cross-border implications of a secondary in another jurisdiction,
what reaches logs and metrics, and the absence of telemetry.
