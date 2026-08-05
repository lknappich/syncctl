# AGENTS.md — clean-room & contribution policy

This file governs how AI agents and human contributors must work on
`syncctl`. It exists to keep the project legally clean: a true
clean-room reimplementation of functionality that GitLab ships in its
paid Enterprise Edition, using **only** public, documented interfaces.

## What this project IS

- An independent orchestrator that runs **standard infrastructure
  tooling** — PostgreSQL physical streaming replication, `rsync`,
  `git fetch`, S3 bucket replication — to mirror a self-hosted GitLab
  instance to one or more replicas.
- A control plane (`syncctl`) that monitors sync state, surfaces drift,
  and performs safe failover.
- An optional observational validator that diffs public API outputs
  between sites (read-only; never writes via API as the replication
  path).

## What this project is NOT

- It is **not** GitLab Geo. It contains no GitLab EE code, no
  reimplementation of EE-specific Ruby modules, and no license
  circumvention.
- It does **not** read, decompile, or introspect GitLab EE source or
  binaries to determine behavior. Behavior is derived solely from
  public docs and from the observable state of software the operator
  already runs (Postgres schema, filesystem layout, HTTP API
  responses).

## Allowed information sources

Contributors MAY consult, and code MAY rely on:

1. GitLab's **public REST/GraphQL API documentation**.
2. The **public PostgreSQL schema** of GitLab CE (Community Edition),
   which is observable on any CE install the operator owns. Querying
   `pg_stat_replication`, table row counts, and the schema itself is
   standard Postgres use, not reverse engineering.
3. Standard tooling: `pg_basebackup`, WAL receiver protocol, `rsync`,
   `git` plumbing, S3/MinIO APIs, Redis CLI.
4. GitLab's public installation/configuration docs (omnibus, Helm,
   `gitlab.rb` template).

## Disallowed information sources

Contributors MUST NOT:

1. Read GitLab EE source (including the `geo/` namespace within EE).
2. Use decompilers, binary introspection, or traffic capture against
   an EE-licensed instance to learn non-public behavior.
3. Use any API endpoint or field not in GitLab's public docs.
4. Submit code derived from GitLab EE source, in whole or in part.
5. Reimplement GitLab's Ruby-side crypto (e.g. `Gitlab::Crypto::*`).
   The tool copies the `db_key_base` so the *GitLab application itself*
   decrypts on the secondary; our code never decrypts.

## Naming and trademarks

Clean-room policy protects us on copyright. It says nothing about
trademarks, so this section covers that separately.

"GitLab" is a registered trademark of GitLab B.V., and "Geo" is the name
of one of their paid features. Therefore:

1. The project, binary, Go module, Docker image, and Prometheus metric
   namespace MUST NOT lead with "GitLab" or use "Geo" as a product name.
   They are all named `syncctl`.
2. Describing what the tool works with is fine and expected — "a
   replication tool for self-hosted GitLab" is nominative use. Naming
   the *product* after the mark is not.
3. Comparative statements ("this is not GitLab Geo", "GitLab Geo is a
   paid feature") are accurate and should stay. Do not remove them.
4. Do not use GitLab's logo, wordmark, or brand colors anywhere in the
   repo, docs, or release artifacts.

## Build & test commands

```sh
go build ./...
go test ./...
go vet ./...
```

Before committing, run all three (or `make build test vet`). The CI
workflow also runs `govulncheck ./...` and `staticcheck ./...`.

## Style

- Go. Single static binary via `cmd/syncctl`.
- No comments unless they explain *why*; the code should explain *what*.
- Secrets via env only; reject literals in config.
- Idempotent reconcilers: any run must be safe to retry.
- Structured logs via zerolog; metrics via Prometheus.

## Commit / PR conventions

- Conventional Commit prefixes (`feat:`, `fix:`, `chore:`, `docs:`,
  `refactor:`, `test:`).
- Keep PRs focused; one logical change each.
- Do not commit secrets, `.env` files, or local state.