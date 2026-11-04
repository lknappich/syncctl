# Roadmap

What `syncctl` should do next, in priority order. Reviewed on 2026-11-03
against `v1.1.0` (`087b10d`); line references point at that commit. The
order is a priority, not a schedule.

All unit tests pass, but no test runs against a real GitLab or Postgres, and
several core paths contradict GitLab's public docs and CE schema. So the first
milestone makes the existing promises true before adding new ones.

Each finding is marked:

- **Verified**: shown by the code, the public CE schema, or a test run.
- **Confirm**: follows from public docs, but not yet run on a real install.

All of this work stays inside the clean-room policy in
[`AGENTS.md`](AGENTS.md): public docs, the CE schema and standard tooling only.

## 1.1.1: Security and dependency refresh

Go 1.25 no longer receives fixes, so CI cannot pass `govulncheck` on it.
Newer modules also require Go 1.26, so Dependabot's PRs cannot go green one at
a time.

- Require Go 1.26 and build with Go 1.27. This resolves GO-2026-6809 and
  GO-2026-6617 in the standard library.
- Update all modules, which resolves GO-2026-6629 in `golang.org/x/text`.
- Update pinned actions, CI tools and the base image.
- Land it as `fix(deps):`. The release config hides `chore`, so a `chore:`
  commit would not produce a release, and the published binaries need these
  fixes.

**Done when:** a release built with Go 1.27 is published and `govulncheck` is
clean on `main`.

## 1.2: Make the core path true

**Goal:** a replica and a failover drill work on real GitLab CE, and CI proves
it on every run.

### 1. End-to-end test against real GitLab CE

Everything is mocked today, which is how the findings below passed a green
test suite. This comes first because it validates all the other items.

**Done when:** a scheduled workflow runs `pg setup`, `sync`, `failover` and
`adopt-as-secondary` on two Omnibus CE instances, for the current and the
previous CE release.

### 2. Correct the failover sequence

- **Confirm:** `gitlab-ctl stop` stops all components before `pg_ctl promote`
  runs, and promoting needs a running server
  ([`failover.go:267-274`](internal/failover/failover.go)). Stop only the
  application services.
- **Confirm:** `gitlab-ctl deploy-registry-readonly` and `gitlab-ctl sidekiq
  pause|resume` are not in GitLab's public docs
  ([`readonly.go:39-40,59-60`](internal/readonly/readonly.go)). They run on
  every promotion ([`failover.go:275`](internal/failover/failover.go)) and at
  `serve` startup with `read_only_secondary`, which the wizard offers as the
  default ([`initcmd.go:98`](internal/initcmd/initcmd.go)). Use the documented
  registry `maintenance.readonly` setting and `gitlab-ctl stop sidekiq`.
- **Verified:** `ApplicationSetting.current.update!` runs against a read-only
  standby, and `2>/dev/null || true` hides the failure
  ([`readonly.go:41,61`](internal/readonly/readonly.go)). Remove it.
- **Verified:** auto-failover re-runs promotion on every tick while the primary
  stays down: 8 ticks produced 6 promotion sequences in a dry run
  ([`failover.go:105-116`](internal/failover/failover.go)). Promotion must run
  once.
- **Verified:** `syncctl failover` without `--secondary` promotes the first
  configured secondary ([`main.go:545-547`](cmd/syncctl/main.go)). Auto-failover
  already refuses to choose by config order, and this path should too.
- **Verified:** the post-failover runbook prints `adopt-as-secondary
  --secondary`, a flag that does not exist
  ([`failover.go:249`](internal/failover/failover.go)).
- **Verified:** the role-swap bootstrap sets no slot and no `application_name`
  ([`failover.go:318-325`](internal/failover/failover.go)), so the new pair is
  unprotected and unmonitored.
- **Verified:** other secondaries keep following the old primary after a
  promotion. Re-point them, or say so in the runbook.
- **Verified:** a failed step stops the sequence with services down and no way
  to resume. Make the steps idempotent and resumable.

**Done when:** the end-to-end drill promotes, serves writes and role-swaps, and
re-running any step after a failure is safe.

### 3. Make monitoring report the truth

- **Verified:** the lag query filters on the site name
  ([`postgres/reconciler.go:143`](internal/reconciler/postgres/reconciler.go)),
  but `pg setup` sets `application_name` to `syncctl` or the slot name
  ([`config.go:789`](internal/config/config.go),
  [`pgsetup.go:101`](internal/pgsetup/pgsetup.go)). After a standard bootstrap
  the row is not found and lag stays at `-1`. The testing guide documents a
  manual workaround; the bootstrap should get it right.
