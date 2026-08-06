# Data Protection and Privacy

`syncctl` copies a GitLab installation — database, repositories, and
blobs — from one host to another, usually in a different place. A GitLab
database is one of the most personal-data-dense stores most
organisations run. This document says what that means in practice, so
that deploying `syncctl` is a decision you make with the facts rather
than one you discover afterwards.

[`SECURITY.md`](SECURITY.md) covers how the mechanism is secured. This
covers the data it moves.

> **This is not legal advice.** It is a description of what the software
> does with data, written by the people who wrote the software, so that
> you can take it to someone qualified. Where it names a regulation it is
> pointing at a question you should ask, not answering it. Data
> protection law varies by jurisdiction, by the categories of data you
> hold, and by your role as controller or processor — none of which this
> document knows. Get your own advice before relying on any of it.

## What replication copies

PostgreSQL replication here is **physical**: WAL streaming copies every
table, every row, every byte — including columns GitLab encrypts. There
is no filtering layer, and there is no way to exclude a table. At
minimum, the secondary receives:

| Category | Examples |
|---|---|
| Identity | Names, usernames, email addresses, public profile fields |
| Authentication | Password hashes, 2FA secrets and recovery codes |
| Credentials | Personal and project access tokens, deploy keys, CI variables |
| Integration secrets | Webhook secrets, third-party integration credentials |
| Network identifiers | Last and current sign-in IP addresses |
| Free text | Issue, merge request and note bodies, commit messages |
| Activity | Audit and event streams, CI job logs |

Plus, via `rsync`/`git fetch` and object storage: every repository's full
history, and all uploads, artifacts, LFS objects and packages.

Assume that anything a user has ever typed into GitLab is on the
secondary.

## The secondary is not a lower-trust environment

`syncctl` requires the secondary to share the primary's `db_key_base`.
This is not optional — without it the replica cannot decrypt its own
encrypted columns and is not a working GitLab. The security rationale is
in [`SECURITY.md`](SECURITY.md#db_key_base-sharing).

The privacy consequence deserves stating plainly: **the secondary can
decrypt every credential and secret the primary holds.** Access to the
secondary is equivalent to access to the primary. Give it the same
controls — network isolation, host access, audit, physical security. A
replica in a less-protected environment is a downgrade of your whole
security posture, not a backup.

## Moving data between jurisdictions

**Configuring a primary and a secondary in different countries moves
personal data between those countries.** In several jurisdictions that is
a regulated act in its own right, independent of how well the transfer is
encrypted. Whether it is regulated in yours, and on what terms, is a
question for your own advisers — the point here is that the software does
it, silently, as soon as the config says so.

The shipped [`deploy/config.example.yaml`](deploy/config.example.yaml)
pairs `eu-west-1` with `us-east-1`. That is an illustration of the
configuration format, **not a recommendation**. Copied literally it moves EU-resident personal data to the United States.
Our understanding is that this engages GDPR Chapter V and needs a lawful
transfer mechanism — an adequacy decision, standard contractual clauses,
or another Article 46 safeguard — but confirm that against your own
circumstances rather than against this file.

Before deploying across a border:

- Confirm with your own advisers that you have a lawful basis for the
  transfer, not merely a secure channel for it.
- Check any data-residency commitment you have made to customers or
  regulators. A replica in another region can breach a residency promise
  even if nobody ever reads from it.
- Record the secondary in your processing records. It is a second copy of
  the data, in a second place, and a subject access request covers it.

The lowest-friction answer is usually to keep the primary and secondary
in the same jurisdiction. `syncctl` is equally useful for
same-jurisdiction disaster recovery, and that deployment raises none of
the above.

## Deletion and erasure

Deletions propagate, but not instantly and not uniformly:

- **Database rows** replicate with WAL, typically within seconds. Lag is
  visible as `syncctl_pg_replay_lag_seconds`.
- **Repositories and blobs** are reconciled on the sweep interval
  (`sync.sweep_interval`, default 5 minutes) with `rsync --delete`, so a
  deleted object survives on the secondary until the next sweep.
- **Object storage** relies on your provider's replication for deletes;
  `syncctl` verifies parity rather than performing the copy.

When responding to an erasure request, treat the request as complete only
after a sweep has run and reported the component in sync. Nothing in
`syncctl` acknowledges an erasure as finished on your behalf, and nothing
here should be read as defining what "complete" means for your
obligations.

## What syncctl itself retains

**Nothing.** There is no control database and no local state directory.
Sweep history, drift state and failover records live in memory and are
lost on restart. Everything that outlives the process does so because you
configured somewhere for it to go:

**Logs.** Structured JSON on stderr. They may contain project and group
paths, repository paths, and site names — see
[Log contents](#log-contents). Their retention is entirely your logging
stack's.

**Metrics.** A Prometheus endpoint. Labels carry reconciler names, site
names from your config, table names from a fixed allowlist, and severity.
**No metric label ever carries a project, repository, user, path or other
identifier** — metrics are scraped continuously and frequently shipped to
third-party vendors, so they are held to a stricter rule than logs.

### Log contents

At default (`info`) level, logs may contain:

- Site names, external URLs, and SSH hostnames from your config
- Project and group paths, when a sync for a specific project fails
- Repository paths on disk

They do **not** contain repository content, database rows, user
identities, or any secret value. Command transcripts from `git` and
`rsync` are truncated and only emitted at `debug`.

Project and group paths are frequently confidential in themselves. If
yours are, or if your logs leave your infrastructure, set:

```yaml
log:
  redact_project_paths: true
```

Paths are then replaced with a stable short hash, so you can still
correlate failures across log lines without the names leaving your
infrastructure.

## No telemetry

`syncctl` does not phone home. It has no analytics, no crash reporting,
and no update check. Every outbound network connection goes to an address
you put in your own config file:

| Connection | Destination |
|---|---|
| PostgreSQL control and replication | `postgres.host` |
| SSH, `rsync`, `git fetch` | `ssh_host` |
| Object storage | `object_storage.s3.endpoint`, or your provider |
| Container registry | `registry.url` |
| Failover health checks | `external_url`, `failover.health_check_urls` |
| API validator | `external_url` |
| `syncctl sla` | your own metrics endpoint |

That list is exhaustive; there is no other network egress in the binary.

## Operator checklist

Before running `syncctl serve` in production:

- [ ] Primary and secondary are in a jurisdiction you are permitted to
      hold this data in — or a transfer mechanism is documented
- [ ] The secondary has host, network and access controls equivalent to
      the primary, because `db_key_base` makes it credential-equivalent
- [ ] The secondary appears in your processing records and data map
- [ ] Log retention for `syncctl` output is set deliberately, and
      `log.redact_project_paths` reflects whether project names may leave
      your infrastructure
- [ ] `/metrics` is not reachable from untrusted networks — loopback by
      default since 1.0, so confirm any widening of `metrics.addr` was
      deliberate (see [`SECURITY.md`](SECURITY.md#network-exposure))
- [ ] Your erasure process accounts for sweep lag on repositories and
      blobs

## Reporting a privacy concern

Use the same private channel as security reports — see
[`SECURITY.md`](SECURITY.md#reporting-a-vulnerability). Please do not
open a public issue for anything involving real personal data.
