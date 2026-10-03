#!/usr/bin/env bash
# smoke-test.sh — end-to-end smoke test for the ONNX signal-processor.
#
# Builds the real signal-processor binary with the `onnx` build tag, runs it
# as a UDP receiver, streams synthetic CW and WFM IQ frames over UDP (the same
# construction models/train.py and the Go E2E tests use), and verifies the
# binary detects BOTH modulations at the expected frequencies:
#
#   CW  -> 16.0000 MHz  (1.5 MHz offset on a 14.5 MHz frame)
#   WFM -> ~100.8 MHz   (800 kHz offset on a 100 MHz frame)
#
# Preconditions: Go + cgo, and the trained model (models/classifier.onnx,
# fetched via git-lfs). The ONNX Runtime shared library is auto-downloaded for
# the current platform when ORT_LIBRARY_PATH is not already set — the same
# release/layout docker/Dockerfile.classifier uses.
#
# Env overrides:
#   MODEL_PATH        path to the ONNX model (default: models/classifier.onnx)
#   ORT_LIBRARY_PATH  path to libonnxruntime (.so/.dylib); downloaded if unset
#   ORT_VERSION       ONNX Runtime version (default: 1.29.0, must be >= 1.29)
#
# Usage:
#   ./scripts/smoke-test.sh
#   make smoke-test
set -euo pipefail

# Run from the repo root regardless of the caller's directory.
cd "$(dirname "$0")/.."

ORT_VERSION="${ORT_VERSION:-1.29.0}"
MODEL_PATH="${MODEL_PATH:-models/classifier.onnx}"

if [ ! -f "$MODEL_PATH" ]; then
  echo "smoke-test: model not found: $MODEL_PATH (run 'git lfs pull' first)" >&2
  exit 1
fi

WORKDIR="$(mktemp -d)"
SP_PID=""
cleanup() {
  if [ -n "$SP_PID" ]; then
    kill "$SP_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

# ── Resolve the ONNX Runtime library (download for this platform if needed) ──
if [ -z "${ORT_LIBRARY_PATH:-}" ]; then
  goos="$(go env GOOS)"
  goarch="$(go env GOARCH)"
  case "$goos/$goarch" in
  darwin/arm64) pkg="onnxruntime-osx-arm64"; lib="libonnxruntime.dylib" ;;
  darwin/amd64) pkg="onnxruntime-osx-x64"; lib="libonnxruntime.dylib" ;;
  linux/amd64) pkg="onnxruntime-linux-x64"; lib="libonnxruntime.so" ;;
  linux/arm64) pkg="onnxruntime-linux-aarch64"; lib="libonnxruntime.so" ;;
  *)
    echo "smoke-test: unsupported platform $goos/$goarch (set ORT_LIBRARY_PATH)" >&2
    exit 1
    ;;
  esac
  echo "smoke-test: downloading ONNX Runtime ${ORT_VERSION} (${pkg})..."
  curl -sfL -o "$WORKDIR/ort.tgz" \
    "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}-${ORT_VERSION}.tgz"
  tar -xzf "$WORKDIR/ort.tgz" -C "$WORKDIR"
  ORT_LIBRARY_PATH="$WORKDIR/${pkg}-${ORT_VERSION}/lib/$lib"
fi
if [ ! -f "$ORT_LIBRARY_PATH" ]; then
  echo "smoke-test: ONNX Runtime lib not found: $ORT_LIBRARY_PATH" >&2
  exit 1
fi

# ── Build the processor (onnx tag) and the frame sender ──
echo "smoke-test: building signal-processor (-tags onnx) + smoke-frames..."
CGO_ENABLED=1 go build -tags onnx -o "$WORKDIR/signal-processor" ./cmd/signal-processor
go build -o "$WORKDIR/smoke-frames" ./cmd/smoke-frames

# ── Pick a free UDP port ──
port="$(python3 -c 'import socket; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(("",0)); print(s.getsockname()[1])' 2>/dev/null)" || port=""
[ -n "$port" ] || port=19100
log="$WORKDIR/sp.log"

