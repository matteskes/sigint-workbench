#!/bin/bash
# deploy.sh — Deploy air-gap assets from transfer media to the target system.
#
# Usage (on the air-gap target):
#   sudo ./scripts/deploy.sh <transfer-directory>
#
# This script copies supplied assets (model, tiles, config) into their
# proper locations on the air-gap target system and verifies deployment.
#
# See: E2E-TEST-SUITE.md §4.11, Tests 4.11.A3, A5, A6.

set -euo pipefail

TRANSFER_DIR="${1:?Usage: deploy.sh <transfer-directory>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ─── Asset locations on the air-gap target ───
MODEL_DIR="${PROJECT_ROOT}/models"
TILES_DATA_DIR="${PROJECT_ROOT}/tiles/data"
CONFIG_DIR="${PROJECT_ROOT}/config"

# ─── Pre-deploy checks ───
if [[ ! -d "$TRANSFER_DIR" ]]; then
    echo "ERROR: Transfer directory ${TRANSFER_DIR} not found."
    exit 1
fi

# Verify network is truly down (air-gap pre-flight).
echo "--- Pre-deploy air-gap check ---"
if ping -c1 -W1 8.8.8.8 2>/dev/null; then
    echo "ERROR: Outbound network is NOT fully blocked — aborting deploy."
    echo "This system must be air-gapped before deploying sensitive assets."
    exit 1
else
    echo "  [PASS] Outbound network is blocked (ping 8.8.8.8 failed)."
fi

# ─── Step 1: Deploy model (if present in transfer) ───
echo ""
echo "--- Step 1: Model deployment ---"
if [[ -d "${TRANSFER_DIR}/models" ]]; then
    for model_file in "${TRANSFER_DIR}/models/"*.onnx; do
        if [[ -f "$model_file" ]]; then
            BASENAME="$(basename "$model_file")"
            cp "$model_file" "${MODEL_DIR}/${BASENAME}"
            echo "  [OK] models/${BASENAME}"
        fi
    done
else
    echo "  (no models in transfer — skipping)."
fi

# ─── Step 2: Deploy tile sets (if present in transfer) ───
echo ""
echo "--- Step 2: Tile set deployment ---"
for region_dir in "${TRANSFER_DIR}"/*/; do
    if [[ ! -d "$region_dir" ]]; then
        continue
    fi
    SLUG="$(basename "$region_dir")"
    MBTILES="${region_dir}/${SLUG}.mbtiles"

    if [[ -f "$MBTILES" ]]; then
        # Validate before deploying.
        if "${SCRIPT_DIR}/validate-tiles.sh" "$MBTILES"; then
            cp "$MBTILES" "${TILES_DATA_DIR}/${SLUG}.mbtiles"
            echo "  [OK] tiles/data/${SLUG}.mbtiles"

            # Generate audit log entry.
            FILE_HASH=""
            if command -v shasum &>/dev/null; then
                FILE_HASH="$(shasum -a 256 "$MBTILES" | cut -d' ' -f1)"
            elif command -v sha256sum &>/dev/null; then
                FILE_HASH="$(sha256sum "$MBTILES" | cut -d' ' -f1)"
            fi

            AUDIT_FILE="${PROJECT_ROOT}/system.audit.log"
            echo "DEPLOY $(date -u '+%Y-%m-%dT%H:%M:%SZ') region=${SLUG} hash=${FILE_HASH} operator=$(whoami)" >> "$AUDIT_FILE"
            echo "  [AUDIT] Logged to system.audit.log"
        else
            echo "  [SKIP] Validation failed — not deploying ${SLUG}.mbtiles"
        fi
    else
        echo "  [WARN] No .mbtiles in ${SLUG}/ — skipping."
    fi

    # Deploy manifest if present.
    if [[ -f "${region_dir}/MANIFEST.txt" ]]; then
        cp "${region_dir}/MANIFEST.txt" "${TILES_DATA_DIR}/${SLUG}.MANIFEST.txt"
        echo "  [OK] tiles/data/${SLUG}.MANIFEST.txt"
    fi
done

# ─── Step 3: Deploy config (if present in transfer) ───
echo ""
echo "--- Step 3: Config deployment ---"
if [[ -d "${TRANSFER_DIR}/config" ]]; then
    for conf_file in "${TRANSFER_DIR}/config/"*.yaml; do
        if [[ -f "$conf_file" ]]; then
            BASENAME="$(basename "$conf_file")"
            cp "$conf_file" "${CONFIG_DIR}/${BASENAME}"
            echo "  [OK] config/${BASENAME}"
        fi
    done
else
    echo "  (no config in transfer — skipping)."
fi

# ─── Step 4: Post-deploy verification ───
echo ""
echo "--- Post-deploy verification ---"
echo "  [INFO] Restart services to pick up new assets:"
echo "         docker compose up -d sdr-capture iq-ingest signal-processor"
echo "               recorder api-gateway ws-hub tiles"
echo ""
echo "  [INFO] Verify ONNX loads: check signal-processor logs for model load."
echo "  [INFO] Verify tiles serve: curl http://localhost:8082/data/v3/0/0/0.pbf"
echo ""
echo "=== Deployment complete ==="