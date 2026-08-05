# Architecture

`syncctl` performs **full one-to-one geo-replication** between
self-hosted GitLab instances using only standard infrastructure tooling
and public, documented interfaces. It is a clean-room, independent
implementation — not GitLab Geo and not derived from GitLab EE source.

## Replication paths

### PostgreSQL — physical WAL streaming

The primary replication path for the database is PostgreSQL's native
physical streaming replication:

1. `syncctl pg setup` runs `pg_basebackup` against the primary to seed
   the secondary's data directory.
2. The secondary's WAL receiver connects to the primary using a
   `REPLICATION`-privileged role and a named replication slot.
3. `syncctl serve` observes lag via `pg_stat_replication` (on the
   primary) and `pg_is_in_recovery()` (on the secondary).

This copies every table, ID, timestamp, and encrypted column
byte-for-byte. No application-level logic is involved.

### Git data

Two modes, selected by `primary.git.mode`:

- **rsync** — `rsync` of `/var/opt/gitlab/git-data/repositories` from
  primary to secondary. Requires SSH access to the primary's
  filesystem. Byte-identical copy.
- **fetch** — per-project `git fetch --prune +refs/*:refs/*` driven by
  the project list already present in the replicated DB. Used when
  the primary's filesystem is not directly accessible (different cloud
  provider, etc.). The project list and disk-path mapping are derived
  from the public GitLab CE schema (`projects` + `routes` tables).

### Object storage

- **S3** — cross-region replication is performed by the cloud
  provider (AWS, MinIO, etc.). `syncctl` verifies the replica bucket's
  object count and total size matches the primary, surfacing drift if
  replication lag exceeds the configured threshold.
- **FS** — `rsync` of uploads, artifacts, packages, LFS, and registry
  blob directories.

### Encrypted columns

The secondary shares the primary's `db_key_base` so the GitLab
application itself can decrypt webhook secrets, access tokens, 2FA
seeds, etc. `syncctl dbkey` verifies parity. Our tool never decrypts
anything — it only copies and compares the key bytes.

### Redis

The secondary runs its own empty Redis. Cache/queue state is not a
source of truth and replicating it would risk double-execution on
promotion.

### Sidekiq

Not replicated. In-flight jobs at the moment of primary failure are
the only accepted RPO loss. The secondary's Sidekiq is paused while in
read-only mode.

## Control plane

`syncctl` is the single binary that orchestrates all of the above:

| Command | Purpose |
|---|---|
| `serve` | Run reconcilers + metrics + webhook + failover loop |
| `pg setup` | Bootstrap a secondary via `pg_basebackup` |
| `pg status` | One-shot PG lag report |
| `sync` | Run one sweep of all reconcilers |
| `dbkey` | Verify `db_key_base` parity |
| `failover` | Promote a secondary (human-gated) |
| `adopt-as-secondary` | Role-swap old primary → secondary |
| `runbook` | Generate Markdown runbook from config |
| `sla` | Print RPO/RTO summary from metrics |
| `doctor` | Prerequisite checks |
| `init` | Interactive config wizard |

## Reconciler model

Each replication component is a `reconciler.Reconciler` — an idempotent
function that observes state and performs repairs when safe. The runner
sweeps all reconcilers on a configurable interval (default 5m). A
single reconciler failure does not abort the others.

Webhooks can trigger near-real-time per-project git fetch outside the
sweep interval, with debouncing to coalesce push bursts.

## Failover

Human-gated by default. The failover controller monitors primary health
via HTTP checks. After 3 consecutive failures (configurable quorum), the
primary is declared down. Promotion runs the sequence: stop services →
`pg_ctl promote` → disable read-only → start services → verify
`db_key_base` parity.

Auto-failover is opt-in and dangerous.

## What this is NOT

- Not GitLab Geo. No GitLab EE code is used or reimplemented.
- Does not read, decompile, or introspect GitLab EE source or binaries.
- Does not use any non-public API endpoint.
- Does not decrypt encrypted columns itself (the GitLab app does that).

See [`AGENTS.md`](../AGENTS.md) for the full clean-room policy.