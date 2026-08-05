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