- **Verified:** `pg setup` reads `slot_name` from the secondary, while the
  example config sets it only on the primary, so following the example creates
  no slot ([`main.go:413`](cmd/syncctl/main.go),
  [`config.example.yaml:26`](deploy/config.example.yaml)).
- **Verified:** `lag_warning_threshold` and `lag_critical_threshold` are never
  evaluated, and the Postgres reconciler reports OK at any lag
  ([`postgres/reconciler.go:105-125`](internal/reconciler/postgres/reconciler.go)).
  Implement them or remove them.
- **Verified:** nothing watches replication slots. A stalled secondary with a
  slot retains WAL until the primary's disk fills, so the tool can take down
  the site it protects. Add slot metrics and a `max_slot_wal_keep_size` check
  to `doctor`.
- **Verified:** `ci_builds` and `ci_pipelines` are now the partitioned tables
  `p_ci_builds` and `p_ci_pipelines`. The sweep only matches `relkind = 'r'`
  and treats no row as 0, so both always compare as `0 == 0`
  ([`consistency/reconciler.go:37,145-150`](internal/reconciler/consistency/reconciler.go)).
- **Verified:** the sweep compares `pg_class.reltuples`, and `pg_class` is
  itself physically replicated, so a difference shows lag at most and never
  real divergence. Compare a signal that can differ, such as `max(id)` at a
  known replay LSN, or sampled `pg_amcheck`.

**Done when:** the end-to-end test stalls a replica and breaks a table, and
`syncctl` reports both.

### 4. Make fetch mode work, or label it experimental

- **Verified:** both queries select `projects.hashed_storage`, which is not in
  the CE schema; the column is `storage_version`
  ([`gitfetch/reconciler.go:174,362`](internal/reconciler/gitfetch/reconciler.go)).
  Every sweep and webhook fetch fails on a real database.
- **Verified:** `git -C <path> fetch` needs an existing repository and nothing
  creates one, so new projects never arrive (`:306`). Deleted projects are
  never removed.
- **Verified:** wikis, design repositories, snippets and object pools are not
  handled, which contradicts "full one-to-one" in the README.
- **Verified:** `projects.id` is `bigint` but is read as `int32` (`:156`). The
  remote path is hard-coded instead of using `repos_path` (`:303`), and the
  storage shard is ignored (`:230`).

**Done when:** the end-to-end test replicates every repository type in fetch
mode. Until then, the README and a startup warning mark it experimental.

### 5. Check all secrets, not only `db_key_base`

GitLab's backup docs require keeping `/etc/gitlab/gitlab-secrets.json` as a
whole and warn that 2FA users cannot sign in after losing the secrets file.
`syncctl` compares one key ([`dbkey.go`](internal/dbkey/dbkey.go)).

**Done when:** `doctor` and `failover` compare every key by digest, never
printing values and never decrypting, and `doctor` reports differing SSH host
keys.

### 6. Check version parity before replicating

Nothing compares GitLab versions or Postgres major versions between sites,
though physical replication requires the same Postgres major version.

**Done when:** `doctor` and `failover` fail on a mismatch, and the docs include
a supported-versions table and an upgrade runbook for a replicated pair.

### 7. Ship a container image that can run

`distroless/static` contains no `ssh`, `rsync`, `git` or `pg_basebackup`, and
`syncctl` shells out to all of them
([`sshexec.go:220`](internal/sshexec/sshexec.go),
[`pgsetup.go:44`](internal/pgsetup/pgsetup.go)). The dev stack has its own
problems (**Confirm**; Docker was not available for this review): `build: .`
points at a directory with no Dockerfile, and overriding the entrypoint runs
`postgres` as root ([`docker-compose.yml:20,40`](deployments/dev/docker-compose.yml)).

**Done when:** the image runs `doctor` and `sync` against the end-to-end
environment and the dev stack starts from a clean checkout. Otherwise, document
the image as unsupported and stop publishing it.

### 8. Align the docs with the code

- Change the README's "all core replication paths are implemented and
  functional" and its status table to match what the end-to-end test proves.
- `dns_plugin` accepts `route53` and `cloudflare` and then does nothing. Reject
  those values until they are implemented.
- `architecture.md` says reconcilers repair drift (`:87`), treats the fixed
  count of 3 failures as the quorum (`:97`), and describes an outdated promotion
  order (`:99`).
