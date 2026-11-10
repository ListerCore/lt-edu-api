#!/bin/sh
# Brings the database up to date before the server starts: pending migrations, then pending seed files and the
# superadmin. Both are idempotent, so every container start (and every replica) can run them. Set
# APP__DB__AUTO_MIGRATE=false to skip both, e.g. when a deploy step runs them separately.
set -eu

if [ "${APP__DB__AUTO_MIGRATE:-true}" != "false" ]; then
  ./migrate up
  ./seed
fi

exec ./main "$@"
