# SIGINT Workbench — E2E Test Suite Specification

**Based on:** `docs/UI-BUGCHECK.md` (B1–B19) and `docs/STACK-AUDIT.md` (A1–A23)
**Scope:** End-to-end testing across all layers — SDR hardware → UDP pipeline → API → WebSocket → Frontend UI
**Runtime:** `make dev` stack (Docker TCP services + native SDR capture + Vite dev server)
**Tools:** Playwright (frontend), curl/WSCat (WebSocket probes), extended smoke-test.sh (UDP pipeline)

---

## 1. Languages & API Types Inventory

### 1.1 Languages Used

| Language | Where Used |
|---|---|
| **Go 1.25** | All 6 backend services (`cmd/sdr-capture`, `iq-ingest`, `signal-processor`, `recorder`, `api-gateway`, `ws-hub`), bench tools (`rtl-list`, `rtl-calibrate`, `smoke-frames`), all `internal/` packages |
| **TypeScript 5.7** | Frontend: API client, audio decoder, store modules (signals, audio, connection, spectrum, sdrs, tdoa, health), hooks |
| **Svelte 5** | 22+ `.svelte` components: views (analysis, recordings, signals, spectrum, setup, home), shell (App Bar, ConnectionPill, ShortcutsOverlay), components (Inspector, MapView, AudioPlayer, LiveAudioPlayer, VUMeter, SpectrumView, etc.) |
| **SQL** | PostGIS schema (`db/init.sql` + 5 migrations) — 6 tables: `sdrs`, `signals`, `recordings`, `tracks`, `annotations`, `verifications` + `app_settings` |
| **Bash** | Smoke test (`scripts/smoke-test.sh`), dev scripts (`scripts/dev-macos.sh`), ONNX fetcher (`scripts/fetch-ort.sh`), tile setup (`tiles/setup-tiles.sh`) |
| **YAML** | Per-service configs (`config/*.yaml`), Docker Compose (`docker-compose.yml`), CI (`docker-compose*.yml`) |
| **Nginx Config** | Production reverse proxy (`docker/nginx.conf`) |

### 1.2 API Types

| API Type | Protocol | Endpoint / Method | Service |
|---|---|---|---|
| **REST JSON** | HTTP GET/POST/PUT/DELETE | `/health`, `/api/signals`, `/api/signals/{id}`, `/api/signals/{id}/annotations`, `/api/signals/{id}/track`, `/api/recordings`, `/api/recordings/{id}/audio`, `/api/recordings/{id}/tfr`, `/api/sdrs`, `/api/sdrs/{id}/status`, `/api/sdrs/{id}`, `/api/sdrs/{id}/scan`, `/api/settings`, `/api/events/signal-tower`, `/setup`, `/api/v1/status`, `/api/v1/frequency`, `/api/v1/gain`, `/api/v1/scan` | `api-gateway` (proxies to capture, recorder, settings) |
| **WebSocket (upgrade)** | HTTP 101 Upgrade | `GET /ws` (event relay), `GET /ws/audio` (live audio relay) | `api-gateway` relays to `ws-hub` / `recorder` |
| **WebSocket frames** | Binary + Text frames | Audio: `audio.meta` (text hello) → binary Opus packets; Events: JSON `ws.Event` with `type` + `payload` | `ws-hub` (event types: `signal.new`, `signal.update`, `signal.removed`, `signal.tdoa`, `sdr.status`, `audio.level`, `track.update`, `spectrum.frame`) |
| **UDP binary (IQ frames)** | UDP datagrams | v1: raw int16 IQ, v2: header (magic, format, seq, sampleIndex) + IQ | `sdr-capture` → `iq-ingest` → `signal-processor` / `recorder` |
| **UDP binary (IQ frames v2)** | UDP, `stream_format: sdr2` | Header: magic bytes, format id, seq counter, sample index, IQ data | Same pipeline |
| **HTTP JSON (hub ingest)** | POST `application/json` | `POST /api/events` — JSON event objects from pipeline services | `ws-hub` |
| **HTTP JSON (recorder TFR)** | POST `application/json` | `POST /api/recordings/{id}/tfr` — time-frequency representation upload | `recorder` |
| **File serving (WAV, audio)** | HTTP GET | `GET /api/recordings/{id}/audio` — serves recorded WAV/audio files | `api-gateway` proxies to recorder's file system |
| **Tile serving (MBTiles)** | HTTP GET | `GET /data/v3/{z}/{x}/{y}.pbf` — vector tiles from MapTiler | `tiles` (tileserver-gl) |

---

## 2. Lessons from UI-BUGCHECK (B1–B19)

### 2.1 Bug Findings & Extracted Lessons

| Bug | Description | Lesson |
|---|---|---|
| **B1** | Missing favicon (404 on every page load) | Every static asset referenced in HTML must exist on disk; 404s in the critical path create console noise that masks real errors |
| **B2** | TCP dial reports capture "ok" while its HTTP API is dead | A TCP open to a wedged process returns "ok" while the API is dead; probes must validate HTTP status codes, not just TCP connectivity |
| **B3** | Arizona MBTiles TileJSON metadata is corrupt (bounds=[…0,…]) | GeoJSON/MBTiles bounds west of Greenwich can produce mid-Atlantic centers; validate/clip bounds before rendering |
| **B4** | Confidence threshold too low — classifying noise spikes as confident "aviation FM" (0.85) | Confidence thresholds and band-specific rules need tighter guardrails; noise-only frames should never be classified as high-confidence signals |
| **B5** | Stale processes blocking ports/USB after crash | Previous bench sessions can outlive pkill; need pre-flight cleanup that SIGKILL'd processes skip |
| **B9** | Canvas label smearing at high devicePixelRatio (2×) | Fixed-size bitmaps stretched by CSS smear text; use native-resolution canvases with CSS-pixel coordinate mapping |
| **B10/B11** | Keyboard nav broken at 375px viewports | Narrow viewports caused horizontal overflow, breaking popover and navigation accessibility |

---

## 3. Lessons from STACK-AUDIT (A1–A23)

### 3.1 Audit Findings & Extracted Lessons

| Audit | Description | Lesson |
|---|---|---|
| **A1** | Hub `Broadcast()` writes synchronously to all clients — one slow client blocks the entire hub | Per-client drop queues are required; a slow client must never stall broadcast to active clients |
| **A2** | Four+ editable settings knobs are parsed but never consumed by services | Settings saved via API must actually change service behavior; test the full save→effect chain, not just file I/O |
| **A3** | Nginx `/ws/` proxies directly to ws-hub, bypassing the gateway relay | WebSocket routes must go through the gateway (which handles origin checking, CORS, close code forwarding); direct hub access is dead-wrong |
| **A4/A5** | `.env.example` + SPEC carry retired or unconsumed variables | Documented env vars must match what services actually read; dead vars mislead implementers |
| **A8** | `signal-processor.yaml` `scan:` block is parsed and dead | Dead config blocks should be removed; every config key must either be wired or explicitly documented as deprecated |
| **A9** | `audio.sample_rate` was unvalidated against 48 kHz demod stack | Sample rate mismatches produce subtly wrong audio; now a hard startup error (A9 remediation) |
| **A10/A11** | Two near-duplicate recordings-list queries; different purge order | Single purge policy needed; query deduplication for consistency |
| **A12** | Hub client connections had no read deadline or keepalive | Zombie peers accumulate; every WS client connection needs a read deadline with ping/pong timeout |
| **A13** | Dead `map/config.ts` exports (`API_URL`/`WS_URL`); stale WS fallback targets the hub | Dead code in frontend exports must be removed; WS fallback must target the gateway, never the hub |

