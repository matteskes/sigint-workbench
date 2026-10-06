# UI Bug-Check Report — `make dev` bench, 2026-10-06

Findings from a fresh-stack bring-up (`docker compose build` → `make dev`)
plus a web-UI bug check. No interactive browser was available, so the UI
was exercised by HTTP probing (route/asset graph, REST, CORS, WS relay,
tile URLs) against the live bench and by reading the frontend code paths
those probes exercise. Two RTL-SDR Blog V4 dongles were on-air for the
duration, so every check ran against real streaming data, not empty
states.

Status:

| Item | Status |
| --- | --- |
| Fresh image build (incl. frontend, post-2026-10-06 edits) | `[done]` — all 6 images |
| `make dev` bring-up, healthy single-owner bench | `[done]` (see §5 — took 3 launches) |
| Route/asset/API/CORS/WS/tile sweep | `[done]` (§4) |
| Bugs filed (B1–B6, §2; N1, §3) | B1–B6 `[fixed]`; N1 `[documented]` |
| §2 re-verification in a real browser (Playwright MCP, WebKit) | `[done]` — found B6, B7, B8; fixed |

## 1. Bench shape under test

Same topology as HARDWARE.md §4: Docker TCP services (db :5432,
api-gateway :8080, ws-hub internal, tiles :8082), native UDP chain
(sdr-capture :9090 ctrl → iq-ingest :9000 → signal-processor :9010),
Vite dev server :5173. The API client talks to `http://localhost:8080`
and `ws://localhost:8080/ws` directly — Vite's `/api` + `/ws` proxy
entries exist but are bypassed by the client's absolute URLs unless
`VITE_API_URL` is set (frontend has no .env, so defaults apply).

## 2. Bugs — UI-visible

### B1 — favicon referenced but missing (404 on every page load)

`frontend/src/app.html` line 5 links `%sveltekit.assets%/favicon.png`,
but `frontend/static/` contains only `.gitkeep`. Every page load in dev
fires `GET /favicon.png → 404`; the nginx production build copies
`static/`, so it 404s in prod too. Browser tab shows the default icon
and each page load logs console noise.

Fix: add `frontend/static/favicon.png`, or drop the `<link>`.

Fixed 2026-10-06: added a generated 64x64 RGBA icon (slate rounded
square, cyan spectrum bars); the `<link>` stays.

### B2 — §20 setup check reports capture "ok" while its HTTP API is dead

`probeTCP` (`internal/api/server_settings.go:88`) marks the capture
component healthy on a bare TCP dial to `CAPTURE_CTRL_ADDR`. A wedged
process's kernel listen backlog still accepts dials, so during the §5
incident the Setup page showed `capture: ok:true` while every real
request to the control API hung and the SDR rail's
`GET /api/sdrs/{id}/status` 502'd ("capture status: sdr-capture
unreachable" in the gateway log).

Fix: make the §20 probe issue `GET /api/v1/status` with a short timeout
(~2 s) and require a 200, matching what the UI actually needs.

Fixed 2026-10-06: the capture probe now runs `probeHTTP` (GET
`/api/v1/status`, 200 required) inside the existing 2 s budget;
ws-hub/recorder stay dial-only.

### B3 — Arizona MBTiles TileJSON metadata is corrupt

`GET :8082/data/v3.json` returns `bounds: [-114.8325, 30.05891, 0,
37.00596]` (maxLon = 0 — the Greenwich meridian, not ~-109) and
`center: [-57.41625, 33.532435, 7]` (mid-Atlantic). The UI is
unaffected today only because `MapView.svelte:45` hard-codes its camera
(San Francisco) and never reads TileJSON center/bounds; anything
bounds-driven (tileserver-gl's own preview page, a future
fit-to-data/fit-to-receivers feature) lands in the ocean.

Fix: rebuild `tiles/data/north-america_us_arizona.mbtiles` so the
`metadata` table's bounds/center are correct, or patch the metadata
table in place.

Fixed 2026-10-06: metadata patched in place (east -108.994756, center
-111.913628,33.532435,7) and a post-render Step-5 sanity check added
to `tiles/setup-tiles.sh` (rejects east == 0 and a center outside the
bounds).

### B4 — airband noise classified as confident wideband-FM "aviation"

