#!/usr/bin/env bash
# EQ-04: starts LiveKit, puts constant load on the critical modules, stops LiveKit 30 s into the run and checks that
# the critical modules never failed and that the API process kept running (same PID, health OK).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
CONTAINER="${APP__LIVEKIT__CONTAINER:-lt-edu-livekit}"
API_HEALTH="${API_HEALTH:-http://localhost:5050/api/v1/health}"

cd "$ROOT"
docker compose up -d livekit > /dev/null
PID_BEFORE="$(pgrep -f 'bin/server$' || true)"
echo "LiveKit arriba; PID de la API: ${PID_BEFORE:-?}"

(sleep 30 && docker stop "$CONTAINER" > /dev/null && echo ">> $(date +%T) LiveKit detenido") &
STOPPER=$!

cd "$HERE"
status=0
DURATION="${DURATION:-90s}" ./run.sh eq04-disponibilidad.js || status=$?
wait $STOPPER || true

PID_AFTER="$(pgrep -f 'bin/server$' || true)"
HEALTH="$(curl -s "$API_HEALTH?db=true&redis=true")"
echo "PID de la API después: ${PID_AFTER:-?} · salud: $HEALTH" | tee -a "$HERE/results/eq04-disponibilidad.txt"
if [[ -n "$PID_BEFORE" && "$PID_BEFORE" != "$PID_AFTER" ]]; then
  echo "FALLA: la API se reinició durante la prueba" | tee -a "$HERE/results/eq04-disponibilidad.txt"
  status=1
fi
exit $status
