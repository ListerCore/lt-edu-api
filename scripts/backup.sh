#!/bin/sh
# Daily backup of the database (phase E9 of docs/ceba/PLAN.md): a pg_dump in custom format, named by date, with the
# old ones pruned. Run it from cron on the host (or a scheduled job of the hosting panel):
#
#   15 3 * * *  cd /srv/joseph-api && BACKUP_DIR=/srv/backups ./scripts/backup.sh >> /var/log/joseph-backup.log 2>&1
#
# Environment:
#   APP__DB__URL or DATABASE_URL  connection string (pg_dump of the same major version as the server, 18)
#   DB_CONTAINER                  instead of a local pg_dump, run it inside this container (docker exec)
#   BACKUP_DIR                    where the dumps go (default ./backups)
#   BACKUP_KEEP_DAYS              dumps older than this are removed (default 30)
#   BACKUP_COPY_CMD               optional command run with the dump path, to copy it off the server, e.g.
#                                 'mc cp {} remote/joseph-backups/' or 'rclone copy {} drive:joseph'
# The files (attachments) live in the object storage (MinIO / S3): keep versioning or replication on its bucket.
set -eu

URL="${APP__DB__URL:-${DATABASE_URL:-}}"
DIR="${BACKUP_DIR:-./backups}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
STAMP="$(date +%Y%m%d-%H%M)"
FILE="$DIR/joseph-$STAMP.dump"

mkdir -p "$DIR"
if [ -n "${DB_CONTAINER:-}" ]; then
  USER_NAME="${APP__DB__USER:-joseph}"
  DB_NAME="${APP__DB__NAME:-joseph_db}"
  docker exec "$DB_CONTAINER" pg_dump --format=custom --no-owner --no-privileges -U "$USER_NAME" -d "$DB_NAME" > "$FILE.part"
elif [ -n "$URL" ]; then
  pg_dump --format=custom --no-owner --no-privileges --dbname="$URL" > "$FILE.part"
else
  echo "backup: set APP__DB__URL (or DATABASE_URL) or DB_CONTAINER" >&2
  exit 1
fi
# A dump that pg_restore cannot list is not a backup
if [ -n "${DB_CONTAINER:-}" ]; then
  docker exec -i "$DB_CONTAINER" pg_restore --list < "$FILE.part" > /dev/null
else
  pg_restore --list "$FILE.part" > /dev/null
fi
mv "$FILE.part" "$FILE"
echo "backup: $FILE ($(du -h "$FILE" | cut -f1))"

if [ -n "${BACKUP_COPY_CMD:-}" ]; then
  sh -c "$(echo "$BACKUP_COPY_CMD" | sed "s|{}|$FILE|g")"
  echo "backup: copied off the server"
fi
find "$DIR" -name 'joseph-*.dump' -mtime +"$KEEP" -print -exec rm -f {} \;
