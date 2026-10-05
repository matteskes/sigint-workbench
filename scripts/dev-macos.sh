#!/bin/bash
# make dev backend (macOS): native sdr-capture + Docker services + Vite.
#
# Supervises the capture process so it can never flood the terminal or
# outlive Ctrl+C again:
#   - Docker services start FIRST (a clean environment spends minutes
#     building images), so iq-ingest owns UDP :9000 before capture runs.
#   - The capture's stdout/stderr go to a log file, not the terminal.
#   - The capture's PID is trapped; Ctrl+C at the Vite prompt (or the
#     script exiting for any reason) tears it down with the frontend.
set -euo pipefail

cd "$(dirname "$0")/.."

# docker compose merges .env automatically; export it here too so the
# UDP wait below uses the same port the compose file publishes.
if [ -f .env ]; then
    set -a
    . ./.env
    set +a
fi
UDP_PORT="${IQ_INGEST_UDP_PORT:-9000}"
LOG="${SIGINT_CAPTURE_LOG:-/tmp/sigint-workbench-sdr-capture.log}"

capture_pid=""

cleanup() {
    if [ -n "$capture_pid" ]; then
        kill "$capture_pid" 2>/dev/null || true
    fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "=== Stopping any previous sdr-capture ==="
pkill -f 'bin/sdr-capture' 2>/dev/null || true

echo "=== Building sdr-capture (native macOS, hw tags when possible) ==="
if ! make build-capture-hw 2>/dev/null; then
    make build-capture
fi

echo "=== Starting Docker services (first run builds images; takes a while) ==="
docker compose up -d iq-ingest signal-processor recorder api-gateway ws-hub db tiles

echo "=== Waiting for the iq-ingest UDP forward on :$UDP_PORT ==="
ready=0
for _ in $(seq 1 60); do
    if lsof -nP -i :"$UDP_PORT" 2>/dev/null | grep -qi udp; then
        ready=1
        break
    fi
    sleep 3
done
if [ "$ready" = 1 ]; then
    echo "iq-ingest UDP :$UDP_PORT is up"
else
    echo "WARNING: nothing listening on UDP :$UDP_PORT after 3 min;" \
        "starting sdr-capture anyway — it retries and recovers" \
        "once iq-ingest comes up"
fi

echo "=== Starting sdr-capture (logs: $LOG) ==="
nohup ./bin/sdr-capture -config config/sdr-capture.yaml >>"$LOG" 2>&1 &
capture_pid=$!

echo "=== Starting frontend dev server (Ctrl+C stops sdr-capture too) ==="
cd frontend
npm run dev
