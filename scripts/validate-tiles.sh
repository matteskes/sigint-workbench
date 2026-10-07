#!/bin/bash
# validate-tiles.sh — Validate a single .mbtiles file for air-gap deployment.
#
# Usage:
#   ./scripts/validate-tiles.sh <path-to-mbtiles-file>
#
# This script validates an .mbtiles file that was supplied via the
# escape hatch (USB/external media). It checks:
#   1. SQLite schema integrity (required tables/columns).
#   2. Bounds sanity (west < east, center inside bounds).
#   3. Tile count (non-empty).
#   4. SHA-256 matches the MANIFEST.txt if present.
#
# See: E2E-TEST-SUITE.md §4.11, Test 4.11.A5 (escape-hatch audit).

set -euo pipefail

MBTILES="${1:?Usage: validate-tiles.sh <path-to-mbtiles.mbtiles>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MANIFEST_FILE="$(dirname "$MBTILES")/MANIFEST.txt"

if [[ ! -f "$MBTILES" ]]; then
    echo "ERROR: ${MBTILES} not found."
    exit 1
fi

ERRORS=0
REGION_SLUG="$(basename "$MBTILES" .mbtiles)"

echo "=== Validating ${MBTILES} ==="

# ─── Check 1: SQLite schema integrity ───
if ! command -v sqlite3 &>/dev/null; then
    echo "WARNING: sqlite3 not found — skipping schema checks."
else
    echo ""
    echo "--- Check 1: SQLite schema ---"

    # Check required tables exist.
    TABLES="$(sqlite3 "$MBTILES" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;")"
    for req_table in metadata tiles tile_images; do
        if echo "$TABLES" | grep -q "^${req_table}$"; then
            echo "  [PASS] Table '${req_table}' exists."
        else
            echo "  [FAIL] Missing required table '${req_table}'."
            ERRORS=$((ERRORS + 1))
        fi
    done

    # Check metadata table has required keys.
    for key in name tilesUrl bounds center attribution; do
        VAL="$(sqlite3 "$MBTILES" "SELECT value FROM metadata WHERE name='${key}';" 2>/dev/null || true)"
        if [[ -n "$VAL" ]]; then
            echo "  [PASS] metadata.key='${key}' = ${VAL:0:80}"
        else
            # 'attribution' is optional; others are required.
            if [[ "$key" != "attribution" ]]; then
                echo "  [WARN] metadata.key='${key}' missing (optional for tileserver-gl)."
            fi
        fi
    done

    # Check bounds (from the old setup-tiles.sh validation logic).
    BOUNDS="$(sqlite3 "$MBTILES" "SELECT value FROM metadata WHERE name='bounds';" 2>/dev/null || true)"
    CENTER="$(sqlite3 "$MBTILES" "SELECT value FROM metadata WHERE name='center';" 2>/dev/null || true)"

    if [[ -n "$BOUNDS" ]]; then
        if echo "$BOUNDS" | awk -F, -v center="$CENTER" '
            NF != 4 { print "  [FAIL] bounds is not west,south,east,north"; exit 1 }
            $3 == 0 { print "  [FAIL] east bound is 0 (tilemaker B3 bug)"; exit 1 }
            $3 <= $1 { print "  [FAIL] east (" $3 ") <= west (" $1 ")"; exit 1 }
            $1 < -180 || $1 > 180 || $3 < -180 || $3 > 180 \
                    { print "  [FAIL] longitude out of range: " $0; exit 1 }
            $2 <  -90 || $2 >  90 || $4 <  -90 || $4 >   90 || $4 <= $2 \
                    { print "  [FAIL] latitude out of range: " $0; exit 1 }
            {
                n = split(center, c, ",")
                if (n != 3) {
                    print "  [WARN] center \"" center "\" is not lon,lat,zoom"
                } else {
                    if (c[1]+0 < $1+0 || c[1]+0 > $3+0 || c[2]+0 < $2+0 || c[2]+0 > $4+0) {
                        print "  [FAIL] center (" c[1] "," c[2] ") outside bounds " $0
                        exit 1
                    } else {
                        print "  [PASS] center (" c[1] "," c[2] ") inside bounds"
                    }
                }
            }
        '; then
            : # pass, nothing to do
        fi
    else
        echo "  [WARN] bounds not found in metadata."
    fi

    # Check tile count (must be > 0 for a useful tile set).
    TILE_COUNT="$(sqlite3 "$MBTILES" "SELECT COUNT(*) FROM tiles;" 2>/dev/null || echo "0")"
    if [[ "$TILE_COUNT" -gt 0 ]]; then
        echo "  [PASS] ${TILE_COUNT} tiles in tile table."
    else
        echo "  [FAIL] Tile table is empty (0 tiles)."
        ERRORS=$((ERRORS + 1))
    fi
