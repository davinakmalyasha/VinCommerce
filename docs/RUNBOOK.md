# Runbook: backups and restore

## What exists

- `scripts/db-backup.sh` — custom-format `pg_dump`, read back with
  `pg_restore --list`, SHA-256 checksum, retention prune.
- `scripts/db-restore-drill.sh` — restores a dump into a scratch database and
  compares row counts. It never touches the source database.
- CI job `Backup + restore drill` — runs both scripts against a throwaway
  PostgreSQL 18 with seeded tables. A green job means the dump was restored and
  its rows matched, not merely that `pg_dump` exited 0.

## Take a backup

```bash
DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=vincom DB_PASSWORD=... DB_NAME=vincom \
BACKUP_DIR=./backups RETENTION_DAYS=7 \
  ./scripts/db-backup.sh
```

The script prints the dump path. Keep the `.sha256` file beside it: a dump
without a checksum cannot be distinguished from one that was truncated in
transit.

## Prove a backup restores

```bash
DB_PASSWORD=... TABLES=products,orders \
  ./scripts/db-restore-drill.sh backups/vincom-20260101T000000Z.dump
```

Run this after changing schema, not only when something breaks. A drill that
has never passed on the current schema is the same as no drill.

## What this is NOT

**Not point-in-time recovery.** A logical dump restores to the moment it was
taken; every order between the dump and the incident is lost. Real PITR needs:

1. `archive_mode = on` and an `archive_command` that ships WAL segments to
   durable storage (S3/GCS with object lock, or a second host);
2. a base backup (`pg_basebackup`) on a schedule;
3. retention long enough to cover the worst recovery window you have promised
   (7 days of WAL is the usual starting point);
4. a restore rehearsal against the SAME major version, on a machine that is not
   the database host.

None of that is in this repository. `postgres` in `infra/compose` is a single
container with a named volume; losing the host loses the volume. Treat the
compose setup as a demo and the scripts above as the copy mechanism.

## Restore into production (outline)

1. Stop the API and worker (`docker compose stop api worker`).
2. Take a final logical dump of the broken database if it still starts.
3. Create a new database and `pg_restore --no-owner --no-privileges`.
4. Point `DB_NAME` at it, run migrations (goose), start the API, check
   `/health/ready`.
5. Reconcile: `ledger:reconcile` and `payouts:release_reservations` run on the
   worker schedule; confirm the trial balance is clean before re-enabling
   payouts.

## TLS

`infra/compose/compose.tls.yaml` adds a terminator: nginx 1.29 on 8443 with
TLS 1.2/1.3, HSTS, nosniff, a plain-HTTP 301 to https, and a local
`/healthz` for probing. It proxies to `web:8080` on the compose network; the
web port stays loopback.

```bash
mkdir -p infra/tls/certs   # put fullchain.pem and privkey.pem here
docker compose -f infra/compose/compose.full.yaml                -f infra/compose/compose.tls.yaml up -d
```

`infra/tls/certs/` is gitignored. Certificate renewal is NOT automated: that
belongs to the certificate manager or a certbot sidecar, and a renewal hook
that silently stops working is worse than none. CI generates a self-signed
certificate, boots the same config, and asserts the TLS handshake, HSTS and
the redirect -- see job `TLS terminator probe`.