---

## 4. E2E Test Suite

### 4.1 Category: API Health & Connectivity Probes
*Addresses: B2, A1, A12*

#### Test 1.1 — HTTP Health Probe Validates Actual API (from B2)
**Scenario:** A service process is wedged — its kernel listen backlog accepts TCP dials, but the HTTP stack is dead.

| Step | Action | Expected |
|---|---|---|
| 1 | `GET http://localhost:8080/health` | 200, `{"status":"ok"}` |
| 2 | `GET http://localhost:8081/health` | 200, `{"status":"ok","clients":N}` |
| 3 | `GET http://localhost:127.0.0.1:9090/api/v1/status` | 200, per-device status array (at least one SDR) |
| 4 | Verify all three responses contain valid JSON with expected fields | Parse JSON, assert fields present |

**Why:** A TCP dial would return "ok" for all three even when wedged. Only an HTTP 200 validates the API is functional.

#### Test 1.2 — Hub Backpressure Simulation (from A1) `[implemented]`
**Test file:** `tests/e2e/api/backpressure_test.go`
**Scenario:** Two WebSocket clients connect to `/ws`; one stops reading.

| Step | Action | Expected |
|---|---|---|
| 1 | Connect Client A and Client B to `ws://localhost:8081/ws` | Both receive 101 |
| 2 | Pipeline 50 events via `POST /api/events` | Both receive all 50 |
| 3 | Stop Client A's read loop (but keep connection open) | Hub does NOT block |
| 4 | Pipeline another 50 events | Client B receives all 50 within 500ms |
| 5 | Wait for Client A eviction (writeWait=5s) | Hub.ClientCount drops to 1 |

**Why:** Without per-client queues, Client A's stopped read blocks the hub's broadcast goroutine, starving Client B.

**Test functions:**
- `TestHubBackpressure` — full backpressure lifecycle (Steps 1-5)
- `TestHubNonblockBroadcast` — stress test with 5 concurrent clients, 200-event burst
- `TestHubSlowClientQueueShedding` — validates queue shedding preserves fast client delivery

#### Test 1.3 — Zombie Client Eviction (from A12)
**Implementation:** `tests/e2e/api/zombie_eviction_test.go::TestZombieClientEviction`
**Scenario:** A WS client connects, sends no messages, receives no pongs.
**Steps:**
| Step | Action | Expected |
|---|---|---|
| 1 | Connect Client A (zombie — never reads) and Client B (active) | Both report 101 Upgrade |
| 2 | Verify hub `ClientCount()` is ≥ 2 | `GET /health` reports clients ≥ 2 |
| 3 | Post events — Client B receives them, Client A stays silent | Hub delivers to Client B only |
| 4 | Wait 120s for eviction timeout (shortened for local dev) | Hub evicts Client A, decrements count |
| 5 | Close Client B | Hub client count → 0 |

#### Test 1.4 — Close Frame Forwarding (from B17, §10.4.4)
**Implementation:** `tests/e2e/api/close_frame_forwarding_test.go::TestCloseFrameForwarding`
**Scenario:** One client sends a WebSocket close frame with a specific code and reason; verify peer clients receive the same close frame through the full gateway relay chain.
**Steps:**
| Step | Action | Expected |
|---|---|---|
| 1 | Connect Client A (sends close) and Client B (observer) | Both report 101 Upgrade |
| 2 | Drain initial hub messages | Clean state |
| 3 | Client A writes close frame (code 1000, custom reason) | Hub delivers close frame to peers |
| 4 | Client B reads close frame | `CloseError` with matching code + reason |
| 5 | Verify hub `ClientCount()` decrements | Count drops by 1 |
| 6 | Repeat through gateway relay (port 8080/ws) | Gateway relay forwards close frames |

---

### 4.2 Category: Frontend UI Behavior
*Addresses: B9, B10, B11, B12, B13, B14, B15, B16, B19*

#### Test 2.1 — Canvas Text Rendering at High DPR (from B9)
**Platform:** Playwright with `deviceScaleFactor: 2` (MacBook Retina simulation)

| Step | Action | Expected |
|---|---|---|
| 1 | Navigate to `/spectrum` | SpectrumView renders |
| 2 | Verify canvas has `devicePixelRatio` 2 (check `canvas.width / canvas.clientWidth`) | Native-resolution canvas |
| 3 | Verify label text (e.g., "-30", "0.5", "480.0") is crisp, not smeared | Labels at correct pixel positions |
| 4 | Verify labels do not overlap adjacent lanes (lane 1 reserved zone, lane 2 unconstrained) | No text overlap |
| 5 | Verify waterfall heatmap maintains native bins × rows (not stretched) | Heat map bins align with frequency labels |

**Why:** Fixed-size bitmaps stretched by CSS smear every label into unreadable mush at DPR 2.

#### Test 2.2 — Keyboard Interaction Contract (from B12, B13)
**Platform:** Playwright with keyboard emulation

| Step | Action | Expected |
|---|---|---|
| 1 | Press `/` | Search input receives focus, shows focus ring |
| 2 | Type digits and letters | Text lands in search field, no route switch triggered |
| 3 | Press `?` | Shortcuts overlay toggles open/closed |
| 4 | Focus a button (Tab), press `Space` | Button activates natively (not swallowed) |
| 5 | Focus a link (Tab), press `Enter` | Navigation triggered (not swallowed) |
| 6 | Press `Escape` once while a popover is open | Only that popover closes; others remain open |
| 7 | Press `Escape` while an input is focused | Input retains focus (no blur) |

**Why:** The global keyboard shortcuts overlay must respect native browser activation keys and close one layer per Escape press.

#### Test 2.3 — Deep-Link URL Lifecycle (from B14, B15, B19)
**Platform:** Playwright with URL manipulation

| Step | Action | Expected |
|---|---|---|
| 1 | Navigate to `/signals` | Page renders, URL is clean (no `?signal=`) |
| 2 | Click a signal row to open the Inspector | URL becomes `?signal=<valid-id>` |
| 3 | Click "Clear Selection" (Esc button) | `?signal=` is removed via `history.replaceState`, no console errors |
| 4 | Hard load with `?signal=<garbage-string>` | Page renders, Inspector stays closed, no `replaceState` errors |
| 5 | Hard load with `?signal=<valid-live-signal-id>` | Page renders, Inspector opens, URL mirrors correctly |
| 6 | Verify the Inspector unmounts when the signal is no longer live | `?signal=` is cleaned from URL automatically |

**Why:** When the Inspector unmounts (selection cleared), the stale `?signal=` must not remain in the URL, causing errors on subsequent navigation.

#### Test 2.4 — Narrow Viewport Layout (from B16)
**Platform:** Playwright viewport resizing

