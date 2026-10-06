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
| Bugs filed (B1–B5, §2; N1, §3) | `[open]` — none fixed yet |

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

### B4 — airband noise classified as confident wideband-FM "aviation"

The processor (SIGNAL_TTL=30, rules classifier) publishes noise peaks at
~-88 dBm as signals, and airband-range hits come out as
`modulation: WFM, class: aviation, confidence: 0.85` (observed live at
121.954 MHz and 122.664 MHz — AM voice band, not broadcast FM). The
Signals table and map render these as real, confidently-classified
signals. Not frontend code — a detector-threshold/classifier issue —
but it is what users see first on the dashboard.

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

## 3. Ops note

### N1 — backgrounded `make dev` freezes under job control

`npm run dev` (Vite) reads stdin for its interactive help; when `make
dev` is backgrounded from a terminal, Vite gets SIGTTIN and stops
(state `T`) — the port listens but nothing serves. Workaround used for
this run: `nohup make dev < /dev/null > /tmp/sigint-dev.log 2>&1 &`.
Worth one line in the README/Makefile comments for anyone scripting the
bench.

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
