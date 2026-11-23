# Dependency refresh and setup script — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring syncctl's toolchain, modules, actions and images current, then ship an idempotent `scripts/install.sh` that installs or updates syncctl on a GitLab secondary as a systemd service.

**Architecture:** One small PR per task, each merged after CI is green before the next starts (later tasks touch the same files). The setup script installs the release binary plus a systemd unit shipped in the release archive; configuration stays with `syncctl init`, secrets go to a root-only env file.

**Tech Stack:** Go 1.26/1.27, GitHub Actions, goreleaser v2, bash, systemd.

**Spec:** `docs/superpowers/specs/2026-11-23-dependency-refresh-and-setup-script-design.md`

## Global Constraints

- `go.mod` keeps `go 1.26.0`; CI, e2e and release build with `go-version: "1.27"` and `check-latest: true`.
- Dependency PRs use `fix(deps):`/`fix(build):`/`fix(ci):` so release-please cuts a release; `chore` is hidden.
- Actions stay pinned by full commit SHA with a version comment.
- No comments unless they explain *why* (AGENTS.md).
- Before each commit: `go build ./... && go vet ./... && go test ./...` and `gofmt -l .` empty.
- Each PR: branch from fresh `main`, one logical change, merge commit after green CI.
- Script: never runs `pg setup`, never touches a remote host, never writes secrets outside `/etc/syncctl/syncctl.env` (`root:root 0600`).

## Review Focus

- Re-running the script on a configured host must not prompt, overwrite config/env/known_hosts, or change their modes — only binary, unit and restart.
- Non-interactive stdin (piped, CI): every prompt takes its safe default (do not start a service after a failing `doctor`; never accept host keys unconfirmed).
- A secret containing `$`, quotes, spaces or `#` must round-trip through `syncctl.env` unchanged (systemd EnvironmentFile quoting).
- Checksum mismatch or a missing asset for the arch must abort before anything is installed.
- An older release without `syncctl.service` in its archive must fail with a clear message, not install a half setup.

---

### Task 1: Build with Go 1.27

**Files:** Modify `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/workflows/e2e.yml`

- [ ] Replace every `go-version: "1.26"` with `"1.27"`; ensure `check-latest: true` on each setup-go step.
- [ ] `go build ./... && go test ./...` locally with Go 1.27.
- [ ] Commit `fix(build): build CI and releases with Go 1.27`, PR, green CI, merge.

### Task 2: Update all Go modules

**Files:** `go.mod`, `go.sum`