| Step | Action | Expected |
|---|---|---|
| 1 | Resize viewport to 375 × 812 (iPhone SE) | Page renders, no horizontal scrollbar |
| 2 | Resize to 480 × 800 | Page renders, no horizontal scrollbar |
| 3 | Resize to 1280 × 480 (landscape mobile) | Shell remains usable, navigation wraps internally |
| 4 | Resize to 2560 × 1440 (4K monitor) | No overflow, no clipping |
| 5 | On 375px: verify navigation uses internal scrolling, not document overflow | Nav items wrap to second row/column, scroll container is bounded |

**Why:** Every page horizontally overflowed at 375px in the original bug. Internal scrolling + nav wrapping fixes this.
| Step | Action | Expected |
|---|---|---|
| 1 | Connect a WS client to `ws://localhost:8080/ws` | 101 Upgrade |
| 2 | Do not read from, do not pong — wait 120 seconds | Hub eviction trigger fires |
| 3 | `GET http://localhost:8081/health` | `clients` count decremented by 1 |
| 4 | Verify the connection is actually closed on the server side | No goroutine leak |

**Why:** Without read deadlines/keepalive, zombie peers accumulate and consume server resources.
| **B12** | Global keymap swallowing native button/link activation | Space/Enter on buttons and links must not be swallowed by the global keyboard shortcuts overlay |
| **B13** | Escape key closing all popovers at once | Multiple Escape key presses should close one layer per press, not all at once |
| **B14/B15** | Stale `?signal=` stranded in URL when Inspector unmounts | When Inspector unmounts, the deep-link URL remains with a stale signal ID; the URL must be cleaned on selection clear |
| **B16** | Horizontal overflow at 375px viewports | Internal scrolling + nav wrapping needed; entire pages went wide on narrow viewports |
| **B17** | WS close code degraded from 1000 (clean) to 1006 (abnormal) at the gateway relay | Close codes and reasons must be forwarded intact through all relay layers |
| **B18** | Native recorder received 4.7k pkt/s; containerized recorder starved | Services with high-rate UDP I/O must run on the correct network path; native vs Docker parity matters |
| **B19** | `replaceState` errors on hard load with `?signal=` | Deep-link URL on initial load must be handled gracefully during server-side rendering |

---

### 4.3 Category: WebSocket End-to-End
*Addresses: B17, B18*

#### Test 3.1 — Clean WS Close Code Forwarding (from B17)
**Platform:** Playwright WebSocket capture

| Step | Action | Expected |
|---|---|---|
| 1 | Connect to `ws://localhost:8080/ws/audio` via Playwright | 101 Upgrade with `Origin: http://localhost:5173` |
| 2 | Receive the `audio.meta` hello message (text frame) | Contains `type`, `signalId`, `centerHz`, `modulation`, `subType`, `sampleRate`, `channels`, `bitrate` |
| 3 | Wait for session expiry (~3 seconds, no active session) | Close frame received with code 1000, reason "no live stream" |
| 4 | Verify `wasClean=true` on the client-side close event | UI renders "Stream ended (recorder closed it)" |
| 5 | Verify button becomes "▶ Live" (retry) | No console errors, no exception thrown |
| 6 | Click "Live" again | New session starts, VU meter tracks |

**Why:** The recorder sends a clean 1000 close, but the gateway must forward it intact — not degrade to abnormal 1006 (TCP hangup). If 1006 is forwarded, the dashboard renders "ended" as an error instead of a normal closure.

#### Test 3.2 — Gateway-to-Upstream 502 Handling (from B17)
**Scenario:** The recorder service is disconnected (Docker container stopped).

| Step | Action | Expected |
|---|---|---|
| 1 | Stop the recorder container (`docker compose stop recorder`) | Recorder unreachable |
| 2 | Connect to `ws://localhost:8080/ws/audio` | 502 JSON response: `{"error":"recorder unreachable"}` |
| 3 | Verify the browser handles the 502 without unhandled rejection | No console errors |
| 4 | Restart the recorder container | Future connections succeed |

**Why:** Unreachable upstream should answer with a 502 JSON, not a raw socket error. The frontend must handle this gracefully.

#### Test 3.3 — Multi-Client WS Fan-Out
**Platform:** Playwright with multiple browser contexts (simulating tabs)

| Step | Action | Expected |
|---|---|---|
| 1 | Open 3 browser tabs, navigate each to `http://localhost:3000` | All 3 show `● live` connection pill |
| 2 | Trigger a signal detection event (via simulation or on-air) | All 3 tabs receive the event simultaneously |
| 3 | Close one tab | Remaining 2 tabs continue receiving events |
| 4 | Verify hub client count decrements on close | `GET /ws-hub:8081/health` shows reduced client count |

**Why:** The hub must broadcast to all connected clients. One client closing should not affect others.

### 4.4 Category: Full Pipeline Smoke Test
*Addresses: B5, B18, extends existing smoke-test.sh*

#### Test 4.1 — Complete IQ-to-UI Pipeline
**Extends:** `scripts/smoke-test.sh` (existing CW + WFM smoke test)

| Step | Action | Expected |
|---|---|---|
| 1 | `docker compose up -d` (all services) | All containers healthy |
| 2 | Start native `sdr-capture` with simulator mode (`-config config/sdr-capture.sim.yaml`) | Two simulated devices streaming IQ |
| 3 | Send synthetic CW frames (via `smoke-frames -mod cw`) | `signal-processor` detects CW at 16.0000 MHz |
| 4 | Send synthetic WFM frames (via `smoke-frames -mod wfm`) | `signal-processor` detects WFM at ~100.8 MHz |
| 5 | Send below-center CW frames (`-offset -1500000`) | Detected at 13.0000 MHz (D4 §5.3 wrapped negative-bin test) |
| 6 | `GET http://localhost:8080/api/signals` | Signal entries appear within 5 seconds of detection |
| 7 | Navigate to `/signals` in browser | Detected signals render in SignalTable |
| 8 | Navigate to `/spectrum` | SpectrumView and Waterfall render signal data |
| 9 | Navigate to `/analysis` | TFR (Time-Frequency Representation) renders correctly |

**Why:** This validates the full chain from IQ samples through DSP/Classification to the UI, catching integration issues that unit tests miss.

#### Test 4.2 — Recording Lifecycle (from B18)
**Scenario:** Live signals trigger recording sessions.

| Step | Action | Expected |
|---|---|---|
| 1 | Simulate signals via `sdr-capture` simulator | `signal-processor` publishes detections |
| 2 | Recorder picks up signals, starts WAV + IQ recording | Files written to configured `RECORDINGS_DIR` |
| 3 | `GET http://localhost:8080/api/recordings` | Recording entries appear with relative `file_path` |
| 4 | `GET http://localhost:8080/api/recordings/{id}/audio` | WAV file serves correctly (Content-Type: audio/x-wav) |
| 5 | Wait for session rotation (300s IQ cap) | Stream closes cleanly ("Stream ended"), fresh click starts new session |
| 6 | Verify the recorded files resolve through the gateway (not native path) | File paths are relative, gateway serves from its volume mount |