- `testing-guide.md` says `:9101` although the default is loopback (`:186`), and
  says `--yes` *or* `failover_enabled` although both are required (`:221`).
- `SECURITY.md` claims to list every `sudo` command but omits `ls`, `rm`, `mkdir`
  and `tee`.
- `README.md:163` and `AGENTS.md:86` name `staticcheck`, but CI runs
  `golangci-lint`.
- `CONTRIBUTING.md` says the release pipeline runs `govulncheck` (`:40`) and that
  the dev stack includes GitLab (`:25`). Neither is true.

## 1.3: Operate it with confidence

**Goal:** an operator can run this unattended and trust what it reports.

- **Fencing and a witness.** The old primary is not fenced, and health is judged
  from one vantage point with a default quorum of 1. Until both are solved, mark
  `auto_failover` experimental.
- **Role awareness.** After a failover the config still names the old primary,
  so `serve` monitors the wrong direction until someone edits it.
- **Planned switchover.** Add `syncctl switchover`: stop writes, wait for the
  replica to reach the primary's LSN, then promote with no data loss. Drills and
  migrations need this more than an emergency path.
- **Persistent state.** Add a local journal for failover steps and drift history.
  Resuming a failover needs it, and the "keeps no state" statements in the README
  and `PRIVACY.md` must change with it.
- **Alerts and dashboards.** There are four metrics and no shipped rules. Add
  failover state, slot health and per-component status, plus Prometheus alert
  rules and a dashboard.
- **Notifications.** Send a generic webhook on primary-down and on each promotion
  step.
- **Readiness.** `/healthz` always returns 200
  ([`metrics.go:96`](internal/metrics/metrics.go)). Separate liveness from
  readiness.
- **Sweep isolation.** Reconcilers run one after another, so a slow `rsync`
  delays lag monitoring ([`runner.go:70-97`](internal/reconciler/runner.go)). Give
  each its own schedule and timeout.
- **Cheaper, stronger checks.** `rsync --checksum` reads all data on every sweep,
  and S3 verification lists both buckets in full and compares only count and
  size. Add periodic deep checks and sampled ETag comparison.
- **DNS hook.** Add a generic `on_promote` hook first; vendor plugins can build on
  it.
- **Packaging and scripting.** Add a systemd unit, `.deb`/`.rpm` packages, and
  `--output json` for `sync`, `doctor` and `pg status`.
- **Test gates.** `cmd/syncctl` is at 30.7% coverage and `internal/failover` at
  65.1%, the most critical path among the least covered. The CI coverage step
  also ignores failures through `|| true` (`ci.yml:42`).

## Later

- Auto-repair of detected drift, with dry-run and limits (the deferred "Phase 2").
- External Postgres and Patroni, Gitaly Cluster, and Helm-based installs.
- Cascading replication topologies.

## Keeping dependencies healthy

This can happen alongside any milestone. Dependabot PRs sat open for almost three
months while known vulnerabilities went unnoticed, and these items stop that from
recurring.

- **Scan on a schedule.** `govulncheck` runs only on pushes and PRs, so a quiet
  repository finds nothing. Add a weekly run.
- **Group Dependabot updates.** The AWS SDK modules have to move together, but
  arrive as four separate PRs. Also add the `docker` and `docker-compose`
  ecosystems.
- **Protect `main`.** It has no branch protection, so CI can be bypassed.
- **Harden the release job.** It pipes `install.sh` from syft's `main` branch into
  `sh` while holding `contents: write` and `id-token: write`
  ([`release.yml:49`](.github/workflows/release.yml)). This conflicts with the
  SHA-pinning policy in `ci.yml`. Use a pinned installer or a verified checksum.
- **Pin what still floats.** Pin the base image by digest, the goreleaser version
  (`~> v2`), and `minio/minio:latest`.
- **Build releases on the newest patch.** `setup-go` prefers the runner's tool
  cache unless `check-latest` is set. Runner images do not ship Go 1.27 yet, so
  `"1.27"` resolves to the newest patch today, but once they do, a release can
  build on a stale one. Set `check-latest: true` in `release.yml`.
- **Replace `gopkg.in/yaml.v3`.** Its upstream is archived. Move to the maintained
  `go.yaml.in/yaml/v3`.

## Non-goals

These come from the project's policy and design, and stay out of scope:

- Anything that needs GitLab EE source, or endpoints outside the public docs.
- Decrypting data in `syncctl`. The GitLab application does that.
- Using API writes as the replication path.
- Replicating Redis or Sidekiq state.
