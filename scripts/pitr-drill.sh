#!/usr/bin/env bash
# Point-in-time recovery drill.
#
# Proves the backup story is more than a pg_dump: a base backup plus archived
# WAL is restored into a SECOND PostgreSQL instance, and the row written AFTER
# the base backup must be present. That row exists only in the WAL archive, so
# a passing drill means archive + replay actually work.
#
# Requires Docker and the postgres:18 image. Designed for CI, where throwing
# containers away is free. It never touches a real database.
#
# What it does NOT prove: retention (how long WAL is kept), off-host storage,
# or the operator's restore runbook. Those are decisions, not code.
set -euo pipefail

IMAGE="${PITR_IMAGE:-postgres:18@sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722}"
SRC=pitr-src
DST=pitr-dst
WORK="${PITR_WORK_DIR:-./.pitr}"
ARCHIVE="$WORK/archive"
BASE="$WORK/base"
SRC_PORT=15433
DST_PORT=15434

cleanup() {
  docker rm -f "$SRC" "$DST" >/dev/null 2>&1 || true
}
trap cleanup EXIT

rm -rf "$WORK"
mkdir -p "$ARCHIVE" "$BASE"
# The postgres image runs as uid 999; a bind mount owned by the CI user is not
# writable by it. A throwaway directory in a throwaway job is the right place
# for 0777.
chmod 777 "$ARCHIVE"

psql_src() {
  docker exec -e PGPASSWORD=vincom_dev "$SRC" \
    psql -U vincom -d vincom -v ON_ERROR_STOP=1 -tAc "$1"
}
psql_dst() {
  docker exec -e PGPASSWORD=vincom_dev "$DST" \
    psql -U vincom -d vincom -v ON_ERROR_STOP=1 -tAc "$1"
}

wait_ready() {
  local name=$1
  for _ in $(seq 1 60); do
    if docker exec "$name" pg_isready -U vincom >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "$name did not become ready" >&2
  docker logs "$name" 2>&1 | tail -40 >&2 || true
  return 1
}

echo "== start source with WAL archiving"
docker run -d --name "$SRC" \
  -e POSTGRES_USER=vincom -e POSTGRES_PASSWORD=vincom_dev -e POSTGRES_DB=vincom \
  -p "$SRC_PORT:5432" \
  -v "$(cd "$ARCHIVE" && pwd):/archive" \
  "$IMAGE" \
  -c wal_level=replica \
  -c archive_mode=on \
  -c "archive_command=test ! -f /archive/%f && cp %p /archive/%f" >/dev/null
wait_ready "$SRC"

echo "== write the row that the base backup will NOT contain"
psql_src "CREATE TABLE events (id int PRIMARY KEY, note text NOT NULL);" >/dev/null
psql_src "INSERT INTO events VALUES (1, 'before-backup');" >/dev/null

echo "== base backup"
docker exec -u postgres "$SRC" pg_basebackup -U vincom -D /tmp/base -Fp -Xs -c fast >/dev/null
docker cp "$SRC:/tmp/base/." "$BASE/"

echo "== write the row that only exists in WAL"
# Count archives BEFORE the write so the wait below cannot succeed on a segment
# that predates the after-backup row. `pg_switch_wal()` returns the NEW segment
# name, not the one just completed, so comparing names is the wrong check.
archived_before="$(psql_src "SELECT archived_count FROM pg_stat_archiver;")"
psql_src "INSERT INTO events VALUES (2, 'after-backup');" >/dev/null
psql_src "SELECT pg_switch_wal();" >/dev/null
psql_src "CHECKPOINT;" >/dev/null
psql_src "SELECT pg_switch_wal();" >/dev/null

for _ in $(seq 1 60); do
  archived_count="$(psql_src "SELECT archived_count FROM pg_stat_archiver;")"
  if [ "$archived_count" -gt "$archived_before" ] 2>/dev/null; then
    break
  fi
  sleep 1
done
if [ "${archived_count:-0}" -le "${archived_before:-0}" ] 2>/dev/null; then
  echo "no WAL segment was archived after the second row (before=$archived_before after=${archived_count:-none})" >&2
  exit 1
fi

echo "== stop the source and restore into a second instance"
docker stop "$SRC" >/dev/null

# The base backup predates the second row, so the destination can only see it if
# streaming recovery replays the archived WAL.
printf "restore_command = 'cp /archive/%%f %%p'\nrecovery_target_action = 'promote'\n" \
  >> "$BASE/postgresql.auto.conf"
touch "$BASE/recovery.signal"

# docker cp leaves root-owned files; the image runs postgres as uid 999.
docker run --rm --user root -v "$(cd "$BASE" && pwd):/data" --entrypoint chown \
  "$IMAGE" -R 999:999 /data

# The official entrypoint demands a password even when PGDATA already exists;
# it never initialises here, but it refuses to start without this.
docker run -d --name "$DST" \
  -e POSTGRES_PASSWORD=vincom_dev \
  -e PGPASSWORD=vincom_dev \
  -p "$DST_PORT:5432" \
  -v "$(cd "$BASE" && pwd):/var/lib/postgresql/data" \
  -v "$(cd "$ARCHIVE" && pwd):/archive" \
  "$IMAGE" >/dev/null
wait_ready "$DST"

# pg_isready answers while the server is still replaying. The assertions below
# would then race the replay and read only the base-backup row. Wait for
# promotion (recovery_target_action=promote) before counting.
for _ in $(seq 1 120); do
  in_recovery="$(psql_dst "SELECT pg_is_in_recovery();")"
  if [ "$in_recovery" = "f" ]; then
    break
  fi
  sleep 1
done
if [ "${in_recovery:-t}" != "f" ]; then
  echo "the restored instance never finished WAL replay" >&2
  docker logs "$DST" 2>&1 | tail -40 >&2 || true
  exit 1
fi

rows="$(psql_dst "SELECT count(*) FROM events;")"
after="$(psql_dst "SELECT count(*) FROM events WHERE note = 'after-backup';")"
if [ "$rows" != "2" ] || [ "$after" != "1" ]; then
  echo "restore did not replay the WAL: events=$rows after-backup=$after" >&2
  docker logs "$DST" 2>&1 | tail -40 >&2 || true
  exit 1
fi

echo "PASS: base backup + WAL replay restored both rows (events=$rows, after-backup=$after)"