**Why:** B18 revealed that containerized recorders starve on macOS. This test verifies the native recorder pair works correctly, with files served through the gateway.

---

### 4.5 Category: Settings & Configuration
*Addresses: A2, A3, A4, A8, A13*

#### Test 5.1 — Settings Save Actually Changes Service Behavior (from A2)
**Scenario:** Saved settings must propagate to running services, not just file I/O.

| Step | Action | Expected |
|---|---|---|
| 1 | `GET http://localhost:8080/api/settings` | Returns all editable knobs |
| 2 | Save a signal TTL (e.g., 60 seconds) via `PUT /api/settings` | 200 response |
| 3 | Wait 61 seconds, then query `/api/signals?min=-90&max=90` | Signals older than TTL are archived/removed |
| 4 | Retune an SDR via `PUT /api/sdrs/{id}` with `freqHz: 145.5` | sdr-capture's control API responds, frequency changes |
| 5 | `GET /api/sdrs/{id}/status` | Shows new frequency |

**Why:** A2 identified four knobs that are parsed but never consumed. This test validates the full save→effect chain.

#### Test 5.2 — Settings Round-Trip Persistence

| Step | Action | Expected |
|---|---|---|
| 1 | `GET /api/settings` → list all keys and values | All documented knobs present |
| 2 | `PUT /api/settings` → update multiple values | 200 response |
| 3 | `GET /api/settings` again | Updated values persist |
| 4 | Restart the api-gateway container | Settings reloaded from file, values match saved state |

#### Test 5.3 — Dead Config Cleanup Verification (from A3, A4, A8, A13)

| Step | Action | Expected |
|---|---|---|
| 1 | Parse `docker-compose.yml` — no references to `classifier` or `location-service` containers | D2 stubs fully removed |
| 2 | Parse `.env.example` — all variables are consumed by at least one service | No retired vars (`CLASSIFIER_PORT`, `LOCATION_SERVICE_PORT`, etc.) |
| 3 | Parse `docker/nginx.conf` — `/ws` proxies to `api-gateway:8080`, not `ws-hub:8081` | A3 fix verified |
| 4 | Check `config/signal-processor.yaml` — no dead `scan:` block | A8 fix verified |
| 5 | Check `frontend/src/lib/map/config.ts` — no dead `API_URL`/`WS_URL` exports | A13 fix verified |

### 4.6 Category: Map & Geospatial
*Addresses: B3*

#### Test 6.1 — Tile Bounds Validation (from B3)

| Step | Action | Expected |
|---|---|---|
| 1 | `GET http://localhost:8082/data/v3.json` | TileJSON returned |
| 2 | Parse `bounds` array: `[minLon, minLat, maxLon, maxLat]` | `maxLon > minLon` (not 0 for regions west of Greenwich) |
| 3 | Navigate to `/` (home/map view) | MapLibre renders tiles, camera is within the data region (not mid-Atlantic) |
| 4 | Zoom through levels 0–14 | No missing tiles or 404 errors |

**Why:** B3 found Arizona MBTiles with `bounds: [-114.8325, 30.05891, 0, 37.00596]` — `maxLon = 0` pushed the entire render to the mid-Atlantic Ocean.

### 4.7 Category: Signal Classification Accuracy
*Addresses: B4*

#### Test 7.1 — Confidence Threshold & Class Source Validation (from B4)

| Step | Action | Expected |
|---|---|---|
| 1 | Feed noise-only IQ frames (no tones) through the pipeline | No high-confidence classifications produced (confidence < threshold) |
| 2 | Feed CW tones at various SNR levels (−20 dB, −10 dB, 0 dB, +10 dB, +25 dB) | Classification confidence increases with SNR; below threshold SNR → no classification or low confidence |
| 3 | Feed WFM tones (modulated) | Correct "broadcast" class source, not "land_mobile" or "marine" |
| 4 | Parse log output for class source enum | Source is one of: `aviation`, `land_mobile`, `marine`, `amateur`, `broadcast`, `gnss`, `wifi`, `unknown` — never a method:label like `onnx:*/rules:*` |

---

### 4.8 Category: Regression Suite (Existing Smoke + UI-BUGCHECK Sweep)
*Addresses: B5, B10–B16, B19*

#### Test 8.1 — CW + WFM + Below-Center (Extending Existing smoke-test.sh)
**Existing** (from `scripts/smoke-test.sh`), validated in CI:

| Step | Action | Expected |
|---|---|---|
| 1 | Build signal-processor with `-tags onnx` | Binary compiles with ONNX Runtime |
| 2 | Stream CW frames (`smoke-frames -mod cw -count 5`) | CW peak detected at 16.0000 MHz in logs |
| 3 | Stream WFM frames (`smoke-frames -mod wfm -count 5`) | WFM peak detected at ~100.8 MHz in logs |
| 4 | Stream below-center CW (`-offset -1500000 -count 5`) | Detected at 13.0000 MHz (D4 §5.3 wrapped negative-bin) |
| 5 | Verify class source enum is valid for each detection | No `method:label` values in class field |

#### Test 8.2 — Aggressive UI Sweep (from UI-BUGCHECK B12–B16)
**Platform:** Playwright with aggressive interaction patterns

| Step | Action | Expected |
|---|---|---|
| 1 | Table search with regex metacharacters (`.*[`) | No crash, shows "no signals match" |
| 2 | Table search with 300-char unicode string | No crash, graceful "no match" |
| 3 | Freq column sort toggle (click header) | Sort direction toggles (96.234 → 97.294 → 94.909 MHz) |
| 4 | "Without position" chip toggle | Row set toggles, no errors |
| 5 | Canvas drag commit (sel 95.618–96.341 MHz) | Frequency span renders, correct overlay |
| 6 | Canvas drag degenerate click (barely moved) | Clears, no stuck drag state |
| 7 | Pointer leaves canvas mid-drag (`pointerleave` event) | Drag self-commits or clears, no phantom overlay |
| 8 | Route switch mid-drag (click another view during drag) | Old drag overlay removed, new view renders |
| 9 | Waterfall drag races (rapid clicks) | No stuck state, clean clear on click |
| 10 | 2560×1440 and 1280×480 viewport render | No overflow, no clipping |
| 11 | `/api/signals/{id}/track` 404 | Console error for SPEC'd "no track" response — handled gracefully, not a frontend defect |

### 4.9 Category: API Contract Validation

#### Test 9.1 — REST API Contract Completeness

| Step | Action | Expected |
|---|---|---|
| 1 | Test every SPEC-documented endpoint (all paths from §13, §7.4) | Each returns documented status codes |
| 2 | Send invalid JSON to JSON endpoints | 400 `{"error":"invalid JSON"}` |
| 3 | Send GET to POST-only endpoints | 405 `{"error":"method not allowed"}` |
| 4 | Reference nonexistent signal (`/api/signals/00000000-0000-0000-0000-000000000000`) | 404 |
| 5 | Retune unknown SDR (`PUT /api/sdrs/unknown`) | 404 |
| 6 | Retune SDR without scan loop (scan paused, no sweep) | 409 from sdr-capture |

#### Test 9.2 — CORS Configuration

