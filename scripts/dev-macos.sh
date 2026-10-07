#!/bin/bash
# make dev backend (macOS): native SDR + UDP pair, Docker TCP services, Vite.
#
# Topology follows docs/HARDWARE.md §4: Docker Desktop's UDP port
# forwarder silently drops at the full dual-dongle rate (~3.5k pkt/s),
# so the whole UDP chain (sdr-capture -> iq-ingest -> signal-processor
# -> recorder) runs NATIVELY on loopback; only TCP services stay in
# Docker (db, api-gateway, ws-hub, tiles). B18: the recorder is native
# too — a containerized one starves behind the same broken forwarder,
# which silenced §10 recording and §10.4 live audio on this bench. The
# gateway reaches the recorder's live-audio WS and §19 TFR API via
# host.docker.internal (same pattern as the capture control proxy),
# and the compose `recorder` container is stopped here.
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
# §7.4/§20: the capture control API lives natively on host loopback
# :9090 on this bench, so the compose gateway must reach it via the
# host gateway, not the in-network sdr-capture default (its §20 probe
# and the §7.4 tune/gain proxy would both fail otherwise). A .env
# value still wins.
export CAPTURE_CTRL_ADDR="${CAPTURE_CTRL_ADDR:-host.docker.internal:9090}"
# B18: the recorder runs NATIVELY on this bench (inside the UDP chain
# — see HARDWARE.md §4), so the gateway's /ws/audio and §19 TFR relays
# must reach the host recorder, not the stopped compose service. Same
# pattern as CAPTURE_CTRL_ADDR above; a .env value still wins.
export RECORDER_WS_ADDR="${RECORDER_WS_ADDR:-host.docker.internal:9012}"
export RECORDER_API_ADDR="${RECORDER_API_ADDR:-host.docker.internal:9013}"

UDP_PORT="${IQ_INGEST_UDP_PORT:-9000}"
# Native capture control port (§7.4/§20) — derived from the exported
# CAPTURE_CTRL_ADDR above; used by the B5 port-free/health checks.
CAPTURE_CTRL_PORT="${CAPTURE_CTRL_ADDR##*:}"
# A21: timestamped per-run log files — no unbounded appends to one
# /tmp log across bench runs. Explicit overrides are honored as-is.
RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
CAP_LOG="${SIGINT_CAPTURE_LOG:-/tmp/sigint-workbench-sdr-capture-${RUN_STAMP}.log}"
INGEST_LOG="${SIGINT_INGEST_LOG:-/tmp/sigint-workbench-iq-ingest-${RUN_STAMP}.log}"
PROC_LOG="${SIGINT_PROCESSOR_LOG:-/tmp/sigint-workbench-signal-processor-${RUN_STAMP}.log}"
REC_LOG="${SIGINT_RECORDER_LOG:-/tmp/sigint-workbench-recorder-${RUN_STAMP}.log}"

capture_pid=""
ingest_pid=""
processor_pid=""
recorder_pid=""

cleanup() {
    for pid in "$capture_pid" "$ingest_pid" "$processor_pid" "$recorder_pid"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# B5 (docs/UI-BUGCHECK.md): a previous bench can outlive pkill —
# SIGKILL'd wrappers skip the cleanup trap, SIGTTIN-stopped children
# still hold their listeners and USB claims. These two checks turn
# "spawn and hope" into verify-or-fail.

# wait_port_free PORT NAME — poll until nothing holds PORT (either
# end of the socket), escalating to pkill -9 once, then fail with
# the holder PIDs instead of spawning into a zombie bench.
wait_port_free() {
    local port="$1" name="$2" holders
    for _ in $(seq 1 40); do # 20 s TERM grace
        if ! lsof -nP -i :"$port" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    echo "  :$port still held after SIGTERM — sending SIGKILL to $name"
    pkill -9 -f "bin/$name" 2>/dev/null || true
    for _ in $(seq 1 20); do # 10 s KILL grace
        if ! lsof -nP -i :"$port" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    holders="$(lsof -t -i :"$port" 2>/dev/null | tr '\n' ' ' || true)"
    echo "ERROR: :$port is still held by PID(s): ${holders:-unknown} ($name)"
    echo "  Free it manually (kill -9 <pid>) — often a SIGKILL'd bench"
    echo "  still claiming the USB dongles — then re-run make dev."
    return 1
}

# verify_child PID PROTO PORT NAME LOG — fail fast when a freshly
# spawned child dies (with its last log lines), and succeed only when
# that child itself holds its port. The old readiness loop accepted
# ANY listener on the port, which passed against the previous bench's
# orphan and produced the zombie bench from the bugcheck.
verify_child() {
    local pid="$1" proto="$2" port="$3" name="$4" log="$5" holders
    for _ in $(seq 1 40); do # 20 s startup grace (slow USB bring-up)
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "ERROR: $name (pid $pid) exited during startup — last lines of $log:"
            tail -5 "$log" 2>/dev/null || true
            return 1
        fi
        holders="$(lsof -t -i "$proto:$port" 2>/dev/null | tr '\n' ' ' || true)"
        case " ${holders}" in
            *" $pid "*)
                echo "$name (pid $pid) holds $proto :$port"
                return 0 ;;
        esac
        sleep 0.5
    done
    echo "ERROR: $name (pid $pid) never took $proto :$port within 20 s"
    echo "  (holders now: ${holders:-none}) — last lines of $log:"
    tail -5 "$log" 2>/dev/null || true
    return 1
}

