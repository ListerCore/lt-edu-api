#!/usr/bin/env bash
# Runs a k6 scenario against a local lt-edu-api with the official k6 image (no local install needed).
#
#   ./run.sh eq03-rendimiento.js                 # full ramp to 2 000 VUs (~7.5 min)
#   SCALE=0.1 PEAK_VUS=100 ./run.sh eq03-rendimiento.js   # quick smoke run
#
# Sessions come from cmd/demosessions (demo institutions only). Results land in tests/load/results/.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
SCRIPT="${1:?uso: ./run.sh <escenario.js>}"
NAME="$(basename "$SCRIPT" .js)"
API_URL="${API_URL:-http://host.docker.internal:5050}"
mkdir -p "$HERE/results"

if [[ ! -f "$HERE/sessions.json" ]]; then
  (cd "$ROOT" && printf '{"demo-ebr": %s, "demo-eba": %s}\n' \
    "$(go run ./cmd/demosessions -slug demo-ebr 2>/dev/null)" \
    "$(go run ./cmd/demosessions -slug demo-eba 2>/dev/null)") > "$HERE/sessions.json"
fi

docker run --rm -i \
  --add-host=host.docker.internal:host-gateway \
  --ulimit nofile=65536:65536 \
  -v "$HERE:/scripts" -w /scripts \
  -e API_URL="$API_URL" -e SESSIONS=/scripts/sessions.json \
  -e PEAK_VUS="${PEAK_VUS:-2000}" -e SCALE="${SCALE:-1}" \
  -e SUMMARY="results/$NAME-summary.json" \
  grafana/k6:latest run "$SCRIPT" | tee "$HERE/results/$NAME.txt"
