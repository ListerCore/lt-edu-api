#!/usr/bin/env bash
# Runs the Jest suite against an lt-edu-api built with Go coverage (go build -cover) and reports how much of the
# backend the suite exercised. Needs the database and Redis of .env up, migrated and seeded (make up).
#
#   ./run-with-coverage.sh        → coverage/go/index.html, coverage/go/summary.txt and coverage/jest/
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
HTTP_PORT="${COVER_HTTP_PORT:-5080}"
GRPC_PORT="${COVER_GRPC_PORT:-5081}"
OUT="$HERE/coverage/go"
RAW="$OUT/raw"

rm -rf "$OUT" && mkdir -p "$RAW"
cd "$ROOT"
# -cover instruments every package of the module; generated code is filtered out of the report below
go build -cover -o bin/server-cover ./cmd/server

APP__HOST__PORT="$HTTP_PORT" APP__GRPC__PORT="$GRPC_PORT" GOCOVERDIR="$RAW" ./bin/server-cover > "$OUT/server.log" 2>&1 &
SERVER=$!
trap 'kill -TERM $SERVER 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do
  curl -sf "http://localhost:$HTTP_PORT/api/v1/health" > /dev/null && break
  sleep 1
done

cd "$HERE"
status=0
API_URL="http://localhost:$HTTP_PORT" npx jest --coverage || status=$?

# SIGTERM shuts the server down cleanly, which is when Go writes the coverage counters
kill -TERM $SERVER && wait $SERVER || true
trap - EXIT

cd "$ROOT"
# Hand-written code only: core services and adapters, without the protobuf/gateway generated code and cmd/
go tool covdata percent -i="$RAW" | grep -E 'api/internal/(core|adapter)' | grep -v '/gen/' > "$OUT/summary.txt"
go tool covdata textfmt -i="$RAW" -o "$OUT/all.out"
{ head -1 "$OUT/all.out"; grep -E '^api/internal/(core|adapter)/' "$OUT/all.out" | grep -v '/gen/'; } > "$OUT/coverage.out"
go tool cover -func="$OUT/coverage.out" | tail -1 >> "$OUT/summary.txt"
go tool cover -html="$OUT/coverage.out" -o "$OUT/index.html"
python3 "$HERE/coverage-scope.py" "$OUT/coverage.out" > "$OUT/scope.txt"
echo "Cobertura del backend ejercitada por Jest (todo el backend):"
cat "$OUT/summary.txt"
echo
echo "Cobertura de los módulos con endpoints REST:"
cat "$OUT/scope.txt"
exit $status
