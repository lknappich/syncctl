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
| `/metrics`, `/healthz` | `127.0.0.1:9101` | none | Exposes site names, replication lag, drift counters. Loopback since 1.0; widening `metrics.addr` is deliberate and warns. |
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

### Container registry auth realm

The Docker Registry v2 flow answers an unauthenticated request with a 401
naming a token endpoint in its `WWW-Authenticate` header — a value the
responding server controls. Fetching it unconditionally would let a
malicious or compromised registry make `syncctl` issue arbitrary requests
from a host that holds Postgres replication credentials, SSH access to
both sites, and object-storage keys.

A realm is honoured only when its host is one the operator configured —
`registry.url`, the site's `external_url`, or an explicit
`registry.auth_realm` — and only over `https` unless the registry itself
is plain `http`. Redirects are checked against the same set, so an
approved host cannot bounce the request onward.

### external_url scheme

The API validator sends a GitLab personal access token in a
`PRIVATE-TOKEN` header to `<external_url>/api/v4/...`, and the registry
reconciler derives its endpoint the same way. Use `https`. An `http`
URL loads with a warning; any other scheme is rejected.

## No secrets in config files or logs

All secrets should be read from environment variables via `${VAR}`
expansion. The config loader rejects any `${VAR}` reference that is unset
or empty.

A **literal** secret in a field tagged `env:"required"` is rejected at
load, naming every offending field:

```
primary.postgres.password must be an environment reference such as ${MY_SECRET}, not a literal value

Move these values to environment variables and reference them as ${VAR},
or pass --allow-literal-secrets (or SYNCCTL_ALLOW_LITERAL_SECRETS=1) while you migrate
```

This was a warning throughout 0.2.x, which stated the rejection would
land in the next major version. It has.

`--allow-literal-secrets` restores the warning for operators mid-migration.
It logs on every load, so the state cannot be forgotten. It is not
intended to be permanent.

Secret values are never logged or printed.

## Verifying a release

Release archives carry SLSA build provenance: a signed statement, issued
by GitHub's Sigstore instance during the release workflow, that a given
file was produced by this repository at a specific commit. It is the
answer to "did this binary really come from syncctl, built from the
source it claims?".

```sh
gh attestation verify syncctl_1.0.0_linux_amd64.tar.gz --repo lknappich/syncctl
```

That checks the artifact against the attestation without a key to
distribute or rotate — the signing identity is the workflow's own OIDC
token, so a forged artifact would need a signature from this repository's
release job.

Then confirm the archive matches the published checksum file, which is
attested alongside it:

```sh
sha256sum -c syncctl_1.0.0_checksums.txt --ignore-missing
```

An SBOM is published with each archive.

Container images carry the same provenance, attested by digest and stored
alongside the image in the registry:

```sh
gh attestation verify oci://ghcr.io/lknappich/syncctl:1.0.0 --owner lknappich
```

The attestation is bound to the image digest rather than its tag, so it
stays valid for the exact bytes that were published even if a tag is
later repointed.

## Data protection

[`PRIVACY.md`](PRIVACY.md) covers what personal data replication moves,
the cross-border implications of a secondary in another jurisdiction,
what reaches logs and metrics, and the absence of telemetry.
