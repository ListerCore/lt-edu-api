#!/bin/sh
# Brings the database up to date before the server starts. Migrations are idempotent, so every container start
# (and every replica) can run them. Set APP__DB__AUTO_MIGRATE=false to skip, e.g. when a deploy step runs them.
set -eu

if [ "${APP__DB__AUTO_MIGRATE:-true}" != "false" ]; then
  ./migrate up
fi

exec ./main "$@"