The processor (SIGNAL_TTL=30, rules classifier) publishes noise peaks at
~-88 dBm as signals, and airband-range hits come out as
`modulation: WFM, class: aviation, confidence: 0.85` (observed live at
121.954 MHz and 122.664 MHz — AM voice band, not broadcast FM). The
Signals table and map render these as real, confidently-classified
signals. Not frontend code — a detector-threshold/classifier issue —
but it is what users see first on the dashboard.

Fix: (1) gate on SNR, not absolute power — a peak must clear the
record's own noise floor by a configurable margin, scale-free against
§5.6 calibration; (2) correct the airband rule (AM voice, moderate
confidence; non-voice bandwidths stay Unknown).

Fixed 2026-10-06: `dsp.PeakDetector.MinSNRDB` (new `peak_detection.
min_snr_db` knob, default 10 dB, exposed in §20 settings) drops
sub-floor peaks using the previously discarded `noiseFloor`; the
airband rule now yields AM/aviation at 0.6 (0/200 kHz bandwidths →
Unknown/0.3). Regression tests in `rules_test.go` and
`main_test.go` (`TestPeakDetectorSnrGate`).

### B5 — `make dev` restart fragility: zombie bench with false-green checks

Observed end-to-end on 2026-10-06 (this is what turned one bring-up
into three):

1. A non-clean kill of `make dev` (SIGKILL; the trap cannot run) leaves
   capture/ingest/processor orphaned, still holding the USB dongles and
   UDP :9000/:9010. Orphans can additionally end up SIGTTIN-stopped
   (state `T`) under job control — a stopped capture still accepts TCP
   dials (feeds B2) but serves no HTTP.
