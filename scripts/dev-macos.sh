#!/bin/bash
# make dev backend (macOS): native SDR + UDP pair, Docker TCP services, Vite.
#
# Topology follows docs/HARDWARE.md §4: Docker Desktop's UDP port
# forwarder silently drops at the full dual-dongle rate (~3.5k pkt/s),
# so the UDP chain (sdr-capture -> iq-ingest -> signal-processor) runs
# NATIVELY on loopback; only TCP services stay in Docker (db,
# api-gateway, ws-hub, tiles). The recorder sees no IQ in this
# topology (its UDP consumer would cross the same broken forwarder),
# so recording stays a Linux/prod feature of the bench.
#
# Everything this script starts is supervised so nothing can flood the
# terminal or outlive Ctrl+C:
#   - process stdout/stderr go to log files under /tmp, not the terminal
#   - every PID is trapped; Ctrl+C at the Vite prompt (or the script
#     exiting for any reason) tears the whole bench down
set -euo pipefail

cd "$(dirname "$0")/.."

# docker compose merges .env automatically; export it here so the
# native processes below see the same settings (LOG_LEVEL etc.). The
# native signal-processor pins CAPTURE_API_HOST to localhost (the .env
# value targets the container topology) and reaches ws-hub via the base
# compose loopback publish (127.0.0.1:8081).
if [ -f .env ]; then
    set -a
    . ./.env
    set +a
fi
UDP_PORT="${IQ_INGEST_UDP_PORT:-9000}"
# A21: timestamped per-run log files — no unbounded appends to one
# /tmp log across bench runs. Explicit overrides are honored as-is.
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
CAP_LOG="${SIGINT_CAPTURE_LOG:-/tmp/sigint-workbench-sdr-capture-${RUN_STAMP}.log}"
INGEST_LOG="${SIGINT_INGEST_LOG:-/tmp/sigint-workbench-iq-ingest-${RUN_STAMP}.log}"
PROC_LOG="${SIGINT_PROCESSOR_LOG:-/tmp/sigint-workbench-signal-processor-${RUN_STAMP}.log}"

capture_pid=""
ingest_pid=""
processor_pid=""

cleanup() {
    for pid in "$capture_pid" "$ingest_pid" "$processor_pid"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "=== Stopping any previous bench processes ==="
pkill -f 'bin/sdr-capture' 2>/dev/null || true
pkill -f 'bin/iq-ingest' 2>/dev/null || true
pkill -f 'bin/signal-processor' 2>/dev/null || true

echo "=== Building native binaries (capture with hw tags when possible) ==="
if make build-capture-hw; then
    :
else
    echo "WARNING: hardware-tagged build FAILED (error above) — falling back"
    echo "  to a NO-HARDWARE binary. NO SDRs WILL BE FOUND until it is fixed."
    make build-capture
fi
make build-ingest build-processor

echo "=== Detected SDR hardware ==="
./bin/sdr-capture -devices || true

echo "=== Starting Docker TCP services (first run builds images; takes a while) ==="
docker compose up -d \
    db api-gateway ws-hub tiles
# Idempotent schema migrations (db/migrations; no-op on a fresh init).
cat db/migrations/*.sql 2>/dev/null | docker compose exec -T db psql -U sdr -d sdr >/dev/null 2>&1 || true

echo "=== Starting native UDP pair (iq-ingest :$UDP_PORT -> processor :9010) ==="
CONFIG=config/iq-ingest.yaml LISTEN_PORT="$UDP_PORT" \
    CONSUMERS=localhost:9010 \
    nohup ./bin/iq-ingest >>"$INGEST_LOG" 2>&1 &
ingest_pid=$!
LISTEN_PORT=9010 SIGNAL_TTL=30 \
    DB_URL="${DB_URL_LOCAL:-postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable}" \
    WS_HUB_URL=http://127.0.0.1:8081 \
    CAPTURE_API_HOST=localhost \
    SDR_CONFIG=config/sdr-capture.yaml \
    PROCESSOR_CONFIG=config/signal-processor.yaml \
    CLASSIFIER_CONFIG=config/classifier.yaml \
    nohup ./bin/signal-processor >>"$PROC_LOG" 2>&1 &
processor_pid=$!

echo "=== Waiting for the native iq-ingest listener on :$UDP_PORT ==="
ready=0
for _ in $(seq 1 20); do
    if lsof -nP -i :"$UDP_PORT" 2>/dev/null | grep -qi udp; then
        ready=1
        break
    fi
    sleep 3
done
if [ "$ready" = 1 ]; then
    echo "iq-ingest UDP :$UDP_PORT is up"
else
    echo "WARNING: nothing listening on UDP :$UDP_PORT; starting sdr-capture" \
        "anyway — it recovers once the ingest appears"
fi

echo "=== Starting sdr-capture ==="
echo "    capture   log: $CAP_LOG"
echo "    ingest    log: $INGEST_LOG"
echo "    processor log: $PROC_LOG"
nohup ./bin/sdr-capture -config config/sdr-capture.yaml >>"$CAP_LOG" 2>&1 &
capture_pid=$!

echo "=== Starting frontend dev server (Ctrl+C stops the whole bench) ==="
cd frontend
npm run dev
