# Contributing to syncctl

Thanks for your interest. This project is a **clean-room** reimplementation
of functionality available in GitLab's paid Enterprise Edition. To keep the
project legally clean, all contributors must follow the policy in
[`AGENTS.md`](AGENTS.md). In particular:

- Do not submit code derived from GitLab EE source, in whole or in part.
- Do not use API endpoints or fields that are not in GitLab's public docs.
- Do not reimplement GitLab's Ruby-side crypto; this tool only copies the
  `db_key_base` so the GitLab application itself decrypts on the secondary.
- Use only standard, documented interfaces: public REST/GraphQL API, public
  Postgres schema of GitLab CE (which you run), `rsync`, `git` plumbing,
  S3/MinIO APIs.

## Development setup

```sh
go build ./...
go test ./...
go vet ./...
```

Requires Go 1.25+. A `docker-compose` dev stack (primary + secondary
GitLab + Postgres + MinIO) is provided under `deployments/dev` for
end-to-end testing of the replication flows.

## Pull requests

- One logical change per PR.
- Conventional Commit prefixes (`feat:`, `fix:`, `chore:`, `docs:`,
  `refactor:`, `test:`).
- Run `go build ./... && go test ./... && go vet ./...` before pushing.
- Do not commit secrets, `.env` files, or local state.

## Releasing

Releases are cut from tagged commits on `main`. `goreleaser` builds
multi-arch binaries and Docker images. The release pipeline also runs
`govulncheck ./...`.

## Code of conduct

See [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md). Be excellent to each
other. Disagreements about architecture are welcome; disagreements about
people are not.