# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Withdrawn releases

`v0.2.0` and `v0.2.1` were failed release attempts and carry no
artifacts. The `v0.2.0` tag stays valid for `go install`, but GitHub
permanently reserves a tag once its release has existed, so it can never
carry binaries. Everything intended for those versions ships in the next
release.

## [0.2.2](https://github.com/lknappich/syncctl/compare/v0.1.0...v0.2.2) (2026-08-05)


### ⚠ BREAKING CHANGES

* Prometheus metrics are renamed from geo_sync_* to syncctl_*. Existing alerts and dashboards must be updated:   geo_sync_pg_replay_lag_seconds       -> syncctl_pg_replay_lag_seconds   geo_sync_drift_total                 -> syncctl_drift_total   geo_sync_sync_duration_seconds       -> syncctl_sync_duration_seconds   geo_sync_last_sync_timestamp_seconds -> syncctl_last_sync_timestamp_seconds

### Bug Fixes

* **ci:** create the tag for the draft release before building ([8839414](https://github.com/lknappich/syncctl/commit/8839414c747b8da3f887da72f890c5ba2d4b82f0))
* **ci:** create the tag for the draft release before building ([527c7bf](https://github.com/lknappich/syncctl/commit/527c7bfeca9077a9011a03482a80f9b17167888c))
* **ci:** have release-please create the release as a draft ([4e9309c](https://github.com/lknappich/syncctl/commit/4e9309c2b95e3e4918ba167c2724fc47951558a1))
* **ci:** have release-please create the release as a draft ([e16cd1b](https://github.com/lknappich/syncctl/commit/e16cd1b2b187122f93460ec85a339185de687b70))
* **gitfetch:** correct hashed storage layout to documented SHA-256 form ([d705a4c](https://github.com/lknappich/syncctl/commit/d705a4c1e58f2392779cae16607a909bfbbe626a))
* **readonly,config:** stop describing write suppression as Maintenance Mode ([d0d1e47](https://github.com/lknappich/syncctl/commit/d0d1e479f5d3a0896e291df95f8951a48ada3e70))


### Refactoring

* **consistency,pgsetup,autorepair:** inject runners for test coverage ([3510afd](https://github.com/lknappich/syncctl/commit/3510afd4b061cc36d2e61750761fd581da98a263))
* **doctor:** inject Runner and PoolFactory for full test coverage ([e8449fb](https://github.com/lknappich/syncctl/commit/e8449fb056360aac26b45188ee34b6012f2b795b))
* **gitrsync,fsstorage,gitfetch:** inject localcmd.Runner for tests ([d0e0c32](https://github.com/lknappich/syncctl/commit/d0e0c329e9e9df2e42fa5344ac93e7cb14cb14ca))
* **postgres,objectstorage:** extract Querier and bucketLister interfaces ([a544588](https://github.com/lknappich/syncctl/commit/a544588c96c93c87f38837e0b3aa3370953c72a2))
* rename project to syncctl ([872a4dc](https://github.com/lknappich/syncctl/commit/872a4dcd1436528dc81800bbc433de78ba30cb3c))
* **sshexec:** introduce Runner interface for mockable SSH calls ([ac24e55](https://github.com/lknappich/syncctl/commit/ac24e551246c1359c75e48b482d49514e0e1a201))


### Documentation

* **agents,readme:** add trademark policy and disclaimer ([f901a7d](https://github.com/lknappich/syncctl/commit/f901a7d0f5ae9fef8224915288dbbb9defbcb43c))


### Chores

* reset release baseline to v0.1.0 and cut 0.2.2 ([2b0e0c0](https://github.com/lknappich/syncctl/commit/2b0e0c074f10e17386158825b5b9090fbe8c2034))

## [Unreleased]

### Fixed

- **PostgreSQL TLS**: Connections now default to `sslmode=require` instead of
  `sslmode=disable`. Passwords with special characters (spaces, quotes,
  backslashes) are safely encoded using libpq quoting rules.
- **Webhook security**: Empty secret token is now rejected at construction.
  Project paths are validated against path traversal (`../../`). Concurrency
  is capped (8 concurrent syncs) to prevent DoS. HTTP server timeouts prevent
  Slowloris attacks.
- **Metrics server**: Added ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout.
- **YAML env injection**: `${VAR}` placeholders are resolved after YAML parse
  via struct reflection, preventing injection of additional YAML keys via
  newline-containing env values.
- **Registry reconciler**: 401 Unauthorized is now treated as "skip" instead
  of reporting false drift.
- **pg_basebackup**: Removed duplicate `-S main` flag. Hardened
  `postgresql.auto.conf` editing to handle missing trailing newlines.
- **SSH host keys**: Centralized SSH execution in `internal/sshexec` package.
  `known_hosts_file` config field pins host keys with
  `StrictHostKeyChecking=yes`.
- **Failover safety**: Auto-failover logs a warning at config load time.
- **Consistency sweep**: 10% tolerance band on `reltuples` estimates prevents
  false drift from ANALYZE timing differences.

### Added

- **Bounded-parallel git fetch**: Worker pool (default 8, configurable via
  `SetMaxParallel`) materially improves sync time on large instances.
- **SECURITY.md**: Documents the sudo/SSH trust model, recommended sudoers
  allowlist, host-key verification, and db_key_base sharing rationale.
- **CODE_OF_CONDUCT.md**: Contributor Covenant 2.1.
- **CHANGELOG.md**: Keep-a-Changelog format.
- **Issue/PR templates**: GitHub issue templates and PR template.
- **Dependabot**: Weekly dependency updates for Go modules and GitHub Actions.
- **golangci-lint**: CI configuration with gosec, errcheck, govet, staticcheck,
  ineffassign, misspell, gofmt, revive.
- **Makefile**: `build`, `test`, `vet`, `lint`, `fmt`, `coverage`, `vuln`,
  `docker`, `release-snapshot` targets.
- **Dev config**: `deployments/dev/config.yaml` with `sslmode: disable` for
  local docker-compose stack.
- **docs/architecture.md**: Full architecture document.
- **Tests**: projectpath validation, sshexec config, config DSN encoding,
  pgsetup auto.conf editing, consistency tolerance.

### Changed

- Go toolchain aligned to 1.24 across `go.mod`, CI, Dockerfile, and docs.
- SLA report fields renamed from misleading `PGLagP50/PGLagP99` to honest
  `PGLagCurrent/PGLagPeak`. Component count derived dynamically from metrics.
- README updated from stale "Phase 0" status to reflect actual capabilities.
- CONTRIBUTING.md updated to reference Go 1.24.
