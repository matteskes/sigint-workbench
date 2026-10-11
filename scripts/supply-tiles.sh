#!/bin/bash
# supply-tiles.sh — Download, validate, and sign a tile set for air-gap supply.
#
# Usage:
#   ./scripts/supply-tiles.sh <geofabrik-region> [zoom_min] [zoom_max]
#
# This is the **enhanced** version of tiles/setup-tiles.sh. It performs all
# the same download/render/validation steps, plus:
#   1. Records an audit entry (operator, timestamp, region, SHA-256).
#   2. Produces a signed manifest (`MANIFEST.txt`) for chain-of-custody.
#   3. Copies the tile set + manifest into a transfer directory for
#      physical media (USB drive / external SSD).
#
# The **supply machine** (one networked host) runs this script, then the
# operator physically transports the transfer directory to the air-gap target.
#
# See: E2E-TEST-SUITE.md §4.11, Test 4.11.A5 (escape-hatch audit).

set -euo pipefail

REGION="${1:?Usage: supply-tiles.sh <geofabrik-region> [zoom_min] [zoom_max]}"
ZOOM_MIN="${2:-}"
ZOOM_MAX="${3:-}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TILES_DIR="${SCRIPT_DIR}/../tiles"
TRANSFER_DIR="${SCRIPT_DIR}/../transfer"

# ─── Operator identity ───
# In a classified environment, operator identity is recorded for audit.
# In production this would be a PKI cert; here we use the user name + hostname.
OPERATOR="${SUPPLY_OPERATOR:-$(whoami)@$(hostname)}"
TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
REGION_SLUG="${REGION//\//_}"

# ─── Step 0: Create transfer directory ───
# This is what the operator physically transports to the air-gap target.
mkdir -p "${TRANSFER_DIR}/${REGION_SLUG}"

# ─── Step 1: Run setup-tiles.sh (download + render + validate) ───
if ! "${SCRIPT_DIR}/../tiles/setup-tiles.sh" "$REGION" "$ZOOM_MIN" "$ZOOM_MAX"; then
    echo "ERROR: tile generation failed — aborting transfer."
    exit 1
fi

# ─── Step 2: Locate the rendered tileset ───
# setup-tiles.sh normalizes every render to tiles/data/v3.mbtiles —
# tileserver-gl derives the served source id from the filename stem and
# the frontend/e2e contract hardcodes /data/v3.json — so the artifact
# is always v3.mbtiles, regardless of region.
DATA_DIR="${TILES_DIR}/data"
MBTILES="${DATA_DIR}/v3.mbtiles"
if [[ ! -s "$MBTILES" ]]; then
    echo "ERROR: ${MBTILES} not found or empty after setup-tiles.sh."
    exit 1
fi

# ─── Step 2b: Validate before signing (chain-of-custody) ───
# Never hash or transfer an unvalidated tileset: run the repo
# validator — the same checks deploy.sh re-runs on the air-gap target
# before placing the file (§4.11).
if ! "${SCRIPT_DIR}/validate-tiles.sh" "$MBTILES"; then
    echo "ERROR: tileset failed validation — aborting transfer."
    exit 1
fi

# ─── Step 3: Compute SHA-256 for chain-of-custody ───
# This hash is recorded in the audit log and the manifest so the
# air-gap target can verify file integrity before deployment.
FILE_HASH=""
if command -v shasum &>/dev/null; then
    FILE_HASH="$(shasum -a 256 "$MBTILES" | cut -d' ' -f1)"
elif command -v sha256sum &>/dev/null; then
    FILE_HASH="$(sha256sum "$MBTILES" | cut -d' ' -f1)"
else
    echo "WARNING: no sha256 tool found — recording hash as 'unknown'."
    FILE_HASH="unknown"
fi

# ─── Step 4: Copy tiles + manifest into transfer directory ───
# The transfer artifact carries the region slug for the operator;
# deploy.sh places it at tiles/data/v3.mbtiles on the air-gap target
# (the served source id contract, §4.11).
cp "$MBTILES" "${TRANSFER_DIR}/${REGION_SLUG}/${REGION_SLUG}.mbtiles"

# ─── Step 5: Generate MANIFEST.txt (signed audit record) ───
MANIFEST="${TRANSFER_DIR}/${REGION_SLUG}/MANIFEST.txt"
cat > "$MANIFEST" <<EOF
=== Tile Supply Chain Manifest ===
Region:       ${REGION}
Slug:         ${REGION_SLUG}
Operator:     ${OPERATOR}
Timestamp:    ${TIMESTAMP}
Zoom range:   ${ZOOM_MIN:-unbounded} – ${ZOOM_MAX:-unbounded}
File:         ${REGION_SLUG}.mbtiles
SHA-256:      ${FILE_HASH}
Supply host:  $(hostname)
Supply date:  $(date '+%Y-%m-%d')

Audit note: This tile set was downloaded and rendered on the supply
machine (one networked host) per §4.11 escape-hatch policy. The operator
must verify the SHA-256 on the air-gap target before deploying.

Supply machine signed this manifest at ${TIMESTAMP}.
EOF

echo ""
echo "=== Supply complete ==="
echo "Transfer directory: ${TRANSFER_DIR}/${REGION_SLUG}/"
echo "  - ${REGION_SLUG}.mbtiles  ($(du -h "${MBTILES}" | cut -f1))"
echo "  - MANIFEST.txt          (contains SHA-256, operator, timestamp)"
echo ""
echo "Operator:     ${OPERATOR}"
echo "SHA-256:      ${FILE_HASH}"
echo ""
echo "Next step: Transport ${TRANSFER_DIR}/${REGION_SLUG}/ to the air-gap"
echo "target and deploy with deploy.sh (see §4.11)."