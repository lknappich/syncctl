# syncctl

`syncctl` is an open-source tool that performs **full one-to-one
geo-replication** between self-hosted GitLab instances — a primary and one
or more secondaries — using only public, documented interfaces and standard
infrastructure tooling. It is **not** GitLab Geo (a paid Premium/Ultimate
feature) and contains no GitLab Enterprise Edition code. This is a
clean-room, independent implementation licensed under Apache-2.0.

## Status

**Active — all core replication paths are implemented and functional.**

| Component | Status | Description |
|---|---|---|
| PostgreSQL streaming | ✅ | Physical WAL streaming via `pg_basebackup` + `pg_stat_replication` lag monitoring |
| Git data (rsync) | ✅ | `rsync --delete --checksum` of the repositories tree |
| Git data (fetch) | ✅ | Per-project `git fetch --prune +refs/*:refs/*` with bounded-parallel workers |
| Object storage (S3) | ✅ | Cross-region replication verification (count + size parity) |
| Object storage (FS) | ✅ | `rsync` of uploads/artifacts/LFS/packages/registry dirs |
| Registry | ✅ | Docker Registry v2 API catalog/tag diff (skips on 401) |
| Consistency sweep | ✅ | Row-count comparison + `git fsck` sampling with tolerance band |
| API validator | ✅ | Optional observational count-diff between primary/secondary |
| Webhook receiver | ✅ | Immediate per-project sync with debouncing + concurrency cap |
| Failover | ✅ | Health-check loop + human-gated promotion + role-swap |
| Doctor | ✅ | Prerequisite checks (SSH, PG, replication, db_key_base, tools) |
| Init wizard | ✅ | Interactive `syncctl init` config generator |
| Runbook | ✅ | Markdown runbook generation from config |
| SLA report | ✅ | RPO/RTO summary from Prometheus metrics |

### Limitations / non-goals

- **Redis** is not replicated. The secondary runs its own empty Redis.
  Cache/queue state is not a source of truth and would risk
  double-execution on promotion.
- **Sidekiq** is not replicated. In-flight jobs at the moment of primary
  failure are the only accepted RPO loss. Sidekiq is paused on the
  secondary while in read-only mode.
- **Auto-repair** for S3 objects is deferred to Phase 2 (cloud provider
  replication is the primary path; our tool verifies parity).
- **DNS failover** plugins (route53, cloudflare) are stubbed; manual DNS
  is the default.

## Why

GitLab's built-in Geo is a paid feature. If you operate your own GitLab
servers and want a true 1:1 replica for disaster recovery, geographic
read-scaling, or migration, you should be able to build one with the same
standard tools you already use to run Postgres and object storage:
physical WAL streaming, `rsync`/`git fetch`, and bucket replication. This
project orchestrates those tools with observability, idempotent
reconciliation, and a safe failover flow.

## Architecture

The primary replication path is **infrastructure-level** because only it
can achieve a true byte-identical replica:

- **PostgreSQL** — physical streaming replication (`pg_basebackup` +
  WAL receiver). Copies every table, ID, timestamp, and encrypted column.
- **Git data** — either `rsync` of `/var/opt/gitlab/git-data/repositories`
  or per-project `git fetch --prune +refs/*:refs/*`, driven by the
  project list already present in the replicated DB.
- **Object storage** — S3 cross-region replication (or `rsync` for
  disk-backed uploads/artifacts/LFS/packages/registry blobs).
- **Encrypted columns** — the secondary shares the primary's
  `db_key_base` so it can decrypt webhook secrets, access tokens, 2FA
  seeds, etc. You own both servers, so this is legitimate; it is the
  one secret that must be shared across sites.
- **Redis** — the secondary runs its own empty Redis; not replicated.
- **Sidekiq** — not replicated; in-flight jobs are the only accepted RPO loss.

See [`docs/architecture.md`](docs/architecture.md) for the full design
and the honest limits of each strategy.

## Quickstart

```sh
# 1. Generate a config via the interactive wizard.
syncctl init -o config.yaml

# 2. Export the required environment variables (secrets are env-only).
export PG_CTRL_PASSWORD=...
export PG_REPL_PASSWORD=...
export SEC_REPL_PASSWORD=...
export S3_AK=...
export S3_SK=...

# 3. Validate config and check prerequisites.
syncctl config-validate -c config.yaml
syncctl doctor -c config.yaml

# 4. Bootstrap the secondary's PostgreSQL as a streaming replica.
syncctl pg setup --secondary <name> --data-dir /var/opt/gitlab/postgresql/data

# 5. Verify db_key_base parity between primary and secondary.
syncctl dbkey -c config.yaml

# 6. Start the sync engine (reconcilers + metrics + webhook + failover).
syncctl serve -c config.yaml
```

## Commands

| Command | Description |
|---|---|
| `syncctl version` | Print build version |
| `syncctl init` | Interactive config wizard |
| `syncctl config-validate` | Load and validate config, print summary |
| `syncctl doctor` | Check prerequisites on primary and secondary |
| `syncctl pg setup` | Bootstrap secondary via `pg_basebackup` |
| `syncctl pg status` | Show PostgreSQL replication lag |
| `syncctl sync` | Run one reconciliation sweep |
| `syncctl dbkey` | Verify `db_key_base` parity |
| `syncctl failover` | Promote a secondary to primary (human-gated) |
| `syncctl adopt-as-secondary` | Role-swap old primary to secondary |
| `syncctl runbook` | Generate operational runbook from config |
| `syncctl sla` | Print RPO/RTO summary scraped from a running `syncctl serve` |
| `syncctl serve` | Run sync engine (reconcilers + metrics + webhook + failover) |

## Security

See [`SECURITY.md`](SECURITY.md) for the full security policy, including:
- SSH host-key verification and sudo trust model
- PostgreSQL TLS defaults (`sslmode=require`)
- `db_key_base` sharing rationale
- Private vulnerability disclosure process

## Clean-room policy

See [`AGENTS.md`](AGENTS.md). No contributor may have read GitLab EE
source for the Geo feature, and no non-public endpoints or internal
binaries are used. All replication uses documented Postgres schema,
standard WAL/rsync/git protocols, and public APIs only.

## Development

```sh
make build      # go build ./...
make test       # go test ./...
make vet        # go vet ./...
make lint       # staticcheck ./...
make fmt        # gofmt -w .
make fmt-check  # gofmt -l . (CI gate)
make coverage   # go test -race -coverprofile=coverage.txt ./...
make vuln       # govulncheck ./...
```

Requires Go 1.24+. A `docker-compose` dev stack is provided under
`deployments/dev` for end-to-end testing.

## License

Apache-2.0. See [`LICENSE`](LICENSE).

## Trademarks

GitLab is a registered trademark of GitLab B.V. This project is not
affiliated with, endorsed by, or sponsored by GitLab B.V. References to
GitLab describe the software this tool interoperates with.