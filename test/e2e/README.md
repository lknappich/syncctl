# End-to-end tests against real GitLab CE

These tests boot two Omnibus `gitlab/gitlab-ce` containers, seed the primary,
and drive the real `syncctl` binary against them. Everything else in the
repository is mocked; this is the check that the core path works on the real
software.

Covered today: boot, seed, `doctor`, `pg setup`, `sync`. Failover and
`adopt-as-secondary` come in a later change.

## Layout

- `docker-compose.yml`: `gl-a` (primary) and `gl-b` (secondary) on one network,
  with memory-constrained settings from GitLab's public documentation. `gl-b`
  runs only PostgreSQL and sshd.
- `e2e_test.go`, `harness_test.go`: one ordered scenario, `TestE2E`, behind the
  `e2e` build tag, so `go test ./...` does not run it.
- `known_failures.txt`: scenarios that fail today because of bugs listed in
  `ROADMAP.md`. They are skipped, with the roadmap reference in the skip message.

## Running locally

You need Docker with the compose plugin, Go, and about 12 GB of free memory.
The first boot takes 5 to 15 minutes while GitLab reconfigures.

```sh
E2E_GITLAB_IMAGE=gitlab/gitlab-ce:18.5.1-ce.0 \
  go test -tags e2e -timeout 45m -v ./test/e2e/...
```

Without `E2E_GITLAB_IMAGE`, or without Docker, the test skips.

| Variable | Effect |
|---|---|
| `E2E_GITLAB_IMAGE` | Image to test (required). |
| `E2E_KEEP=1` | Leave the containers running after the test, to debug. |
| `E2E_RUN_KNOWN_FAILURES=1` | Run the scenarios in `known_failures.txt` instead of skipping them. |

Clean up a kept environment with
`docker compose -p syncctl-e2e -f test/e2e/docker-compose.yml down -v`.

## How it works

- `syncctl` runs inside `gl-b`, as it would on a real secondary. `pg setup`
  runs `pg_basebackup` locally and `sync` rsyncs into the local repository
  path, so both need to be on the secondary. It reaches `gl-a` over SSH and
  PostgreSQL on the compose network.
- `gitlab-secrets.json` is copied from `gl-a` to `gl-b` before `gl-b`'s first
  reconfigure, so both sites share the same secrets.
- The replication role and the passwords are set with plain SQL. No GitLab
  helper commands are used.
- Seeding uses a personal access token created with the documented
  `gitlab-rails runner` method, then the public REST API.
- Readiness is polled, never slept on.

The CI workflow is `.github/workflows/e2e.yml`. It runs nightly and on manual
dispatch, against the newest patch of the two newest CE minors.