# ── Start the processor and wait for the listener + ONNX load ──
case "$MODEL_PATH" in
/*) abs_model="$MODEL_PATH" ;;
*) abs_model="$PWD/$MODEL_PATH" ;;
esac
echo "smoke-test: starting signal-processor on UDP :$port (model=$abs_model)"
MODEL_PATH="$abs_model" ORT_LIBRARY_PATH="$ORT_LIBRARY_PATH" \
  "$WORKDIR/signal-processor" -port "$port" >"$log" 2>&1 &
SP_PID=$!

for _ in $(seq 1 50); do
  grep -q 'listening on UDP' "$log" 2>/dev/null && break
  kill -0 "$SP_PID" 2>/dev/null || { echo "smoke-test: processor exited early:" >&2; cat "$log" >&2; exit 1; }
  sleep 0.2
done
grep -q 'listening on UDP' "$log" 2>/dev/null || { echo "smoke-test: processor did not start listening:" >&2; cat "$log" >&2; exit 1; }
if ! grep -q 'loaded ONNX classifier' "$log" 2>/dev/null; then
  echo "smoke-test: ONNX classifier did not load (ORT_LIBRARY_PATH/model?):" >&2
  cat "$log" >&2
  exit 1
fi

# ── Stream frames and let the pipeline run ──
echo "smoke-test: sending CW frames..."
"$WORKDIR/smoke-frames" -mod cw -addr "127.0.0.1:$port" -count 5
echo "smoke-test: sending WFM frames..."
"$WORKDIR/smoke-frames" -mod wfm -addr "127.0.0.1:$port" -count 5
sleep 1.5

# ── Verify: strongest detected peak in each expected region ──
# logSignal format:  ... SIGNAL  <id>  <MHz>  <band>  <mod>/<src>  <method>  <db> dB  BW:...  conf:<x>
# awk fields: $3=SIGNAL $4=id $5=MHz $6='MHz' $7=band $8=mod/src $9=method $10=<db>
# §6.5: class (src, the part of $8 after '/') must be a source enum, not a
# method:label like onnx:cw. So we match the modulation prefix only.
cw="$(awk -v lo=15.95 -v hi=16.05 \
  'BEGIN { best = -1e9 } $3=="SIGNAL" && $8 ~ /^CW\// && $5+0>=lo && $5+0<=hi { if ($10+0>best) { best=$10+0; line=$0 } } END { if (line!="") print line }' \
  "$log")"
wfm="$(awk -v lo=100.7 -v hi=100.9 \
  'BEGIN { best = -1e9 } $3=="SIGNAL" && $8 ~ /^FM\// && $5+0>=lo && $5+0<=hi { if ($10+0>best) { best=$10+0; line=$0 } } END { if (line!="") print line }' \
  "$log")"

fail=0
if [ -z "$cw" ]; then
  echo "smoke-test: FAIL - no CW peak detected near 16.0000 MHz" >&2
  fail=1
else
  echo "smoke-test: OK  CW  -> $(printf '%s' "$cw" | sed -E 's/.*SIGNAL[[:space:]]+//')"
fi
if [ -z "$wfm" ]; then
  echo "smoke-test: FAIL - no WFM peak detected near 100.8000 MHz" >&2
  fail=1
else
  echo "smoke-test: OK  WFM -> $(printf '%s' "$wfm" | sed -E 's/.*SIGNAL[[:space:]]+//')"
fi

# §6.5: class must carry a source enum, never method:label (onnx:*/rules:*).
check_class() {
  local line="$1" src
  src=$(printf '%s' "$line" | awk '{split($8,a,"/"); print a[2]}')
  case "$src" in
    aviation|land_mobile|marine|amateur|broadcast|gnss|wifi|unknown) ;;
    *) echo "smoke-test: FAIL - class source '$src' is not a valid enum: $line" >&2; fail=1 ;;
  esac
}
if [ -n "$cw" ]; then check_class "$cw"; fi
if [ -n "$wfm" ]; then check_class "$wfm"; fi

if [ "$fail" -ne 0 ]; then
  echo "smoke-test: signal-processor log:" >&2
  cat "$log" >&2
  exit 1
fi
echo "smoke-test: PASS - CW + WFM detected end-to-end through the real binary"