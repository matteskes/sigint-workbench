#!/bin/bash
# setup-tiles.sh — Download or generate an MBTiles file for the tile server.
#
# Usage:
#   ./tiles/setup-tiles.sh <region> [zoom_min] [zoom_max]
#
# Examples:
#   ./tiles/setup-tiles.sh california 10 16
#   ./tiles/setup-tiles.sh europe 8 14
#   ./tiles/setup-tiles.sh usa 7 12
#
# This script uses tilemaker (https://github.com/tilemaker/tilemaker)
# to render OSM data into a vector MBTiles file.

set -euo pipefail

REGION="${1:?Usage: setup-tiles.sh <region> [zoom_min] [zoom_max]}"
ZOOM_MIN="${2:-10}"
ZOOM_MAX="${3:-16}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="${SCRIPT_DIR}/data"
PBF_DIR="${SCRIPT_DIR}/pbfs"
OUTPUT="${DATA_DIR}/${REGION}.mbtiles"

mkdir -p "$DATA_DIR" "$PBF_DIR"

echo "=== SIGINT Workbench — Tile Setup ==="
echo "Region:  ${REGION}"
echo "Zoom:    ${ZOOM_MIN} – ${ZOOM_MAX}"
echo "Output:  ${OUTPUT}"
echo ""

# ─── Step 1: Download OSM PBF from Geofabrik ───
PBF_URL="https://download.geofabrik.de/${REGION}.latest.osm.pbf"
PBF_FILE="${PBF_DIR}/${REGION}.osm.pbf"

if [[ ! -f "$PBF_FILE" ]]; then
    echo "--- Downloading OSM data for ${REGION} ---"
    echo "    URL: ${PBF_URL}"
    curl -L --progress-bar -o "$PBF_FILE" "$PBF_URL"
else
    echo "--- Using existing PBF: ${PBF_FILE} ---"
fi

# ─── Step 2: Check for tilemaker ───
if ! command -v tilemaker &>/dev/null; then
    echo ""
    echo "tilemaker not found. Install it with:"
    echo "  macOS:   brew install tilemaker"
    echo "  Linux:   See https://github.com/tilemaker/tilemaker#installation"
    echo ""
    echo "Or use a pre-built MBTiles from:"
    echo "  https://openmaptiles.org/data/"
    echo ""
    echo "Place the .mbtiles file in: ${DATA_DIR}/"
    exit 1
fi

# ─── Step 3: Render MBTiles ───
echo "--- Rendering MBTiles with tilemaker ---"
tilemaker \
    --osm-file "$PBF_FILE" \
    --output "$OUTPUT" \
    --style-map "map. styles" \
    --min-zoom "$ZOOM_MIN" \
    --max-zoom "$ZOOM_MAX" \
    --no-natural-earth

echo ""
echo "=== Done! ==="
echo "MBTiles file: ${OUTPUT}"
echo "Size: $(du -h "$OUTPUT" | cut -f1)"
echo ""
echo "Start the tile server with: docker compose up tiles"
echo "Tiles available at: http://localhost:8082/data/${REGION}/"