#!/bin/sh
# Proves a backup restores (phase E9): loads a dump into a scratch database next to the live one, compares the row
# counts of the main tables with the live database, and drops the scratch database. Run it weekly and after any
# change of the backup job. Exit 0 when the restore is complete and the counts match (rows written to the live
# database after the dump make its counts higher: the check fails only when the restore has fewer rows than the
# dump should hold, i.e. a table came back empty or missing).
#
#   ./scripts/restore-check.sh backups/lt-edu-20261116-0315.dump
#
# Environment: DB_CONTAINER (docker exec) or APP__DB__URL; APP__DB__USER and APP__DB__NAME for the container mode.
set -eu

DUMP="${1:?usage: restore-check.sh <file.dump>}"
SCRATCH="ltedu_restore_check"
USER_NAME="${APP__DB__USER:-ltedu}"
DB_NAME="${APP__DB__NAME:-ltedu_db}"
TABLES="iam.institutions iam.users people.students people.staff enrollment.enrollments academic.sections assessment.competency_marks classroom.items classroom.submissions attendance.activity_events wellbeing.interviews documents.versions attachments.files"

psql_live() {
  if [ -n "${DB_CONTAINER:-}" ]; then docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -At -U "$USER_NAME" -d "$1" -c "$2"
  else psql -v ON_ERROR_STOP=1 -At --dbname="$(echo "${APP__DB__URL:?}" | sed "s|/$DB_NAME|/$1|")" -c "$2"; fi
}

psql_live postgres "DROP DATABASE IF EXISTS $SCRATCH" > /dev/null
psql_live postgres "CREATE DATABASE $SCRATCH" > /dev/null
trap 'psql_live postgres "DROP DATABASE IF EXISTS $SCRATCH" > /dev/null' EXIT

if [ -n "${DB_CONTAINER:-}" ]; then
  docker exec -i "$DB_CONTAINER" pg_restore --no-owner --no-privileges --exit-on-error -U "$USER_NAME" -d "$SCRATCH" < "$DUMP"
else
  pg_restore --no-owner --no-privileges --exit-on-error --dbname="$(echo "${APP__DB__URL:?}" | sed "s|/$DB_NAME|/$SCRATCH|")" "$DUMP"
fi

status=0
printf '%-34s %10s %10s\n' table restored live
for t in $TABLES; do
  restored=$(psql_live "$SCRATCH" "SELECT count(*) FROM $t" 2>/dev/null || echo missing)
  live=$(psql_live "$DB_NAME" "SELECT count(*) FROM $t")
  mark=""
  if [ "$restored" = missing ] || { [ "$live" -gt 0 ] && [ "$restored" -eq 0 ]; }; then mark=" ✗"; status=1; fi
  printf '%-34s %10s %10s%s\n' "$t" "$restored" "$live" "$mark"
done
version=$(psql_live "$SCRATCH" "SELECT max(version) FROM schema_migrations" 2>/dev/null || echo "?")
echo "restore-check: migration version $version"
[ $status -eq 0 ] && echo "restore-check: OK" || echo "restore-check: FAILED"
exit $status
