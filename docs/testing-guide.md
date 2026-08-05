# Testing Guide: Two Real GitLab Instances

This guide walks you through testing syncctl end-to-end using
two real self-hosted GitLab instances — a **primary** and a **secondary**.

## Prerequisites

### What you need

- Two GitLab instances (Omnibus recommended), both accessible via SSH.
- PostgreSQL on the primary configured for physical streaming replication.
- SSH key access from your workstation to both GitLab servers.
- The `syncctl` binary (build it: `go build -o bin/syncctl ./cmd/syncctl`).

### Primary PostgreSQL setup (one-time)

On the **primary** GitLab server, enable replication:

```sh
# 1. Create a replication role (run as postgres user).
sudo -u gitlab-psql /opt/gitlab/embedded/bin/psql \
  -d gitlabhq_production -c \
  "CREATE ROLE gitlab_repl WITH REPLICATION LOGIN PASSWORD 'your_repl_password';"

# 2. Ensure wal_level=replica and max_wal_senders>0.
# Edit /etc/gitlab/gitlab.rb:
#   postgresql['wal_level'] = 'replica'
#   postgresql['max_wal_senders'] = 10
# Then:
sudo gitlab-ctl reconfigure
sudo gitlab-ctl restart postgresql

# 3. Allow the secondary's IP to connect for replication.
# Edit /var/opt/gitlab/postgresql/data/pg_hba.conf, add:
#   host replication gitlab_repl <SECONDARY_IP>/32 md5
# Then:
sudo gitlab-ctl hup postgresql
```

### Secondary PostgreSQL setup (one-time)

On the **secondary** GitLab server, stop PostgreSQL so we can bootstrap
it as a streaming replica:

```sh
sudo gitlab-ctl stop postgresql
sudo rm -rf /var/opt/gitlab/postgresql/data/*
```

### db_key_base

Find the primary's `db_key_base` and copy it to the secondary:

```sh
# On the primary:
grep "gitlab_rails\['db_key_base'\]" /etc/gitlab/gitlab.rb

# On the secondary, edit /etc/gitlab/gitlab.rb and set the SAME value:
#   gitlab_rails['db_key_base'] = '<the_value_from_primary>'
sudo gitlab-ctl reconfigure
```

## Step-by-step test

### Step 0: Build syncctl

```sh
cd /path/to/syncctl
go build -o bin/syncctl ./cmd/syncctl
```

### Step 1: Generate a config

Either use the wizard:

```sh
./bin/syncctl init -o config.yaml
```

Or copy and edit the example:

```sh
cp deploy/config.example.yaml config.yaml
# Edit config.yaml with your primary/secondary IPs, SSH hosts, paths.
```

### Step 2: Export secrets

```sh
export PG_CTRL_PASSWORD='your_gitlab_pg_password'
export PG_REPL_PASSWORD='your_repl_password'
export SEC_REPL_PASSWORD='your_repl_password'
export S3_AK='your_s3_access_key'        # only if using S3
export S3_SK='your_s3_secret_key'        # only if using S3
```

### Step 3: Validate config

```sh
./bin/syncctl config-validate -c config.yaml
```

### Step 4: Run doctor (prerequisite checks)

```sh
./bin/syncctl doctor -c config.yaml
```

This checks:
- SSH connectivity to both sites.
- PostgreSQL control connections on both sites.
- Replication role exists with REPLICATION privilege on primary.
- `wal_level=replica` and `max_wal_senders>0` on primary.
- `pg_is_in_recovery()` on secondary (will WARN if not bootstrapped yet).
- `db_key_base` present on both sites and matching.
- `rsync` and `git` available on both sites.
- Repos path exists on secondary (will WARN if not).

Fix all FAIL items before proceeding. WARN is OK before bootstrap.

### Step 5: Bootstrap the secondary as a streaming replica

On the **secondary** GitLab server, run `pg_basebackup` from the primary:

```sh
# On the secondary server (via SSH):
sudo -u gitlab-psql /opt/gitlab/embedded/bin/pg_basebackup \
  -h <PRIMARY_IP> -U gitlab_repl -D /var/opt/gitlab/postgresql/data \
  -X stream -c fast -R -P -S gitlab_syncctl

# Then start PostgreSQL:
sudo gitlab-ctl start postgresql

# Verify it's in recovery mode:
sudo -u gitlab-psql /opt/gitlab/embedded/bin/psql \
  -d gitlabhq_production -c "SELECT pg_is_in_recovery();"
# Should return: t
```

Or use syncctl (if it can SSH to the secondary and run commands):

```sh
./bin/syncctl pg setup \
  --secondary <secondary_name> \
  --data-dir /var/opt/gitlab/postgresql/data \
  -c config.yaml
```

### Step 6: Verify PostgreSQL replication