- [ ] `go get -u ./... && go mod tidy`; confirm `go` directive still `1.26.0` (if a module forces higher, stop and report).
- [ ] Build, vet, test, `govulncheck ./...` (via `go run golang.org/x/vuln/cmd/govulncheck@v1.1.4`).
- [ ] Commit `fix(deps): update Go modules`, PR (supersedes Dependabot #177–#182), merge; Dependabot closes its PRs.

### Task 3: Replace archived yaml module

**Files:** the one `.go` file importing `gopkg.in/yaml.v3`, `go.mod`, `go.sum`

- [ ] Swap import to `go.yaml.in/yaml/v3` (same API), `go mod tidy`; `gopkg.in/yaml.v3` must no longer be a direct requirement.
- [ ] Tests green; commit `fix(deps): move to the maintained go.yaml.in/yaml/v3`, PR, merge.

### Task 4: Base image on Debian 13, pinned

**Files:** `Dockerfile`

- [ ] `FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5`.
- [ ] `goreleaser check`; commit `fix(build): base the image on distroless Debian 13, pinned by digest`, PR, merge.

### Task 5: Pinned syft install

**Files:** `.github/workflows/release.yml`

- [ ] Replace the `curl …/main/install.sh | sh` step: download `syft_1.57.2_linux_amd64.tar.gz` and `syft_1.57.2_checksums.txt` from the v1.57.2 GitHub release, `sha256sum --check --ignore-missing`, extract `syft` to `/usr/local/bin`.
- [ ] `go test ./internal/releasecheck/...`; commit `fix(ci): install syft from a pinned, checksummed release`, PR, merge.

### Task 6: Dependabot groups, docker ecosystems, weekly govulncheck

**Files:** `.github/dependabot.yml`, `.github/workflows/ci.yml`

- [ ] gomod: group `aws-sdk` (`github.com/aws/*`). Add `docker` (`/`) and `docker-compose` (`/deployments/dev`) ecosystems, weekly, ignoring `version-update:semver-major` for compose (Postgres major must match GitLab).
- [ ] ci.yml: add `schedule: - cron: "23 4 * * 1"` so `govulncheck` runs weekly.
- [ ] Commit `ci: group Dependabot updates, watch images, scan weekly`, PR, merge.

### Task 7: Pin the dev stack's Postgres

**Files:** `deployments/dev/docker-compose.yml`

- [ ] `postgres:16-alpine` → `postgres:16.16-alpine3.24` (both services). MinIO is left as is and reported: `minio/minio` is no longer publicly pullable; replacing it is the maintainer's choice.
- [ ] Commit `chore(dev): pin the dev stack's Postgres image`, PR, merge.

### Task 8: Merge open action bumps

- [ ] Rebase/recreate #163 and #164 if stale, wait for green CI, merge.

### Task 9: systemd unit in the release archive

**Files:** Create `deploy/syncctl.service`; modify `.goreleaser.yaml` (`archives[0].files`), `internal/releasecheck/release_config_test.go`

- [ ] Test `TestArchiveShipsSystemdUnit`: `.goreleaser.yaml` archive files list contains `deploy/syncctl.service`. Run → FAIL.
- [ ] Unit: `User=syncctl`, `Group=syncctl`, `EnvironmentFile=/etc/syncctl/syncctl.env`, `ExecStart=/usr/local/bin/syncctl serve -c /etc/syncctl/config.yaml`, `Restart=on-failure`, `RestartSec=10s`, `After=`/`Wants=network-online.target`, `ProtectSystem=full`, `ProtectHome=yes`, `PrivateTmp=yes`. No `NoNewPrivileges` (would block a local `sudo rsync`, which the ROADMAP 1.2 rsync fix may need). Add to archive files.
- [ ] `systemd-analyze verify deploy/syncctl.service` (warnings about missing binary acceptable), test PASS, `goreleaser check`.
- [ ] Commit `feat(deploy): ship a systemd unit with each release`, PR, merge.

### Task 10: install.sh — install and update

**Files:** Create `scripts/install.sh`, `scripts/install_test.sh`; modify `.github/workflows/ci.yml`

Interface: `install.sh [--version vX.Y.Z] [--archive PATH]`. The test runs the real script as root on the ephemeral CI runner (no test-only hooks) and refuses to run unless `CI=true`.

- [ ] `install_test.sh` builds an archive (`go build` + tar with `deploy/syncctl.service`), runs `install.sh --archive`; asserts `/usr/local/bin/syncctl version` works, unit at `/etc/systemd/system/syncctl.service`, user `syncctl` exists with `/var/lib/syncctl/.ssh/id_ed25519`; second run is idempotent (same key, same modes); archive without the unit → non-zero exit and binary not replaced; checksum mismatch → non-zero exit. Run → FAIL.
- [ ] Implement steps 1–4 and 9 of the spec: preflight, resolve version via `https://api.github.com/repos/lknappich/syncctl/releases/latest`, download archive + `syncctl_<v>_checksums.txt`, `sha256sum --check`, optional `gh attestation verify --repo lknappich/syncctl`, install binary+unit with `install -m`, `useradd --system --home-dir /var/lib/syncctl --create-home --shell /usr/sbin/nologin syncctl`, `ssh-keygen -t ed25519 -N ''` as that user, print pubkey + sudoers pointer, `systemctl daemon-reload`, restart if enabled.
- [ ] CI job `install-script`: `shellcheck scripts/*.sh` and `bash scripts/install_test.sh`.
- [ ] Commit `feat(install): add a setup script that installs and updates syncctl`, PR, merge.

### Task 11: install.sh — first-time configuration

**Files:** Modify `scripts/install.sh`, `scripts/install_test.sh`

- [ ] Tests: no config and stdin not a TTY → install completes, prints "run interactively to configure", service not enabled; pre-seeded config + env → no prompt, modes stay `0600 root:root`; secret `p@ss w$rd"#'\` written by the script reaches the process unchanged, checked with `systemd-run --pipe --wait -p EnvironmentFile=/etc/syncctl/syncctl.env printenv NAME` and with `bash -c 'set -a; . /etc/syncctl/syncctl.env; printenv NAME'`; doctor failing with non-TTY stdin → service not started.
- [ ] Implement spec steps 5–8 and 10: `syncctl init -o`, hidden prompts (`read -rs`) for each `${VAR}` in config (reject newlines), env file written via `umask 077` as `NAME="value"` with backslash, double quote, dollar and backtick each escaped by a backslash (same meaning for systemd and bash), `config-validate` run with that file sourced in a subshell, `ssh-keyscan` + `ssh-keygen -lf` fingerprints with explicit `yes`, append `ssh:\n  known_hosts_file: /etc/syncctl/known_hosts` if no top-level `ssh:`, `config-validate` aborts, `doctor` failure asks (default no), enable + start, final next-steps text with `pg setup` data-wipe warning.
- [ ] Commit `feat(install): configure a new secondary interactively`, PR, merge.

### Task 12: Publish the script and document it

**Files:** `.goreleaser.yaml` (`release.extra_files: [{glob: scripts/install.sh}]`), `README.md` Quickstart, `CONTRIBUTING.md` Go version line

- [ ] README Quickstart leads with: download `install.sh` from the latest release, read it, `sudo bash install.sh`; manual steps stay below.
- [ ] `goreleaser check`; commit `docs: install syncctl with the setup script`, PR, merge.
