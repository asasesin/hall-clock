#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PORT="${PORT:-8480}"
ADDR="${ADDR:-:${PORT}}"
INTERVAL="${INTERVAL:-1}"
TMP_BIN="${TMP_BIN:-/tmp/hall-clock-dev}"

server_pid=""

cleanup() {
  if [[ -n "${server_pid}" ]] && kill -0 "${server_pid}" 2>/dev/null; then
    kill "${server_pid}" 2>/dev/null || true
    wait "${server_pid}" 2>/dev/null || true
  fi
}

start_server() {
  cleanup
  echo "[dev] starting hall-clock on ${ADDR}"
  go build -o "${TMP_BIN}" ./src/hall-clock
  "${TMP_BIN}" -addr "${ADDR}" -web-dir src/hall-clock/web &
  server_pid=$!
  sleep 0.2
}

snapshot() {
  find src -type f \
    \( -name '*.go' -o -name '*.html' -o -name '*.css' -o -name '*.js' \) \
    -print0 \
    | sort -z \
    | xargs -0 shasum
}

trap 'cleanup; rm -f "${TMP_BIN}"; exit 0' INT TERM EXIT

last_snapshot="$(snapshot)"
start_server

while true; do
  sleep "${INTERVAL}"
  current_snapshot="$(snapshot)"
  if [[ "${current_snapshot}" != "${last_snapshot}" ]]; then
    echo "[dev] change detected, restarting"
    last_snapshot="${current_snapshot}"
    start_server
  fi
done