```sh
# On the primary, check that the secondary is connected:
sudo -u gitlab-psql /opt/gitlab/embedded/bin/psql \
  -d gitlabhq_production -c \
  "SELECT application_name, state, sync_state, sent_lsn, replay_lsn \
   FROM pg_stat_replication;"

# Should show the secondary with state=streaming.

# Via syncctl:
./bin/syncctl pg status -c config.yaml
```

### Step 7: Run a single sync sweep

```sh
./bin/syncctl sync -c config.yaml
```

This runs all reconcilers once and prints results:
- `[OK] postgres: ...` — PG streaming is connected with low lag.
- `[OK] git_rsync: ...` — repos are being rsynced (or `[DRIFT]` on first run).
- `[OK] object_storage: ...` — S3 buckets match (or `[DRIFT]` on first run).
- `[OK] consistency_sweep: ...` — row counts match (or `[DRIFT]` on first run).

On the first run, expect some `[DRIFT]` states — the secondary hasn't
synced yet. Subsequent runs should show `[OK]`.

### Step 8: Start the sync engine (continuous)

```sh
./bin/syncctl serve -c config.yaml
```

This runs all reconcilers on the configured sweep interval (default 5min)
and serves metrics on `:9101`. Leave it running.

### Step 9: Make changes on the primary and verify they appear on the secondary

On the **primary** GitLab:
1. Create a new project.
2. Push some commits.
3. Create an issue.
4. Create a merge request.
5. Upload a file.

Wait for the next sweep (or trigger a webhook if configured), then check
the **secondary** GitLab:
- The new project should appear.
- The commits should be present (git data synced).
- The issue and MR should appear (DB replicated via WAL streaming).
- The uploaded file should be accessible (object storage synced).

### Step 10: Verify metrics

```sh
curl http://localhost:9101/metrics | grep syncctl
```

Key metrics to check:
- `syncctl_pg_replay_lag_seconds` — should be < 1s.
- `syncctl_drift_total` — should not be increasing.
- `syncctl_last_sync_timestamp_seconds` — should be recent.

### Step 11: Test failover (DRILL — do this on a non-production system)

```sh
# Dry run first:
./bin/syncctl failover --secondary <name> --dry-run -c config.yaml

# Real failover (requires --yes or config sync.failover_enabled=true):
./bin/syncctl failover --secondary <name> --yes -c config.yaml
```

This will:
1. Stop GitLab on the secondary.
2. Promote PostgreSQL on the secondary (pg_ctl promote).
3. Disable read-only mode.
4. Start GitLab on the secondary.
5. Print a post-failover runbook.

After failover, the secondary is now the primary. Verify:
- The web UI loads on the secondary's URL.
- You can create projects/issues (write-capable).
- CI runners need re-pointing (see runbook).

To reverse (make the old primary a secondary again):
```sh
./bin/syncctl adopt-as-secondary --old-primary-ssh <old_primary:22> -c config.yaml
```

### Step 12: Generate a runbook for your environment

```sh
./bin/syncctl runbook -c config.yaml > runbook.md
```

### Step 13: Check SLA

```sh
./bin/syncctl sla -c config.yaml
```

## Troubleshooting

### `pg_basebackup` fails with "no matching host entry"
The primary's `pg_hba.conf` doesn't allow the secondary's IP for
replication. Add a line:
```
host replication gitlab_repl <SECONDARY_IP>/32 md5
```

### `syncctl doctor` reports db_key_base MISMATCH
The `db_key_base` on the secondary doesn't match the primary. SSH to the
secondary, edit `/etc/gitlab/gitlab.rb`, copy the value from the primary,
and run `sudo gitlab-ctl reconfigure`.

### `syncctl_pg_replay_lag_seconds` is always -1
The secondary's `application_name` in `primary_conninfo` doesn't match
the secondary's name in config. The `pg_stat_replication` row on the
primary won't be found. Fix by setting `application_name=<secondary_name>`
in the secondary's `postgresql.auto.conf`.

### rsync fails with "permission denied"
The SSH user on the primary needs read access to
`/var/opt/gitlab/git-data/repositories`. Typically run as the `git` user:
```sh
ssh -i ~/.ssh/id_git primary.example.com
```

### Consistency sweep shows drift on `ci_builds`
This is expected if CI jobs ran between sweeps and WAL hasn't caught up.
Wait for the next sweep — if drift persists, check PG replay lag.

## Summary of commands

| Command | Purpose |
|---|---|
| `syncctl init` | Generate config via wizard |
| `syncctl config-validate` | Validate config |
| `syncctl doctor` | Check prerequisites on both sites |
| `syncctl pg setup` | Bootstrap secondary as streaming replica |
| `syncctl pg status` | Show PG replication lag |
| `syncctl sync` | Run one reconciliation sweep |
| `syncctl serve` | Run sync engine continuously |
| `syncctl dbkey` | Verify db_key_base parity |
| `syncctl failover` | Promote secondary to primary |
| `syncctl adopt-as-secondary` | Role-swap old primary |
| `syncctl runbook` | Generate operational runbook |
| `syncctl sla` | Show RPO/RTO summary |