| Step | Action | Expected |
|---|---|---|
| 1 | Send `GET /api/signals` with `Origin: http://localhost:5173` | 200, `Access-Control-Allow-Origin: http://localhost:5173` |
| 2 | Send `GET /api/signals` with `Origin: http://evil.example.com` | 403 (denied by `ALLOWED_ORIGINS`) |
| 3 | Send `GET /api/signals` with no `Origin` header | 200 (non-browser clients pass) |
| 4 | WebSocket handshake with foreign origin | 403 (same origin policy enforced) |
| 5 | Set `ALLOWED_ORIGINS=""` (empty) | All origins denied (not silently allow-all) |
---

## 4.10 Robustness & Resilience Tests
*Addresses: A12 (zombie prevention), D10 (error format contract), SERVICE_RESTART resilience*

### 4.10.1 Global Test Infrastructure (applies to ALL tests)

Every e2e test MUST implement the four lifecycle phases:

```go
func (suite *TestSuite) Setup() {
    // 1. SERVICE READINESS: poll /health with 500ms retries up to 30s
    waitForHTTP("http://localhost:8080/health", 30*time.Second)
    waitForHTTP("http://localhost:8081/health", 30*time.Second)
    // db health: try to connect with retries (pg_isready)
    waitForDB("postgres://sdr:sdr@localhost:5432/sdr", 60*time.Second)
}

func (suite *TestSuite) Cleanup() {
    // 2. DETERMINISTIC CLEANUP: remove all signals, recordings, WS sessions
    deleteAllSignals()
    deleteAllRecordings()
    // Close any lingering WS connections (hub client count should be 0)
    assertHubClientCount(0)
    // Kill any background processes (signal-processor, smoke-frames)
    cleanupProcessPool()
}
```

**Why:** Without this, tests leak state, leak processes, and cause cascading failures. The smoke test already does this correctly (lines 39–47 of `scripts/smoke-test.sh`) — the rest of the suite should follow the pattern.

### 4.10.2 Retry / Timeout Contract

Every test step that depends on async behavior (signal detection, event relay, DB commit) MUST include a retry loop:

```
Max retry: 100 attempts (10 seconds)
Poll interval: 100ms
Backoff: none (constant polling is fine for e2e)
```

| Scenario | Default Timeout | Reason |
|---|---|---|
| Signal detection via pipeline | 10s | FFT + ONNX inference takes 200–800ms per frame |
| WS event relay | 5s | Hub fan-out is fast unless backpressure kicks in |
| DB commit (signal insertion) | 10s | PostGIS insert + index update |
| Recording file appear on disk | 30s | Disk I/O can be slow in Docker on macOS |
| Service restart recovery | 60s | Container restart + service init |
| Zombie eviction (read deadline) | 120s | A12 spec: 120-second read deadline |

---

## 5. Priority Matrix

| Priority | Tests | Rationale |
|---|---|---|
| **P0 — Blockers** | 1.1, 1.2, 3.1, 4.1, R1 | Break normative SPEC contracts; data loss or silent failure |
| **P1 — Critical** | 1.3, 2.1, 2.2, 2.3, 4.2, R2, R3 | User-facing failures, resource leaks, broken recording |
| **P2 — Important** | 2.4, 3.2, 3.3, 5.1, 7.1, R4, R5, R6 | Regression prevention, classification accuracy |
| **P3 — Polish** | 5.2, 5.3, 6.1, 8.1, 8.2, 9.1, 9.2 | Doc/code alignment, edge cases |

---

## 6. Execution Instructions

### 6.1 Prerequisites
```bash
# Full stack with hardware (macOS):
make build-capture && ./bin/sdr-capture -config config/sdr-capture.yaml
docker compose up -d
cd frontend && npm run dev

# Full stack without hardware (simulator):
./bin/sdr-capture -config config/sdr-capture.sim.yaml
docker compose up -d
cd frontend && npm run dev

# Or use nohup to background (avoids SIGTTIN):
nohup make dev < /dev/null > /tmp/sigint-dev.log 2>&1 &
```

### 6.2 Running the Suite

```bash
# Frontend tests (unit + integration)
make frontend-test          # vitest run

# Go tests (unit + integration)
make test                   # go test ./... -v -count=1

# ONNX smoke test (existing, extended)
make smoke-onnx             # scripts/smoke-test.sh

# Type check
cd frontend && npm run check  # svelte-check

# Markdown lint
npx markdownlint-cli2 "**/*.md"
```

### 6.3 Test Structure (Proposed)

```
tests/e2e/
├── api/
│   ├── health_probe_test.go          # Test 1.1
│   ├── backpressure_test.go          # Test 1.2 (3 functions)
│   └── cors_validation_test.go       # Test 9.2
├── websocket/
│   ├── zombie_eviction_test.go       # Test 1.3
│   ├── close_code_forwarding_test.go # Test 3.1
│   └── multi_client_fanout_test.go   # Test 3.3
├── pipeline/
│   ├── full_pipeline_test.go         # Test 4.1
│   └── recording_lifecycle_test.go   # Test 4.2
├── resilience/
│   ├── service_restart_test.go       # Test R1
│   ├── error_format_test.go          # Test R2
│   ├── multi_sdr_concurrent_test.go  # Test R3
│   ├── network_instability_test.go   # Test R4
│   ├── schema_compliance_test.go     # Test R5
│   └── snr_boundary_test.go          # Test R6
├── frontend/
│   ├── canvas_dpr_test.go            # Test 2.1 (Playwright)
│   ├── keyboard_contract_test.go     # Test 2.2 (Playwright)
│   ├── deep_link_test.go             # Test 2.3 (Playwright)
│   ├── viewport_layout_test.go       # Test 2.4 (Playwright)
│   └── aggressive_sweep_test.go      # Test 8.2 (Playwright)
├── classification/
│   └── confidence_threshold_test.go  # Test 7.1
```

### 6.4 CI Integration

Add an `e2e` job to `.github/workflows/ci.yml`:

```yaml
e2e:
  name: E2E (full pipeline + UI)
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: "1.25" }
    - uses: actions/setup-node@v4
      with: { node-version: "22", cache: npm, cache-dependency-path: frontend/package-lock.json }

    - name: Build all Docker images
      run: docker compose build

    - name: Start services (simulated SDR)
      run: docker compose up -d db api-gateway ws-hub signal-processor iq-ingest

    - name: Install frontend deps
      working-directory: frontend
      run: npm ci

    - name: Run frontend tests
      working-directory: frontend
      run: npm test

    - name: Run Go unit tests
      run: go test ./... -v -count=1

    - name: Run smoke test (ONNX)
      run: make smoke-onnx

    # ─── E2E tests (require Docker Compose stack running) ───
    - name: Run E2E API health probes
      run: go test ./tests/e2e/api/... -v -count=1

    - name: Run E2E WebSocket tests
      run: go test ./tests/e2e/websocket/... -v -count=1

    - name: Run E2E pipeline tests
      run: go test ./tests/e2e/pipeline/... -v -count=1

    - name: Run E2E resilience tests
      run: go test ./tests/e2e/resilience/... -v -count=1

    - name: Run E2E frontend tests (Playwright, WebKit)
      run: |
        cd frontend
        npx playwright install webkit
        npx vitest run --config vitest.config.ts e2e/
```

