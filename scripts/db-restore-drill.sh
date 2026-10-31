#!/usr/bin/env bash
# Restore a dump into a SCRATCH database and prove it matches the source.
#
# Usage: db-restore-drill.sh <dump-file>
#
# This never touches the source database. It creates a throwaway database,
# restores into it, and compares row counts for the tables named in TABLES
# (default: products,orders). A mismatch fails the script, so a backup that
# restores into an empty-but-valid database with missing data is caught.
#
# The dump file is expected in the custom format written by scripts/db-backup.sh.
set -euo pipefail

dump="${1:-}"
if [ -z "$dump" ] || [ ! -f "$dump" ]; then
  echo "usage: db-restore-drill.sh <dump-file>" >&2
  exit 2
fi

host="${DB_HOST:-127.0.0.1}"
port="${DB_PORT:-5432}"
user="${DB_USER:-vincom}"
source_db="${DB_NAME:-vincom}"
target_db="${RESTORE_DB:-${source_db}_restore_drill}"
tables="${TABLES:-products,orders}"

export PGPASSWORD="${DB_PASSWORD:-}"
if [ -z "${PGPASSWORD}" ]; then
  echo "db-restore-drill: DB_PASSWORD is empty; refusing to guess" >&2
  exit 2
fi

psql_admin=(psql --host "$host" --port "$port" --username "$user" --dbname postgres --no-psqlrc --quiet)

cleanup() {
  "${psql_admin[@]}" -c "DROP DATABASE IF EXISTS \"$target_db\"" >/dev/null || true
}
trap cleanup EXIT

# A previous drill that died before cleanup must not poison this one.
"${psql_admin[@]}" -c "DROP DATABASE IF EXISTS \"$target_db\"" >/dev/null
"${psql_admin[@]}" -c "CREATE DATABASE \"$target_db\"" >/dev/null

pg_restore \
  --host "$host" --port "$port" --username "$user" \
  --dbname "$target_db" --no-owner --no-privileges --exit-on-error \
  "$dump"

failed=0
IFS=',' read -r -a table_list <<< "$tables"
for table in "${table_list[@]}"; do
  table="$(echo "$table" | tr -d '[:space:]')"
  [ -z "$table" ] && continue

  # Identifiers are interpolated, so reject anything that is not a plain name:
  # this script takes its table list from the environment.
  if ! [[ "$table" =~ ^[a-z_][a-z0-9_]*$ ]]; then
    echo "db-restore-drill: refusing suspicious table name '$table'" >&2
    failed=1
    continue
  fi

  source_count="$(psql --host "$host" --port "$port" --username "$user" --dbname "$source_db" \
    --no-psqlrc --tuples-only --no-align -c "SELECT count(*) FROM \"$table\"")"
  target_count="$(psql --host "$host" --port "$port" --username "$user" --dbname "$target_db" \
    --no-psqlrc --tuples-only --no-align -c "SELECT count(*) FROM \"$table\"")"

  if [ "$source_count" != "$target_count" ]; then
    echo "db-restore-drill: $table has $source_count rows in $source_db but $target_count in $target_db" >&2
    failed=1
  else
    echo "PASS $table: $target_count rows restored"
  fi
done

exit "$failed"
