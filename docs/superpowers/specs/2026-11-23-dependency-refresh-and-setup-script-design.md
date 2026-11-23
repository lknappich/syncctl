# Dependency refresh and setup script — design

Date: 2026-11-23. Status: approved by the maintainer in conversation.

## Intent

Two things, in this order:

1. Bring the project to current versions (Go toolchain, modules, actions,
   images), following "Keeping dependencies healthy" and the 1.1.1 section of
   `ROADMAP.md`.
2. Give an **administrator** one script that takes a GitLab secondary from
   nothing to a running, supervised `syncctl serve`, and that also updates an
   existing install to a newer release.

Broader product improvements are already prioritised in `ROADMAP.md`; this
work does not reopen them. The script is the "systemd unit" part of the
roadmap's 1.3 "Packaging and scripting" item.

## Goals

- Every dependency on its newest compatible version; CI and releases built
  with Go 1.27 while `go.mod` keeps requiring Go 1.26.
- An admin can install, configure and start syncctl on a secondary with one
  script, and re-run the same script to update.
- Work lands as many small, reviewable PRs (maintainer's explicit wish).

## Non-goals

- Docker Swarm support. Discarded; the local WIP for it is stashed, not
  committed.
- A container image that can run the SSH/rsync/git paths (ROADMAP 1.2 item 7).
- `.deb`/`.rpm` packages, `--output json` (rest of ROADMAP 1.3).
- Fixing the unprivileged local rsync write (ROADMAP 1.2; listed in
  `test/e2e/known_failures.txt`). The script installs the intended model
  (unprivileged `syncctl` user) and does not paper over that bug.

## Anti-goals

- The script must never run `pg setup` on its own: it wipes the secondary's
  PostgreSQL data directory.
- The script must never change the primary or any other remote host.
- Secrets never land in `config.yaml`, in shell history, or in a
  world-readable file.

## Approaches considered (setup script)

- **A. Release binary + systemd (chosen).** Uses the host's `ssh`, `rsync`,
  `git` and `pg_basebackup`, so every code path works. Linux with systemd only.
- **B. Docker/Swarm.** Needs a fat image with ssh/rsync/git/postgres client,
  SSH keys and `known_hosts` mounted into the container; larger attack
  surface and patch load. Rejected; Swarm is out of scope entirely.

## Part 1 — dependency refresh

Each bullet is its own PR:

- `fix(deps)`: update all Go modules (supersedes the open Dependabot module
  PRs). `fix` so release-please cuts a release (`chore` is hidden).
- `fix(deps)`: replace archived `gopkg.in/yaml.v3` with `go.yaml.in/yaml/v3`.
- `fix(build)`: build CI and releases with Go 1.27 (`check-latest: true`
  everywhere); `go.mod` stays at `go 1.26.0`.
- `fix(build)`: base image `distroless/static-debian13:nonroot`, pinned by
  digest.
- `fix(ci)`: install syft from a pinned release with checksum verification
  instead of piping `main`'s `install.sh` into `sh`.
- `chore(dev)`: pin the dev stack's images (no `:latest`).
- `ci`: group Dependabot (AWS SDK together), add `docker` and
  `docker-compose` ecosystems; run `govulncheck` weekly on a schedule.
- Merge the open action bumps (#163 docker/login-action 4.8.0, #164
  release-please-action 5.0.0 — its only breaking change is the node24
  runtime, which hosted runners provide) once CI is green.

Out of this work: the open feature/fix PRs #174–#176 and the release PR #162.
Merging #162 publishes a release; that is the maintainer's call.

## Part 2 — setup script

Runs **on the secondary**: `pg setup` runs `pg_basebackup` locally and git
`rsync` pulls into the local repository path (see `test/e2e/README.md`).

Files:

- `deploy/syncctl.service` — systemd unit: `User=syncctl`,
  `EnvironmentFile=/etc/syncctl/syncctl.env`,
  `ExecStart=/usr/local/bin/syncctl serve -c /etc/syncctl/config.yaml`,
  restart on failure, systemd hardening that does not block ssh/rsync.
  Shipped inside the release archive (single source for later packages).
- `scripts/install.sh` — the setup script; also attached to every release.

Script flow (idempotent; every step skips what is already in place):

1. Preflight: root, Linux, systemd, amd64/arm64, and `ssh`, `rsync`, `git`,
   `pg_basebackup` present (missing ones are named, with a hint).
2. Fetch the newest release (or `--version X`), verify SHA-256 against the
   release's `checksums.txt`; if `gh` is installed, also
   `gh attestation verify`. `--archive PATH` installs from a local tarball
   instead (offline hosts, tests).
3. Install the binary to `/usr/local/bin/syncctl` and the unit file.
4. Create system user `syncctl` (home `/var/lib/syncctl`) and an ed25519 key;
   print the public key and the sudoers allowlist from `SECURITY.md` for the
   admin to install on primary and secondary.
5. If `/etc/syncctl/config.yaml` is missing: run `syncctl init` to create it.
6. If `/etc/syncctl/syncctl.env` is missing: prompt (hidden input) for every
   `${VAR}` the config references; write the file `root:root 0600`.
7. Host keys: if `/etc/syncctl/known_hosts` is missing, show the
   `ssh-keyscan` fingerprints of every configured `ssh_host`, write them only
   after the admin confirms, and add `ssh.known_hosts_file` to the config if
   it has no `ssh:` section.
8. `syncctl config-validate` (failure aborts) and `syncctl doctor` (failures
   are shown; the admin decides whether to start anyway; default no, and no
   when stdin is not a terminal).
9. `systemctl enable` and (re)start the service.
10. Print the next manual steps: `pg setup` (with the data-wipe warning),
    `dbkey`, and where logs and metrics are.

Update = re-run: steps 2–3 replace binary and unit, 5–7 are skipped because
their files exist, 9 restarts the service.

## Testing

- `shellcheck` on the script in CI.
- CI smoke test on the runner: install from a locally built archive with a
  pre-seeded config and env file; assert binary, user, unit and file modes;
  re-run and assert nothing changed except the restart.
- Unit file checked with `systemd-analyze verify` in the same job.

## Left to the builder

- Exact systemd hardening directives (must keep ssh/rsync/pg_basebackup
  working).
- How the script lists the `${VAR}` names (grep the config is acceptable).
- Splitting Part 2 into PRs: unit file, script, CI job, release attachment,
  README — one each.