---

## 7. Spec Reference Index

Tests map to SPEC sections as follows:

| Test | SPEC Reference |
|---|---|
| 1.1 — Health Probes | §3.2, §13 (API overview) | `[implemented]` |
| 1.2 — Backpressure | §14.3 (fan-out contract) | `[implemented]` |
| | 1.3 — Zombie Eviction | §14.3, A12 (keepalive contract) | `[implemented]` |
| | 1.4 — Close Frame Fwd | §10.4.4, B17, §14.3 | `[implemented]` |
| 2.1 — Canvas DPR | §18 (spectrum/waterfall) |
| 2.2 — Keyboard Contract | §15 (keyboard shortcuts) |
| 2.3 — Deep-Link URL | §3.1 (?signal=<id> mirror) |
| 2.4 — Viewport Layout | UI-DESIGN (responsive design) |
| 3.1 — Close Code Forwarding | §10.4 (live audio relay) |
| 3.2 — 502 Handling | §13 (error mapping) |
| 3.3 — Multi-Client Fan-Out | §14.3 (N-client hub) |
| 4.1 — Full Pipeline | §4–§6 (IQ → DSP → classification) |
| 4.2 — Recording Lifecycle | §10, §11 (recording + retention) |
| 5.1 — Settings Effect | §20 (setup screen) |
| 5.2 — Settings Round-Trip | §16.6 (settings schema) |
| 5.3 — Config Cleanup | D2, A3, A4, A8, A13 |
| 6.1 — Tile Bounds | §16 (map config) |
| 7.1 — Classification Accuracy | §6.5 (class source enum) |
| 8.1 — CW+WFM+Below-Center | §5.3 (below-center), D4 (classification) |
| 8.2 — Aggressive Sweep | UI-BUGCHECK B12–B16 |
| 9.1 — REST Contract | §13 (API specification) |
| 9.2 — CORS Contract | §17.2 (origin policy) |
| R1 — Service Restart Resilience | §3.2 (API overview, service lifecycle), D10 (restart SLAs) |
| R2 — Error Format Validation | §13 (error contract, D10) |
| R3 — Multi-SDR Concurrency | §7.1 (scan loop, D5 max SDRs), A1 (concurrent queueing) |
| R4 — Network Instability | §14.2 (connection management), B19 (WS lifecycle) |
| R5 — PostGIS Schema Compliance | §16 (data model), db/init.sql + 5 migrations |
| R6 — SNR Boundary Detection | §6.5 (class confidence), B4 (confidence guardrails) |

---

## 8. Open Follow-Ups

1. **On-air TDOA validation** (docs/HARDWARE.md §7, §17.4 slice 5) — requires a third receiver; unchanged by this test suite.
2. **CI confirmation of the `markdown` job** — confirm markdownlint returns green on the next push (STACK-AUDIT A23).
3. **Playwright MCP integration** — The SPEC notes that UI validation was done via Playwright MCP (WebKit). The e2e tests should use the same tooling for consistency.
4. **Test data fixtures** — Synthetic IQ frame generators exist in `cmd/smoke-frames` and `internal/classify/onnx_ort_test.go`. Reuse these for frontend e2e tests where UI-level IQ injection is needed.
5. **R3 baseline** — Before running the 100-SDR concurrency test, validate the production stack has enough file descriptors and socket buffers to handle 100 concurrent UDP streams (open file limit, `net.core.rmem_max`, `net.core.wmem_max`).
6. **R5 seed data** — Create a fixture script to insert realistic signal coordinates (Arizona bounds from B3) into the database for UI tests, avoiding the `bounds: 0` tile problem that previously pushed the map to the mid-Atlantic.

### 4.10.3 Test R1 — Service Restart Resilience (B5 extension)
*Scenario: A service crashes and restarts while clients are connected.*

| Step | Action | Expected |
|---|---|---|
| 1 | Connect WS client to `/ws` | 101 Upgrade |
| 2 | Verify client receives `signal.update` events (poll every 500ms) | Events flowing |
| 3 | `docker compose restart api-gateway` | Gateway restarts |
| 4 | Wait for gateway `/health` to return 200 (up to 60s) | Gateway healthy |
| 5 | Reconnect WS client | 101 Upgrade succeeds |
| 6 | Verify events flow through new connection | No data loss window > 5s |
| 7 | Restart all services simultaneously (`docker compose restart`) | All services come back up in < 120s |
| 8 | Wait for all services healthy, reconnect WS, check event flow | All services recover within SLA |

**Why:** The SPEC doesn't define recovery SLAs, but a production system must handle container restarts gracefully. This catches connection leaks and state corruption during restart.

### 4.10.4 Test R2 — Error Format Validation (D10)
*Scenario: All services must return error responses in a consistent JSON format.*

| Step | Action | Expected |
|---|---|---|
| 1 | Hit every error path (404, 502, 503) across all services | Each returns `{"error":"<message>"}` |
| 2 | Parse all error JSON responses — assert `"error"` key exists | No HTML errors, no 500 tracebacks |
| 3 | Verify `api-gateway` returns 503 when DB is down | `{"error":"database unavailable"}` |
| 4 | Verify `api-gateway` returns 502 when recorder is down | `{"error":"recorder unreachable"}` |
| 5 | Verify `ws-hub` returns 403 when CORS origin is denied | `{"error":"origin not allowed"}` |
| 6 | Verify `sdr-capture` returns 404 for unknown device | `{"error":"device not found"}` |
| 7 | Verify `signal-processor` returns 409 when SDR has no scan loop | `{"error":"...scan paused..."}` |
| 8 | Verify `recorder` returns 413 when span exceeds 300s cap | `{"error":"...span too large..."}` |

**Why:** B17 fixed 502 JSON but the spec doesn't mandate a uniform error contract across all services. This test ensures consistency — every service returns `{"error":"<string>"}` for errors, never HTML, never tracebacks.

### 4.10.5 Test R3 — Multi-SDR Concurrent Signal Detection (from B5)
*Scenario: 100 SDRs sweep concurrently — test signal detection when many devices scan simultaneously.*

| Step | Action | Expected |
|---|---|---|
| 1 | Start 100 simulated SDRs (simulator mode, sharing `iq-ingest` UDP port) | 100 `SDRMetadata` entries in `sdrs` table |
| 2 | Each simulator sends 5 CW frames at different frequencies (14.5 MHz ± 50 MHz span) | 500 frames sent to `iq-ingest` |
| 3 | Verify `signal-processor` detects peaks from all 100 devices | 100 unique signal entries in DB |
| 4 | Verify no signals are dropped or miscategorized | All have valid `class_source` enums |
| 5 | Repeat with 50 CW + 50 WFM + 30 noise-only frames (mixed traffic) | Correct classification for each type |
| 6 | Measure detection latency (frame → DB insertion) | P99 < 5 seconds across 100 concurrent streams |
| 7 | Send 1,000 frames per second aggregate (10 SDRs × 100 fps) | No frames dropped, `iq-ingest` consumer queues don't grow unbounded |

**Why:** The SPEC doesn't specify a maximum concurrent SDRs, but the simulator supports 100. This validates the upper bound before production deployments.

