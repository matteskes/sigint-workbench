#!/bin/bash
# setup-tiles.sh — Download or generate an MBTiles file for the tile server.
#
# Usage:
#   ./tiles/setup-tiles.sh <geofabrik-region> [zoom_min] [zoom_max]
#
# Examples (Geofabrik extract paths — see https://download.geofabrik.de):
#   ./tiles/setup-tiles.sh europe/monaco
#   ./tiles/setup-tiles.sh north-america/us/california 8 14
#   ./tiles/setup-tiles.sh switzerland 6 14
#
# tilemaker has no Homebrew formula (`brew install tilemaker` fails:
# never in core, and the old systemed/tilemaker tap was removed), so
# this script runs a tilemaker from PATH if one exists, else it falls
# back to the project Docker image — no local install required. The
# vendored OpenMapTiles config under tiles/tilemaker/ matches the
# layers the workbench map style draws (see tiles/tilemaker/README.md).

set -euo pipefail

REGION="${1:?Usage: setup-tiles.sh <geofabrik-region> [zoom_min] [zoom_max]}"
ZOOM_MIN="${2:-}"
ZOOM_MAX="${3:-}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="${SCRIPT_DIR}/data"
PBF_DIR="${SCRIPT_DIR}/pbfs"
TM_DIR="${SCRIPT_DIR}/tilemaker"
TILEMAKER_IMAGE="ghcr.io/systemed/tilemaker:master"
# Geofabrik paths may carry slashes (europe/monaco); the output stays
# flat so tileserver-gl's /data scan picks it up as one tileset.
REGION_SLUG="${REGION//\//_}"
OUTPUT="${DATA_DIR}/${REGION_SLUG}.mbtiles"

mkdir -p "$DATA_DIR" "$PBF_DIR"

# ─── Step 1: Download OSM PBF from Geofabrik ───
# Geofabrik names files <region>-latest.osm.pbf (dash, not dot).
PBF_URL="https://download.geofabrik.de/${REGION}-latest.osm.pbf"
PBF_FILE="${PBF_DIR}/${REGION_SLUG}.osm.pbf"

if [[ -s "$PBF_FILE" ]]; then
    echo "--- Using existing PBF: ${PBF_FILE} ---"
else
    echo "--- Downloading OSM data for ${REGION} ---"
    echo "    URL: ${PBF_URL}"
    # -f: a wrong region name must fail the script — never save the
    # 404 HTML page as a .pbf (the failure that shipped junk files).
    if ! curl -fL --progress-bar --retry 2 -o "$PBF_FILE" "$PBF_URL"; then
        rm -f "$PBF_FILE"
        echo "ERROR: download failed. '${REGION}' is not a Geofabrik"
        echo "extract path — check https://download.geofabrik.de/ (bare"
        echo "state names like 'arizona' or 'california' do not exist;"
        echo "use e.g. north-america/us/california)."
        exit 1
    fi
fi
PBF_BYTES="$(wc -c < "$PBF_FILE" | tr -d ' ')"
if [ "$PBF_BYTES" -lt 100000 ]; then
    echo "ERROR: ${PBF_FILE} is only ${PBF_BYTES} bytes — not a real"
    echo "OSM PBF. Delete it and re-check the region name."
    exit 1
fi

# ─── Step 2: Locate tilemaker (PATH binary, else Docker) ───
MODE=""
if command -v tilemaker &>/dev/null; then
    MODE="native"
elif docker info &>/dev/null; then
    MODE="docker"
    echo ""
    echo "tilemaker not on PATH — using the ${TILEMAKER_IMAGE} image"
    echo "(no brew formula exists for tilemaker; to install natively"
    echo "see tiles/tilemaker/README.md)"
else
    echo ""
    echo "ERROR: no tilemaker on PATH and Docker is not reachable."
    echo ""
    echo "Options:"
    echo "  1. Start Docker Desktop, then re-run this script (the"
    echo "     ghcr.io/systemed/tilemaker image is used automatically)."
    echo "  2. Build natively (upstream docs/INSTALL.md):"
    echo "       brew install boost lua shapelib rapidjson"
    echo "       git clone https://github.com/systemed/tilemaker"
    echo "       cd tilemaker && make && sudo make install"
    echo "  3. Drop any ready-made .mbtiles into ${DATA_DIR}/"
    exit 1
fi

# ─── Step 3: OpenMapTiles config + process (vendored, v3.2.0) ───
CONFIG="${TM_DIR}/tilemaker-config.json"
PROCESS="${TM_DIR}/process-openmaptiles.lua"
if [[ ! -f "$CONFIG" || ! -f "$PROCESS" ]]; then
    echo "ERROR: missing ${CONFIG} or ${PROCESS} — see"
    echo "tiles/tilemaker/README.md for provenance."
    exit 1
fi

# Optional zoom override: patch a temp copy of the config (tilemaker
# has no CLI zoom flags; zooms live in settings.minzoom/maxzoom).
RUNTIME_CONFIG="$CONFIG"
RUNTIME_TMP=""
cleanup() { if [ -n "$RUNTIME_TMP" ]; then rm -f "$RUNTIME_TMP"; fi; }
trap cleanup EXIT
if [[ -n "${ZOOM_MIN}${ZOOM_MAX}" ]]; then
    ZMIN="${ZOOM_MIN:-0}"
    ZMAX="${ZOOM_MAX:-14}"
    if command -v python3 &>/dev/null; then
        RUNTIME_TMP="${TM_DIR}/.tilemaker-config.json"
        RUNTIME_CONFIG="$RUNTIME_TMP"
        python3 -c 'import json,sys; c=json.load(open(sys.argv[1])); s=c.setdefault("settings",{}); s["minzoom"]=int(sys.argv[2]); s["maxzoom"]=int(sys.argv[3]); json.dump(c,open(sys.argv[4],"w"),indent=2)' \
            "$CONFIG" "$ZMIN" "$ZMAX" "$RUNTIME_TMP"
        echo "Zoom:    ${ZMIN} – ${ZMAX} (patched config copy)"
    else
        echo "WARNING: python3 not found — rendering with the shipped"
        echo "zoom range (0–14); zoom arguments ignored."
    fi
fi

# ─── Step 4: Render MBTiles ───
echo "--- Rendering MBTiles with tilemaker (${MODE}) ---"
if [[ "$MODE" == "native" ]]; then
    tilemaker \
        --input "$PBF_FILE" \
        --output "$OUTPUT" \
        --config "$RUNTIME_CONFIG" \
        --process "$PROCESS"
else
    docker run --rm \
        -v "${SCRIPT_DIR}:/work" \
        "$TILEMAKER_IMAGE" \
        --input "/work/pbfs/${REGION_SLUG}.osm.pbf" \
        --output "/work/data/${REGION_SLUG}.mbtiles" \
        --config "/work/tilemaker/$(basename "$RUNTIME_CONFIG")" \
        --process "/work/tilemaker/process-openmaptiles.lua"
fi

echo ""
echo "=== Done! ==="
echo "MBTiles file: ${OUTPUT}"
echo "Size: $(du -h "$OUTPUT" | cut -f1)"
echo ""
echo "Start the tile server with: docker compose up tiles"
echo "Tiles served at: http://localhost:8082/data/${REGION_SLUG}/{z}/{x}/{y}.pbf"
echo "(with a single .mbtiles in tiles/data, the frontend default"
echo "http://localhost:8082/data/{z}/{x}/{y}.pbf also works)"