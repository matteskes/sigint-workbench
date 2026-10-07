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
| Spectrum canvas-text re-check in the live browser (devicePixelRatio 2) | `[done]` — found B9; fixed |
| Shell popover + §15 keyboard sweep in the live browser (Playwright MCP) | `[done]` — found B10, B11; fixed |
| Aggressive sweep — keyboard contract, WS kill/F5, health degradation, drag races, deep-link + table abuse, viewports, multi-client (§2 B12–B16, §6) | `[done]` — found B12–B16; fixed |

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

### B6 — map style missing `glyphs`: label layer can't render

Found during the browser-based §2 re-verification (Playwright MCP,
WebKit) — every map load logged a console error, which the
HTTP-probing sweep could not see.
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

### B9 — spectrum text rendered into bins-wide bitmaps, smeared by CSS

The line and tick `<canvas>` elements were created with fixed
`width={bins}` (256 px) bitmaps while CSS displayed them at the full
panel width (~440 px on this column), so the browser upscaled each
~1.7×. Every glyph drawn into those bitmaps — the dB grid labels,
frequency tick labels, and signal/marker labels — came out smeared
and overlapping (the top-left grid label read as ".2 dB" mush). A
second layer to it: the signal overlay reserved a hardcoded 17 px of
lane 0 for the first dB grid label, which three-character labels
("-30") already overran, so marker text could draw straight into the
grid label.

Fix: `fitCanvas()` sizes the text-bearing canvases (line + ticks) to
the CSS box × devicePixelRatio at draw time, with the 2D context
working in CSS pixels; a ResizeObserver on the panel marks the frame
dirty on container resize so the backing stores re-fit. The waterfall
keeps its native bins × rows heat bitmap — stretching heat data is
intended, and the §19.4 drag→bin math maps X through
`waterfallEl.width`. The overlay's lane-0 exclusion zone is now
`2 + measureText(firstDbLabel).width + 1`, with the marker font at
9 px and lane baselines at y=11/21 to sit clear of the grid labels.

Fixed 2026-10-06: verified in the live browser at devicePixelRatio 2
— device-scale crops of the plot corner and the dB-label gutter show
crisp, non-overlapping text; the smear is gone.

### B10 — shell and map popovers never dismiss on outside click

The health chips popover (`HealthChips.svelte`), the ⚙ settings menu
(`AppBar.svelte`), and the map layer-toggles popover
(`routes/+page.svelte`) all toggle `open` from their trigger button and
render `{#if open}` — and that was the only path back to closed.
Clicking anywhere else left the panel floating over the view (z-40, on
top of the map and the signal table); only a second click on the very
same trigger would put it away.

Fix: a shared `popoverDismiss` Svelte action (`$lib/ui/actions.ts`) —
a document-level `pointerdown` listener in the capture phase plus an
`Escape` keyhandler (§15: "Esc — close popover"), both guarded by
`open`, attached to each popover's positioning wrapper so the
trigger's own toggle and the panel's controls (checkboxes, links)
never count as "outside". Applied to all three popovers.

Fixed 2026-10-06: verified in the live browser — each popover opens
from its trigger, ignores clicks inside the panel (the layer
checkboxes still toggle), closes on any outside press, and closes on
Esc.

### B11 — the §15 keyboard map was never attached (every shortcut dead)

`+layout.svelte` defines the entire §15 keyboard map in
`handleKeydown` — 1–6 view switches, `?` overlay, `/` search focus,
j/k/↑/↓ navigation, Enter select, Esc close, Space audio — but nothing
ever listened for it: the file had no `<svelte:window>`, and no other
component attaches the handler. UI-DESIGN §15 documents all of these
as working (and the `?` overlay itself advertises them); in reality
every shortcut was dead code. This is also why Esc did nothing for
B10's popovers.

Fix: `<svelte:window onkeydown={handleKeydown} />` in the layout —
the one line the file was always meant to have.

Fixed 2026-10-06: verified in the live browser — `?` opens the
shortcuts overlay and Esc closes it; `2` then `1` switch Spectrum →
Operations.

### B12 — global keymap swallowed Space/Enter activation (a11y regression from B11)

Wiring B11 revived the §15 `handleKeydown`, but its `Space` case called
`e.preventDefault()` unconditionally and its `Enter` case whenever a row
was highlighted. A keyboard user who had Tabbed to any button or link
could not activate it: Space on the focused ⚙ triggered
`toggleLiveAudio()` (a no-op) instead of the click, and Enter on a
focused view link selected the highlighted signal instead of
navigating. WebKit masks the mouse path — Safari-family browsers never
focus buttons on click — but Tab users hit it on every control.

Fix: an `isActivator(target)` guard (`BUTTON`/`A`/`SUMMARY`/
`[role=button]`) — those targets keep native activation; the map's
Space/Enter branches only run from non-activatable focus (body, table,
map).

Fixed 2026-10-06: live-verified — keyboard-focused ⚙ + Space opens the
menu; Enter on a focused Spectrum link navigates instead of hijacking;
Space/Enter from body focus still drive audio toggle / row selection.

### B13 — one Esc closed every stacked layer at once