### 4.10.6 Test R4 — Connection State During Network Instability
*Scenario: Browser experiences transient network drops during signal tracking.*

| Step | Action | Expected |
|---|---|---|
| 1 | Connect WS to `/ws`, verify event relay | Events flowing |
| 2 | Simulate network drop (Throttle: "Offline" in Playwright DevTools) | WS closes (code 1006 from TCP disconnect) |
| 3 | Wait 10 seconds while offline | UI shows "connecting" state, no crash |
| 4 | Restore network | WS reconnects automatically |
| 5 | Verify events flow through reconnected WS | No state corruption |
| 6 | Verify reconnected session receives signals from after the reconnect | No gaps in the signal timeline |

**Why:** B19 found the recorder's `wasClean` flag was not correctly forwarded — but the broader question of connection resilience (what happens when the network drops mid-session) was never tested systematically.

### 4.10.7 Test R5 — PostGIS Schema Compliance
*Scenario: Signal coordinates, frequencies, and metadata stored in PostGIS must comply with schema constraints.*

| Step | Action | Expected |
|---|---|---|
| 1 | Insert signals with extreme coordinate values (lat/lon near ±180, ±90) | Accepted by schema (PostGIS `GEOGRAPHY` handles edge cases) |
| 2 | Insert signals with extreme frequencies (0 Hz, 1.7 GHz, 10 GHz) | Accepted by `float8` column |
| 3 | Insert 500 signals (max spec limit per query) | All inserted, `GET /api/signals` returns 500 |
| 4 | Insert signal without position (`path = NULL`) | Stored as `NULL` `GEOGRAPHY`, not geometry error |
| 5 | Insert signal with empty string path (`''`) | Converted to `NULL` (not crash) |
| 6 | Query signals with `ST_DWithin` spatial index | Index is used (verify `EXPLAIN` shows index scan) |
| 7 | Run `app_settings` CRUD concurrently (30 threads) | No race conditions, idempotent upsert |

**Why:** The schema (db/init.sql + 5 migrations) uses PostGIS `GEOGRAPHY` types, `float8` for frequencies, and `app_settings` for config. If INSERT/UPDATE logic doesn't match schema constraints, signals silently fail or corrupt the database.

### 4.10.8 Test R6 — SNR Boundary Detection (near confidence threshold)
*Scenario: Signals right at the classification confidence threshold (0.5–0.7) should be handled gracefully.*

| Step | Action | Expected |
|---|---|---|
| 1 | Send CW frames at −20 dB SNR (well below classification threshold) | No classification or very low confidence (confidence < 0.3) |
| 2 | Send CW frames at −10 dB SNR (lower boundary) | Confidence 0.3–0.5 (low-confidence range) |
| 3 | Send CW frames at 0 dB SNR (typical) | Normal classification (confidence 0.7–0.95) |
| 4 | Send CW frames at +25 dB SNR (clean) | High-confidence classification (confidence > 0.95) |
| 5 | Send noise-only frames (no tones, same power as −20 dB CW) | Confidence < threshold → no classification (B4 regression) |
| 6 | Verify confidence distribution is monotonic with SNR | Higher SNR → higher confidence (no inversions) |
| 7 | Verify the confidence threshold is configurable via signal-processor config | Changing `threshold` in `classifier.yaml` changes minimum display confidence |

**Why:** B4 found the old rule labeled noise as "aviation wideband FM" at 0.85 confidence. The ONNX fix prevents this, but a systematic SNR boundary sweep proves classification behaves correctly across the full SNR range — not just at extreme SNRs.

**Why:** B4 found the old rule labeled noise spikes as "aviation wideband FM" at 0.85 confidence. New guardrails prevent this.

## 4.11 Category: Air-Gap & Security Hardening (§4.11)

**Scope:** Validate the complete air-gap workflow: supply-chain provisioning,
runtime isolation, escape-hatch auditing, and operational resilience.

**Operational context:** An air-gapped installation has zero outbound network
access after deployment. All assets (ONNX model, tiles, code) must be
supplied via a **supply machine** (one networked host), transferred
physically, and the target system booted and operated entirely offline.
The single escape hatch is **tile downloads** — the only network call
an air-gapped installation may make, mediated through a supply machine.

### Test 4.11.A1 — Network Isolation Verification

**Scenario:** Running system is physically disconnected from all networks.
Verify zero outbound connectivity with a pre-flight check.

| Step | Action | Expected |
|---|---|---|
| 1 | Run nslookup localhost and ping -c1 -W1 127.0.0.1 (loopback OK) | Loopback works |
| 2 | Attempt ping -c1 -W1 8.8.8.8 and ping -c1 -W1 1.1.1.1 (external) | All fail (1–5 s timeout each) |
| 3 | Attempt curl -sf --connect-timeout 3 http://169.254.169.254/latest/meta-data/ (cloud metadata) | Fails (non-zero exit or 404) |
| 4 | Attempt ping -c1 -W1 10.0.0.1 (gateway/router) | Fails (gateway unreachable) |
| 5 | Verify each tool returns a clear failure code (not a false-positive) | Exit 1 for all 4 checks |
| 6 | Log all 4 results as the air-gap baseline | Audit entry created |

**Why:** A compromised firewall rule or stale WiFi connection could expose
an air-gap system. This pre-flight audit must be part of every boot
sequence (A3: offline boot).

### Test 4.11.A2 — ONNX Runtime Telemetry Blockade

**Scenario:** ONNX Runtime has a known telemetry feature that can leak
inference data or model metadata to external endpoints. Verify it is
fully disabled in an air-gapped context.

| Step | Action | Expected |
|---|---|---|
| 1 | Set `ORT_DISABLE_TELEMETRY=1` + `ORTE_LOG_LEVEL=0` (ORTE: ONNX Runtime Env) | No telemetry library loaded |
| 2 | Load ONNX classifier: `oc.Load()` (see §12.3) | Loads successfully |
| 3 | Run 10 inference calls on the CW test suite (conf 0.0–1.0) | All return valid classifications |
| 4 | Monitor with `ss -tnp | grep -v 127.0.0.1` (outbound TCP) | Zero outbound connections |
| 5 | Check `/tmp/onnx*` or `/var/tmp/onnx*` for telemetry artifacts | None created |
| 6 | Run `strace -e network -p <onnx-process>` (if available) | No `connect()`/`getaddrinfo()` syscalls |
| 7 | Verify `ORTE_DISABLE_SESSION_STEERING=1` also honored | No change in inference results |

**Why:** ONNX Runtime 1.14+ added a built-in telemetry provider that
attempts to POST model metadata to Microsoft endpoints. This must never
happen in an air-gapped or classified environment.
**Why:** ONNX Runtime 1.14+ added a built-in telemetry provider that
attempts to POST model metadata to Microsoft endpoints. This must never
happen in an air-gapped or classified environment.

### Test 4.11.A3 — Offline Asset Verification

**Scenario:** A supply machine provisions all assets (model + tiles + code)
before the system goes air-gapped. Verify the target system can boot and
operate with zero network calls.