echo "=== Stopping any previous bench processes ==="
pkill -f 'bin/sdr-capture' 2>/dev/null || true
pkill -f 'bin/iq-ingest' 2>/dev/null || true
pkill -f 'bin/signal-processor' 2>/dev/null || true
pkill -f 'bin/recorder' 2>/dev/null || true
# B5: TERM alone doesn't guarantee a clean bench. Wait until the UDP
# pair and the capture control port are actually free before building
# or spawning anything — spawning into held ports produced the
# usb_claim_interface / bind: address already in use cascade.
wait_port_free "$UDP_PORT" iq-ingest || exit 1
wait_port_free 9010 signal-processor || exit 1
wait_port_free 9011 recorder || exit 1
wait_port_free "$CAPTURE_CTRL_PORT" sdr-capture || exit 1

echo "=== Building native binaries (capture with hw tags when possible) ==="
if make build-capture-hw; then
    :
else
    echo "WARNING: hardware-tagged build FAILED (error above) — falling back"
    echo "  to a NO-HARDWARE binary. NO SDRs WILL BE FOUND until it is fixed."
    make build-capture
fi
make build-ingest build-processor
if make build-recorder; then
    :
else
    echo "WARNING: opus-tagged recorder build FAILED (error above) — falling"
    echo "  back to a no-opus build: recording works, live audio is off."
    go build -o bin/recorder ./cmd/recorder
fi

echo "=== Detected SDR hardware ==="
./bin/sdr-capture -devices || true

echo "=== Starting Docker TCP services (first run builds images; takes a while) ==="
docker compose up -d \
    db api-gateway ws-hub tiles
# B18: the compose `recorder` would claim host :9011/udp for a
# container the UDP forwarder can never feed — the native recorder
# started below replaces it on this bench (HARDWARE.md §4).
docker compose stop recorder >/dev/null 2>&1 || true
# Idempotent schema migrations (db/migrations; no-op on a fresh init).
cat db/migrations/*.sql 2>/dev/null | docker compose exec -T db psql -U sdr -d sdr >/dev/null 2>&1 || true

echo "=== Starting native UDP chain (iq-ingest :$UDP_PORT -> processor :9010 + recorder :9011) ==="
echo "    capture   log: $CAP_LOG"
echo "    ingest    log: $INGEST_LOG"
echo "    processor log: $PROC_LOG"
echo "    recorder  log: $REC_LOG"
CONFIG=config/iq-ingest.yaml LISTEN_PORT="$UDP_PORT" \
    CONSUMERS=localhost:9010,localhost:9011 \
    nohup ./bin/iq-ingest >>"$INGEST_LOG" 2>&1 &
ingest_pid=$!
verify_child "$ingest_pid" udp "$UDP_PORT" iq-ingest "$INGEST_LOG" || exit 1

LISTEN_PORT=9010 SIGNAL_TTL=30 \
    DB_URL="${DB_URL_LOCAL:-postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable}" \
    WS_HUB_URL=http://127.0.0.1:8081 \
    CAPTURE_API_HOST=localhost \
    SDR_CONFIG=config/sdr-capture.yaml \
    PROCESSOR_CONFIG=config/signal-processor.yaml \
    CLASSIFIER_CONFIG=config/classifier.yaml \
    nohup ./bin/signal-processor >>"$PROC_LOG" 2>&1 &
processor_pid=$!
verify_child "$processor_pid" udp 9010 signal-processor "$PROC_LOG" || exit 1

echo "=== Starting native recorder (IQ :9011, live audio :9012) ==="
# §10/§10.4 on the bench (B18): the recorder joins the NATIVE UDP
# chain — a containerized one starves behind Docker's UDP forwarder
# (HARDWARE.md §4). RECORDINGS_DIR=. with the cwd inside ./recordings
# keeps DB rows relative, so the gateway container resolves them under
# its own /recordings mount (§13.1 path confinement).
pushd recordings >/dev/null
RECORDER_CONFIG=../config/recorder.yaml RECORDINGS_DIR=. \
    RECORDER_BIND_HOST=127.0.0.1 \
    DB_URL="${DB_URL_LOCAL:-postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable}" \
    WS_HUB_URL=http://127.0.0.1:8081 \
    nohup ../bin/recorder >>"$REC_LOG" 2>&1 &
recorder_pid=$!
popd >/dev/null
verify_child "$recorder_pid" udp 9011 recorder "$REC_LOG" || exit 1

echo "=== Starting sdr-capture ==="
nohup ./bin/sdr-capture -config config/sdr-capture.yaml >>"$CAP_LOG" 2>&1 &
capture_pid=$!
verify_child "$capture_pid" tcp "$CAPTURE_CTRL_PORT" sdr-capture "$CAP_LOG" || exit 1

echo "=== Starting frontend dev server (Ctrl+C stops the whole bench) ==="
cd frontend
npm run dev