2. The next `make dev` pkills by name, then immediately spawns
   replacements: capture dies with `usb_claim_interface error -3` (old
   instance hadn't released USB), ingest/processor die with
   `bind: address already in use`.
3. The readiness loop (`lsof -nP -i :9000`, `scripts/dev-macos.sh:100`)
   checks for ANY listener and passes against the **orphaned** ingest —
   so the script prints "iq-ingest UDP :9000 is up" and continues with a
   bench that has no live replacements.

Fix (script-side, no app changes needed): after `pkill`, poll until
:9000/:9010/:9090 are actually free (`pkill -9` stragglers) before
spawning; after spawn, verify the listener is the child just launched
(`$!`), not merely that something holds the port. Optionally treat a
child that exits within a few seconds as fatal.

Fixed 2026-10-06: `scripts/dev-macos.sh` gained `wait_port_free`
(20 s TERM grace → `pkill -9` → 10 s KILL grace → fatal with holder
PIDs) run for all three ports before anything spawns, and
`verify_child` (fail fast on early child exit with the log tail;
success only when the child's own PID holds its port) after each
spawn — the any-listener readiness loop is gone. Smoke-tested all
five paths live (free/held/escalation/never-frees/early-exit).

### B6 — map style missing `glyphs`: label layer can't render (console error on every map load)

Found during the browser-based §2 re-verification (Playwright MCP,
WebKit) — the HTTP-probing sweep could not see console errors.
`frontend/src/lib/map/config.ts` declares a `labels` symbol layer
(`layers[4]`, `text-field`), but the style has no `glyphs` property,
which MapLibre requires for any text rendering. Every map load logs
`layers[4].layout.text-field: use of "text-field" requires a style
"glyphs" property` and OSM place names never render.

Fix: set `glyphs` on the style and pin the layer's `text-font` to a
stack the tiles server actually serves — probed live: `Noto Sans
Regular` → 200 `application/x-protobuf`; Open Sans / Arial Unicode
variants → 400. `VITE_FONTS_URL` overrides, mirroring `VITE_TILE_URL`.

Fixed 2026-10-06: `glyphs: FONTS_URL` added to the style and
`text-font: ['Noto Sans Regular']` on the labels layer; the validation
error is gone in the live browser. Source loading then proceeded for
the first time and surfaced B7, which this failure had been masking.

### B7 — basemap vector source passed a tile template as TileJSON `url`

`config.ts` built the `osm` source as `{ type: 'vector', url: TILE_URL }`
where `VITE_TILE_URL` is a `{z}/{x}/{y}` template. MapLibre's `url`
expects a TileJSON, so the map fetched the literal templated path
(`data/v3/%7Bz%7D/%7Bx%7D/%7By%7D.pbf` → 404) and the vector basemap
never loaded — only the background color painted. Fully masked by B6
(the style validation failure aborted the pipeline before the source
fetch); it surfaced the moment B6 was fixed.

Fix: move the template to `tiles: [TILE_URL]` — MapLibre's template
form. `VITE_TILE_URL` semantics unchanged.

Fixed 2026-10-06: verified in-browser — tile requests now hit real
`…/{z}/{x}/{y}.pbf` paths; Arizona-covering tiles return 200 with
geometry, off-coverage tiles 204 as designed. Loading those tiles
surfaced B8.

### B8 — labels read `name`, but this tileset stores `name:latin`

Even with B6+B7 fixed, the `labels` layer rendered nothing: its
`text-field` was `['get', 'name']`, while this tileset's `place`
features carry `name:latin` — confirmed by decoding
`…/7/24/49.pbf` (keys `rank`, `class`, `name:latin`; a feature with
`class=city`, name "Page"). A missing property makes `text-field`
evaluate to null: zero symbols, no glyph fetch, and no error at all.

Fix: `['coalesce', ['get', 'name:latin'], ['get', 'name']]`.

Fixed 2026-10-06: verified in-browser — MapLibre now fetches
`/fonts/Noto Sans Regular/0-255.pbf` (200, its first-ever glyph
request) and the vector basemap visibly renders (place/water
geometry on screen).

## 3. Ops note

### N1 — backgrounded `make dev` freezes under job control

`npm run dev` (Vite) reads stdin for its interactive help; when `make
dev` is backgrounded from a terminal, Vite gets SIGTTIN and stops
(state `T`) — the port listens but nothing serves. Workaround used for
this run: `nohup make dev < /dev/null > /tmp/sigint-dev.log 2>&1 &`.
Worth one line in the README/Makefile comments for anyone scripting the
bench.

Documented 2026-10-06: the `nohup make dev < /dev/null > /tmp/
sigint-dev.log 2>&1 &` recipe is in the `dev:` target comment
(Makefile) and the README's macOS-development section.

## 4. Verified-good checklist (all live-data, 2026-10-06)

| Area | Result |
| --- | --- |
| Routes `/`, `/signals`, `/spectrum`, `/analysis`, `/recordings`, `/setup` | clean SPA shells, title OK (ssr=false, as designed) |
| SvelteKit dev boot chain (kit `entry.js`, generated `app.js`) | 200 |
| `GET /health`, `/api/signals`, `/api/sdrs`, `/api/recordings`, `/api/settings`, `/api/setup/state` | healthy; DB history intact across restarts |
| CORS preflight + PUT/POST with `Origin: http://localhost:5173` | correct `Access-Control-*` echo |
| WS handshake + relay at `/ws` | 101; ~12,800 `signal.update` + 79 `spectrum.frame` + 2 `signal.new` in 8 s |
| `sdr.status` absence during idle monitoring | correct — dedup emits only on state change (§14.4.3); sweep state reaches UI via REST `GET /api/sdrs/{id}/status` |
| Capture status proxy `GET /api/sdrs/{id}/status` | full live payload when the bench is single-owner |
| Retune `PUT /api/sdrs/rtlsdr-1` | 200, row echoed, forwarded to capture |
| Scan toggle `POST /api/sdrs/rtlsdr-0/scan` on→off | sweep genuinely stepped (~2 MHz/s); parked state restored |
| Tiles `GET :8082/data/v3/{z}/{x}/{y}.pbf` | 200 `application/x-protobuf`, real geometry in-state (Arizona); off-state tiles 204 |
| `/api/signals/{id}/track`, `.../annotations` | sane empties for fresh/stationary signals |
| Recordings `[]` | by design in this topology (recorder sees no IQ, HARDWARE.md §4) |

## 5. Bring-up incident record (feeds B5)

- Launch 1 (`make dev` backgrounded, terminal stdin attached): Vite froze
  with SIGTTIN (N1); killing the wrapper with SIGKILL skipped the
  cleanup trap → orphaned capture/ingest/processor held USB + ports.
- Launch 2: replacements all died on arrival (USB claim -3, bind errors)
  while the script's port check passed against the orphans → bench
  looked up but was a zombie; control API hung → SDR rail 502, masked
  by B2's dial-only probe reporting ok.
- Launch 3 (after explicit `kill -9` of orphans and port/USB
  verification): fully healthy on first pass — every §4 check green.
  Residual state: rtlsdr-0 parked at 150.62 MHz (sweep cursor position
  when the toggle test parked it) instead of the 146.52 boot default —
  documented §7.4 semantics; the next sweep resumes from there.