| Step | Action | Expected |
|---|---|---|
| 1 | Supply machine pre-copies: model (`models/rf_ml.onnx`), tiles (`tiles/data/*.mbtiles`), configs (`config/*.yaml`) | All files present on air-gap target |
| 2 | Unplug network cable / disable network interfaces (or firewall all `OUTPUT` policy to `DROP`) | Zero outbound traffic possible |
| 3 | Start all 6 services (`sdr-capture`, `iq-ingest`, `signal-processor`, `recorder`, `api-gateway`, `ws-hub`) | All start successfully |
| 4 | Verify ONNX classifier loads model from local `models/rf_ml.onnx` | `oc.Load()` returns nil error |
| 5 | Verify tile server serves tiles from `tiles/data/*.mbtiles` | `/data/v3/0/0/0.pbf` returns valid PBF |
| 6 | Run a classification cycle: inject 5 synthetic CW frames | 5 valid classification events |
| 7 | Verify no service attempts a background network call (monitor with `ss` or `/proc/net/tcp`) | Clean |

**Why:** This is the canonical air-gap scenario: offline boot with full
functionality. If any service reaches for the network on startup, the
design is non-compliant (H3: maximum isolation).

### Test 4.11.A4 — Resource Limit Enforcement

**Scenario:** An air-gapped system may face resource-constrained hardware
(SDR lab with limited compute). Validate resource limits prevent any
single service from starving others.

| Step | Action | Expected |
|---|---|---|
| 1 | Deploy with cgroup limits: `cpu.quota=50%`, `memory=512MB` per service | cgroups active (check `/sys/fs/cgroup/`) |
| 2 | Inject a high-rate signal burst (100 events/s for 10 s) | No OOM kills; all services stay up |
| 3 | Monitor `cat /sys/fs/cgroup/.../memory.current` per service | Within allocated memory limit |
| 4 | Run `top` during burst: cumulative CPU across 6 services | ≤ 50 % host CPU (check `/proc/stat`) |
| 5 | Verify the slowest client's queue (hub.go: `sendQueueSize=256`) does not grow unbounded | Queue drain rate ≥ event rate |
| 6 | Restart services after burst | All recover cleanly, no stale DB state |

**Why:** H3 requires maximum isolation — resource contention between
services must be bounded. In a classified environment, a denial-of-service
from one service to another is an integrity concern.
**Why:** ONNX Runtime 1.14+ added a built-in telemetry provider that
attempts to POST model metadata to Microsoft endpoints. This must never
happen in an air-gapped or classified environment.

### Test 4.11.A5 — Escape-Hatch Tile Download Audit

**Scenario:** The **single** escape hatch: an operator on the air-gap
system requests a new map region. The supply machine downloads, validates,
and the operator transfers the tile set via USB/external media. Audit
every step.

| Step | Action | Expected |
|---|---|---|
| 1 | Operator requests `europe/monaco` via admin UI | API accepts request, returns `request_id: abc123` |
| 2 | Supply machine runs `supply-tiles.sh europe/monaco` (see §4.11 scripts) | `data/monaco.mbtiles` created, valid metadata |
| 3 | Operator inspects `validate-tiles.sh monaco.mbtiles` | Bounds check passes (west < east, center inside) |
| 4 | Operator copies `monaco.mbtiles` to air-gap system's `tiles/data/` | File present in correct directory |
| 5 | On air-gap system: `docker compose restart tiles` (or equivalent) | Tile server picks up new region |
| 6 | Query `/data/v3/0/0/0.pbf` for the new region | Returns valid vector tile |
| 7 | Audit log entry: timestamp, operator, region, hash | Logged in `system.audit.log` with SHA-256 of file |

**Why:** The escape hatch is a single point of compliance risk. Every
download must be logged, hash-verified, and auditable. A non-logged
tile download would be a policy violation (H3).

### Test 4.11.A6 — Offline Boot Validation

**Scenario:** After air-gapping (physical network disconnect), boot the
full stack from cold. Validate the system starts and operates normally.

| Step | Action | Expected |
|---|---|---|
| 1 | Disconnect all network interfaces (or firewall `OUTPUT` chain: `DROP`) | `iptables -L OUTPUT | grep DROP` confirms policy |
| 2 | Start all 6 services in sequence | Each starts, logs "ready" |
| 3 | `GET http://127.0.0.1:8080/health` (gateway) | 200, `{"status":"ok"}` |
| 4 | `GET http://127.0.0.1:8081/health` (hub) | 200, `{"status":"ok","clients":N}` |
| 5 | Connect frontend: `ws://127.0.0.1:8080/ws` | 101 Switching Protocols |
| 6 | Send a synthetic signal event via `POST /api/events` (hub ingest) | Frontend receives `signal.new` event |
| 7 | Check service logs for any failed outbound connections | No errors about unreachable services |

**Why:** A3 validates the pre-flight (network down before boot). A6
validates the full boot and functional test after air-gapping. A3 + A6
together prove the offline boot workflow end-to-end.

### Test 4.11.A7 — Disk Exhaustion Resilience

**Scenario:** An air-gapped system runs indefinitely without the ability
to remote-in and clear logs. Validate graceful handling of disk full.

| Step | Action | Expected |
|---|---|---|
| 1 | Fill disk: `dd if=/dev/zero of=fillfile bs=1M count=900` (or cgroup `pids.max` + write loop) | System disk ≥ 95 % full |
| 2 | Send classification request (ONNX inference) | Fails cleanly (logs "disk space low") |
| 3 | Send a POST `/api/signals/{id}/annotations` | Returns `507 Insufficient Storage` (RFC 4918) |
| 4 | Hub continues receiving WebSocket events | Queue backs up (not drops); service logs warning |
| 5 | Free disk (remove `fillfile` to < 50 %) | All services recover without restart |
| 6 | Verify data on disk is not corrupted (DB integrity check) | `PRAGMA integrity_check` → ok |

**Why:** H3 requires the system to run unattended in a classified environment.
A full disk must not cause data corruption or an unrecoverable state.
The system should degrade gracefully and alert the operator.

### Test 4.11.A8 — WebSocket Flood Resilience

**Scenario:** A classified environment may have insiders attempting to
flood the WebSocket hub to cause denial-of-service on the UI. Validate
the hub's defense-in-depth.

| Step | Action | Expected |
|---|---|---|
| 1 | Connect 500 WebSocket clients simultaneously | All get 101 (hub accepts, within limits) |
| 2 | Each client sends 100 events/sec for 30 s | 50 k events ingested in 30 s |
| 3 | Monitor `hub.ClientCount()` | ≤ configured max (e.g., 1024) |
| 4 | Monitor broadcast queue (hub.go: `broadcast` chan = 256) | No blocking; broadcasts drop when buffer full |
| 5 | Connect a "rogue" client (no Origin header, mimicking a service) | Accepted (same as non-browser; check auth if any) |
| 6 | Verify broadcast latency for real clients (< 1 s p95) | Real-time signal updates still timely |
| 7 | Stop all rogue clients | Hub recovers within 5 s (client timeout) |

**Why:** A1 found synchronous `Broadcast()` blocks on slow clients.
A8 proves the fix: the hub must handle a flood without blocking or
starving legitimate traffic (H3: availability in a classified environment).