fi

# ─── Check 2: SHA-256 vs manifest ───
if [[ -f "$MANIFEST_FILE" ]]; then
    echo ""
    echo "--- Check 2: Manifest verification ---"

    EXPECTED_HASH=""
    if command -v shasum &>/dev/null; then
        EXPECTED_HASH="$(shasum -a 256 "$MBTILES" | cut -d' ' -f1)"
    elif command -v sha256sum &>/dev/null; then
        EXPECTED_HASH="$(sha256sum "$MBTILES" | cut -d' ' -f1)"
    fi

    MANIFEST_HASH=""
    if command -v grep &>/dev/null; then
        MANIFEST_HASH="$(grep 'SHA-256:' "$MANIFEST_FILE" | sed 's/.*SHA-256: *//' | tr -d '[:space:]')"
    fi

    if [[ -n "$MANIFEST_HASH" && "$MANIFEST_HASH" != "unknown" ]]; then
        if [[ "$EXPECTED_HASH" == "$MANIFEST_HASH" ]]; then
            echo "  [PASS] SHA-256 matches manifest: ${EXPECTED_HASH:0:16}..."
        else
            echo "  [FAIL] SHA-256 mismatch!"
            echo "    File:       ${EXPECTED_HASH:0:16}..."
            echo "    Manifest:   ${MANIFEST_HASH:0:16}..."
            ERRORS=$((ERRORS + 1))
        fi
    elif [[ -n "$MANIFEST_HASH" && "$MANIFEST_HASH" == "unknown" ]]; then
        echo "  [WARN] Manifest SHA-256 is 'unknown' — could not verify."
    else
        echo "  [WARN] No SHA-256 in MANIFEST.txt — skipping hash check."
    fi
else
    echo ""
    echo "--- Check 2: No manifest found ---"
    echo "  [WARN] MANIFEST.txt not present in $(dirname "$MBTILES")."
    echo "  Consider running supply-tiles.sh to generate one."
fi

# ─── Check 3: File size sanity ───
echo ""
echo "--- Check 3: File size ---"
FILE_SIZE=""
if command -v stat &>/dev/null; then
    FILE_SIZE="$(stat -f %z "$MBTILES" 2>/dev/null || stat --format %s "$MBTILES" 2>/dev/null || echo "unknown")"
else
    FILE_SIZE="$(wc -c < "$MBTILES" | tr -d ' ')"
fi

if [[ "$FILE_SIZE" != "unknown" && "$FILE_SIZE" -gt 10000 ]]; then
    echo "  [PASS] File size: ${FILE_SIZE} bytes."
else
    echo "  [FAIL] File size: ${FILE_SIZE} — too small for a valid tile set."
    ERRORS=$((ERRORS + 1))
fi

# ─── Summary ───
echo ""
if [[ "$ERRORS" -eq 0 ]]; then
    echo "=== VALIDATION PASSED ==="
    echo "Region: ${REGION_SLUG}"
    echo "File: ${MBTILES}"
    exit 0
else
    echo "=== VALIDATION FAILED (${ERRORS} error(s)) ==="
    echo "Do NOT deploy this tile set to the air-gap target."
    exit 1
fi