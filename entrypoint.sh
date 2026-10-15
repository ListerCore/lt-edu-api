#!/bin/sh
set -e

# El binario se conecta con las mismas DB_* que lee la API, espera a que
# la base responda y aplica solo las migraciones que falten segun
# `schema_migrations`.
echo "Running database migrations..."
/app/migrate up

echo "Starting server..."
exec ./main