`popoverDismiss`'s document-level Escape listener and the layout's
`svelte:window` Esc chain (§15: shortcuts overlay → receiver card →
selection) fired on the same keystroke: with the ⚙ menu open under the
shortcuts overlay, a single Esc closed both.

Fix: the action's Escape listener (already capture-phase) calls
`e.stopImmediatePropagation()` after dismissing, so the shell handles
the next layer on the next press — topmost first, one layer per
keystroke.

Fixed 2026-10-06: live-verified — menu + overlay stacked: first Esc
closes the menu only (overlay stays), second Esc closes the overlay.

### B14 — stale `?signal=` after Esc (inspector resurrects on reload)

The `?signal=<id>` deep-link mirror lived inside `Inspector.svelte`,
but the inspector only mounts while a signal is selected
(`{#if $selectedSignal && $inspectorOpen}`). Esc clears the selection →
the component unmounts → the effect's delete-the-param branch never
runs → the URL keeps `?signal=<id>`, so refreshing or sharing the link
reopens an inspector the user had just closed.

Fix: the mirror moves into `+layout.svelte` (which survives selection
changes), with a no-op guard when the param already matches.

Fixed 2026-10-06: live-verified — select a signal → URL mirrors; Esc →
URL returns to `/`; reload stays clean.

### B15 — raw `history.replaceState` (SvelteKit router console warning)

The B14-era mirror called `window.history.replaceState` directly, so
every selection logged SvelteKit's "Avoid using history.pushState(…)
and history.replaceState(…)" warning — the router is not kept in sync
by raw history writes.

Fix: same relocation as B14 — the layout mirror uses `replaceState`
from `$app/navigation`.

Fixed 2026-10-06: console clean (0 errors, 0 warnings) on fresh load
and through a selection round-trip.

### B16 — app bar overflowed every page at narrow viewports

The header was a fixed-height, non-wrapping flex row; its nav (~451 px)
plus the right-side control cluster forced `document.scrollWidth` to
891 px at a 375 px viewport — the whole app scrolled sideways on every
route (the map/table were unusable without horizontal panning).

Fix: the header wraps (`min-h-12`, `flex-wrap`, column/row gaps) and
the nav scrolls internally (`min-w-0 max-w-full overflow-x-auto
whitespace-nowrap`) instead of pushing the document wide.

Fixed 2026-10-06: live-verified at 375×667 on `/`, `/spectrum`, and
`/setup` — `scrollWidth` 375 == `clientWidth`, nav inside the viewport.

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

## 6. Aggressive sweep — method + checked-good (2026-10-06)

A second, adversarial pass over the live UI (Playwright MCP, WebKit,
real on-air data): keyboard-contract attacks on the fresh B11 wiring,
in-page WebSocket kills with 100 ms state polling, full-network
degradation via `context.setOffline`, waterfall drag races (release
outside the canvas, mid-drag route switch), deep-link and table abuse,
viewport stress, and two-client fan-out. Findings B12–B16 (§2) came
out of this pass; everything probed that held is recorded here.

| Probe | Result |
| --- | --- |
| WS drop → reconnect → F5 banner | works: killed the store's socket in-page — pill `live` → `reconnecting (1)` at ~120 ms → `live` at ~1.1 s with a fresh socket, "connection restored — state reloaded" banner shown and self-dismissed at ~4 s; a rapid double-kill also recovers |
| Health degradation | `context.setOffline(true)` → popover renders the exact §3.2 amber path: "status probe failed — showing last known state · checked 0s ago · every 30s"; recovers on restore |
| setOffline caveat | offline emulation only blocks *new* requests — an established WebSocket survives it, so the pill never moves under `setOffline`; the WS path must be tested by killing the socket (done above) |
| Waterfall drag races | happy-path drag commits (`sel 95.618–96.341 MHz`), degenerate click clears, and the canvas's `on:pointerleave={dragUp}` self-commits/clears when the pointer leaves mid-drag — no stuck drag state, no phantom span overlay; a mid-drag route switch unmounts cleanly |
| Deep-link abuse | `?signal=<garbage>` (injection-style string) and `?signal=` soft-fail: page renders, inspector stays closed; a valid live id opens the inspector and mirrors into the URL |
| Table abuse | search with regex metacharacters (`.*[`) and a 300-char unicode string filter gracefully to the "no signals match" empty state (substring semantics, no crash); Freq sort toggles direction (96.234 → 97.294 → 94.909 MHz); the "without position" chip toggles (row set unchanged here because nearly all live rows are unlocated on this bench) |
| Multi-client fan-out | two tabs simultaneously: both connection pills `live`, both receive the stream (A1's N-client hub) |
| Viewport stress | 2560×1440 and 1280×480 render without overflow; 480 px height keeps the shell usable (B16 covered the 375 px failure) |
| Keyboard contract | typing guards hold — `/` focuses search, digits/letters land in the field with no route switch; `?` toggles the overlay; Esc does not blur a focused input (guard early-return, by design) |
| `/api/signals/{id}/track` 404 | browser-inherent console line for the SPEC'd "no track" response (§9.4); the app handles it — no unhandled rejection, inspector renders — not a frontend defect |
