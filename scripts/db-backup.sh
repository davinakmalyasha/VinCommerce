#!/usr/bin/env bash
# Back up the PostgreSQL database in the custom format, verify the dump, and
# prune old copies.
#
# The point is not "a pg_dump was run": an unverified dump is a file that looks
# like a backup until the day it is needed. This script reads the archive back
# with `pg_restore --list` before reporting success, and the drill in CI
# (`.github/workflows/ci.yml`, job `db-backup-drill`) restores it into a second
# database and compares row counts.
#
# NOT PITR. This is a logical dump: it restores to the moment the dump was
# taken, losing everything after it. Continuous archiving (`archive_mode`,
# `archive_command`, WAL retention) is an operator concern documented in
# docs/RUNBOOK.md, not something a script in the repository can turn on.
#
# Configuration (same names the application and CI already use):
#   DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME
#   BACKUP_DIR       where to write (default ./backups)
#   RETENTION_DAYS   delete dumps older than this (default 7)
set -euo pipefail

host="${DB_HOST:-127.0.0.1}"
port="${DB_PORT:-5432}"
user="${DB_USER:-vincom}"
name="${DB_NAME:-vincom}"
dir="${BACKUP_DIR:-./backups}"
retention="${RETENTION_DAYS:-7}"

# Accept the standard PGPASSWORD as well as DB_PASSWORD: the CI drill runs
# inside the postgres image, where the client tools read PGPASSWORD.
export PGPASSWORD="${DB_PASSWORD:-${PGPASSWORD:-}}"

if [ -z "${PGPASSWORD}" ]; then
  echo "db-backup: DB_PASSWORD/PGPASSWORD is empty; refusing to guess" >&2
  exit 2
fi

mkdir -p "$dir"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
file="$dir/${name}-${stamp}.dump"

# --no-owner/--no-privileges: the dump is restored into a database owned by a
# different role in most drills, and a restored ACL that references a missing
# role makes pg_restore fail halfway through.
pg_dump \
  --host "$host" --port "$port" --username "$user" \
  --format=custom --no-owner --no-privileges \
  --file "$file" "$name"

# Read the archive back. A truncated or zero-byte dump fails here, which is the
# difference between "the backup task ran" and "there is a restorable backup".
if ! pg_restore --list "$file" >/dev/null; then
  echo "db-backup: dump $file is not readable; leaving it in place for inspection" >&2
  exit 1
fi

sha256sum "$file" > "$file.sha256"

# Retention on the DUMP files only; the checksum is removed with its dump.
find "$dir" -type f -name '*.dump' -mtime +"$retention" -print0 |
  while IFS= read -r -d '' old; do
    rm -f "$old" "$old.sha256"
  done

echo "$file"
