# SIGINT Workbench — Complete UI Design

**Status:** implemented (2026-10-05). This document was the design; the
frontend now implements it — all six routes, the shell, and the P1–P4
roadmap below have shipped. See "Implementation notes" at the end of this
section for the (small) deviations made during the build. The audit in §1
describes the state *before* implementation and is kept for the record.

**Implementation notes (deviations & decisions made while building):**

- **Sweep ring on the map is a static sky halo**, not a rotating dash —
  a rotating dash would repaint the map every frame for zero extra
  information (§17 render budget).
- **`prefers-reduced-motion` throttles the spectrum/waterfall to 1 fps**
  with a "waterfall paused (reduced motion)" caption, rather than freezing
  it entirely — data stays current without streaming motion (§16).
- **`TfrPanel` and `SignalDetail` were deleted, not kept**: the Inspector
  replaces SignalDetail entirely, and AnalysisView + the extracted
  `TfrCanvas` replace TfrPanel's only consumer (inline expansion). §13's
  delete list grows accordingly.
- **`AudioPlayer` was recording-scoped while being kept** (§13 listed it
  unchanged): it had a latent bug — it fetched
  `/api/recordings/{signal.id}/audio`, passing a *signal* id where the
  gateway expects a *recording* id, so playback could never find a row.
  It now takes a `Recording` prop and enforces one-WAV-at-a-time via a
  claim/release pair in the audio store (F4 semantics for recordings).
- **`SDRStatus` gained `lat`/`lon`** — the db rows always had them
  (§12.1); `fetchSDRs` just dropped them. `applySDRStatus` preserves a
  registered position when a positionless `sdr.status` event arrives.
- **Inspector recordings load on first expand** (plus manual refresh)
  instead of on every selection — same cancellation-guard pattern.

Everything here is grounded in what ships today: the §13.1 endpoint table,
the §14 event set, the existing components under `frontend/src/lib`, and the
SPEC's binding constraints (D9/D10, §5.6, §14.3, §18, §19, §20). No UI
element in this document points at an API or event that does not exist —
that is the rule the current UI already violates once (the orphaned
`SearchPanel`, §1).

---

## 1. Current-state audit

| Capability | Backend support | UI today | Gap |
| --- | --- | --- | --- |
| Detection + classification (§5–6) | `signal.new/update/removed` on `/ws`; `GET /api/signals` (bbox, ≤500 rows, `last_seen DESC`) | `SignalList` overlay (windowed to 250), `SignalDetail` | No search/filter wired; no sorting; orphaned `SearchPanel.svelte` is imported nowhere |
| Location: placement, A1 unlocated, tracking (§9.2–9.4) | map fields on `Signal`; `track.update` WS; `GET /api/signals/{id}/track` | Map dots + accuracy halos, track polyline, speed/heading grid | Unlocated signals are invisible (count nowhere); no receiver markers; no layer control |
| TDOA multilateration (§9.6) | `signal.tdoa` WS per attempt (§14.2): fix, locus, or rejection | Inspector TDOA section; dashed locus segment on the map for the selected signal | Shipped with the A20 remediation; on-air validation still awaits a third receiver (§9.6 exit gate) |
| Sweep scan park/resume (§7.4) | `POST /api/sdrs/{id}/scan`, `GET /api/sdrs/{id}/status` | Scan toggle inside `SDRControl` (left rail) | Only reachable from one narrow card; sweep state invisible on map/spectrum |
| Manual tune / gain (§13.1) | `PUT /api/sdrs/{id}` forwards `freqHz` *and* `gainDb` to capture | Frequency input only (`retuneSdr`) | **No gain control at all** despite the endpoint accepting it |
| Spectrum + waterfall (§18) | `spectrum.frame` WS (≤ `spectrum.rate_hz`), per-SDR sources | `SpectrumView` pinned to the bottom of a 256 px left rail | ~160 px tall; the system's richest live data gets the least room |
| Live Opus audio + VU (§10.4, §10.6) | `/ws/audio` relay, `audio.level` WS, `LiveOpusPlayer` | Buried mid-way down `SignalDetail` | Most-used listening control is the hardest to reach |
| Recordings + WAV/IQ (§11, §12.3) | `GET /api/recordings` (`?limit`, `?signalId`), audio streaming | Per-signal list inside the inspector | **No library view**; the `?limit` page of the API is unreachable |
| Time-frequency analysis (§19) | `POST /api/recordings/{id}/tfr`, 4 methods, artifact note | `TfrPanel` (176 px canvas) expanded inline inside a recordings list item | An analysis instrument lives inside a list row; no full-size render |
| Annotations (§12.5) | `GET/POST /api/signals/{id}/annotations` | Notes block in inspector | Fine — keep as-is |
| Setup wizard + health probes (§20) | settings schema API, `GET /api/setup/status` fan-out | `/setup` route + first-run banner | Health probes only visible inside the wizard; dashboard shows nothing |
| WS / service health | reconnect + REST re-bootstrap in `+page.svelte` | **Nothing** — a dead `/ws` looks identical to an empty band | Yesterday's silent-hub outage (commit `d0e566f0` fix) was invisible in the UI |

**Stack facts that bind this design:** Svelte 5 (runes) + Tailwind 4 +
SvelteKit with `adapter-static` (two routes today), MapLibre via
`svelte-maplibre`. **No new runtime dependencies** (D9/D10) — MapLibre is
the only heavy import and stays the last one. The class-color palette is
currently duplicated in `MapView.svelte` and `SignalList.svelte`; this
design centralizes it (§4).

---

## 2. Design principles (binding)

These are derived from the SPEC, not invented. Every screen below obeys them.

1. **Honesty of measurement (§5.6).** Uncalibrated receivers report relative
   power: the unit label is `dB (rel.)`, never `dBm`. Unlocated signals are
   shown as unlocated (A1, §9.3) — never plotted, never given a fake
   position. Every TFR render carries its §19.2 artifact note beside it.
   Waterfall gaps are the frames that never arrived — never interpolated
   (§18.3).
2. **Fail loud (§13.1).** Status codes map to human phrases and stay
   visible: `502` → "capture unreachable", `404` (at capture) → "unknown
   device at capture", `409` → "device has no scan loop", `503` → "database
   unavailable". The local `describe()` helper in `SDRControl` already
   implements this — promote it to a shared util and reuse it everywhere.
3. **Render budget (§14.3).** The coalesced 200 ms signal ingest, the
   250-row table window, and the dirty-flag rAF canvas loop are load-bearing
   under a busy band. New views add *zero* per-event work: tables window,
   canvases redraw on change only, polling exists only where there is no
   event (health 30 s, recordings refresh 30 s).
4. **No new dependencies (D9/D10).** Routing comes from SvelteKit, which is
   already installed. Canvases stay plain `<canvas>`. Icons stay text
   glyphs.
5. **Single ingress (A3, §2.2).** The browser only ever talks to the
   gateway: REST, `/ws`, `/ws/audio`. No component ever dials ws-hub or the
   recorder directly.
6. **Console ergonomics.** Dark, mono numerals, keyboard-first, dense but
   on a 4 px grid. The operator's eye should land on frequency, class, and
   state — in that order.

---

## 3. Information architecture

### 3.1 View map (SvelteKit routes)

| Route | View | Purpose |
| --- | --- | --- |
| `/` | **Operations** | Geographic situational awareness (map home) |
| `/spectrum` | **Spectrum workbench** | Full-height §18 waterfall/spectrum + RF control |
| `/signals` | **Signals** | Full-width triage table for saturated bands |
| `/recordings` | **Recordings** | Library across all signals (§11, §12.3) |
| `/analysis` | **Analysis** | Full-size §19 time-frequency inspector |
| `/setup` | **Setup** | §20 wizard (exists; restyled, unchanged semantics) |

Selection model: the global `selectedSignal` store stays the single source
of truth and is shared by every view. It mirrors into `?signal=<id>` for
deep links; `/analysis` additionally takes `?recording=<id>` (and the
waterfall seed via the existing `spectrumSelection` store).

### 3.2 App shell (persistent on every route)

```text
┌──────────────────────────────────────────────────────────────────────────┐
│ SIGINT Workbench   Operations  Spectrum  Signals  Recordings  Analysis   │
│                                                    ── top bar ──         │
│                                    [● ws live 2s] [db● ws● rec● cap●] ⚙ │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│                        active view (per route)                           │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```text

- **View tabs** — plain links; active tab underlined `sky-400`.
- **Connection pill** — from a new `connection` store fed by the WS
  lifecycle already in `+page.svelte` (moved into the store, not
  duplicated): `● live` (green, "last event Xs ago"), `● reconnecting`
  (amber, attempt N), `○ offline` (red). This is the fix for the
  silent-hub blind spot: a dead `/ws` must never look like an empty band.
- **Service health chips** — `db / ws-hub / recorder / capture` +
  config-writability, from `fetchSetupStatus()` (`GET /api/setup/status`,
  the documented §13 fan-out exception), polled every 30 s. Green dot =
  ok, red = down with the probe's `detail` string on hover/click
  (popover). A red chip on any view means the operator learns within 30 s
  what yesterday's incident hid for hours.
- **First-run banner** — existing `first_run` logic moves into the shell so
  it shows on every view, CTA → `/setup` (§20.4/§20.5).

### 3.3 Layout grid

- ≥1280 px: three panes where applicable (left rail optional, main stage,
  320 px inspector).
- 1024–1279 px: inspector becomes an overlay drawer over the main stage.
- <1024 px: out of scope for v1 — this is a desk console, not a phone app
  (stated honestly in the doc rather than half-supported).

---

## 4. Design tokens

**Color** (Tailwind 4 utilities, no theme extension needed):

| Token | Value | Use |
| --- | --- | --- |
| bg-base | `slate-950` | canvases, map underlay |
| bg-panel | `slate-900` | panels, table header |
| bg-inset | `slate-800` | cards, inputs, chips |
| border | `slate-700` | pane separators |
| accent | `sky-500/600` | selection, primary buttons, spectrum trace |
| ok | `green-400` | active, verified, streaming, live |
| warn | `amber-400` | parked sweep, uncalibrated, artifact notes |
| danger | `red-400` | errors, service down, GNSS class |
| text | `slate-100` primary · `slate-400` secondary · `slate-500/600` muted | |

**Class palette — single source** `src/lib/ui/classColor.ts`: `aviation`
blue-500 `#3b82f6`, `land_mobile` green-500 `#22c55e`, `marine` cyan-500
`#06b6d4`, `broadcast` amber-500 `#f59e0b`, `amateur` purple-500 `#a855f7`,
`gnss` red-500 `#ef4444`, `wifi` indigo-500 `#6366f1`, `unknown` slate-500
`#64748b`. Exports both a Tailwind text class map (tables/chips) and a hex
map (MapLibre paint properties). `MapView` and `SignalList` currently
duplicate these — both consume the module instead.

**Typography.** Frequencies, power, coordinates, durations, IDs:
`ui-monospace`. Section labels: `text-xs uppercase tracking-wide
text-slate-500` (pattern already used across components). The only
oversized element on any screen is the inspector's frequency readout
(`text-lg font-mono` — existing).

**Units.** One formatting helper module `src/lib/ui/format.ts`:
`freqHz()` (kHz/MHz/GHz with 3-decimal MHz), `bandwidthHz()`,
`powerDb()` → `{ value, unit }` where unit is `dBm` only when
`powerCalibrated`, else `dB (rel.)` (§5.6 — currently re-implemented
inline in three components).

---

## 5. Operations view (route `/`)

Purpose: geographic situational awareness. Default landing; what the
dashboard is today, minus the crowding.

```text
┌ top bar ────────────────────────────────────────────────────────────────┐
├──────────────────────────────────────────────────────┬───────────────────┤
│                                                      │ INSPECTOR         │
│                MAP  (MapView, full stage)            │ (when a signal    │
│                                                      │  is selected)     │
│   ● signals + accuracy halos · ⦿ receivers · tracks  │ §6 of this doc    │
│                                                      │                   │
│  [layers ▾]              [○ 3 signals unlocated]     │                   │
├──────────────────────────────────────────────────────┴───────────────────┤
│ SIGNALS  [search………] (all)(aviation)(land_mobile)(marine)(broadcast)…    │
│  FREQ ▲   CLASS      MOD      POWER      VERIFIED  AGE   SDR   ▦ 128 act │
│  145.500 MHz  amateur  FM(NFM)  −62.1 dB (rel.)            2s   rtlsdr-0 │
│  …  (window of 250, §14.3)            +412 more not rendered             │
└──────────────────────────────────────────────────────────────────────────┘
```text

**Map (existing `MapView`, extended):**

- Signal dots + accuracy halos + class colors — existing layers, palette
  now from `classColor.ts`. Labels render for the selected signal only
  (declutter: labels for 500 signals is noise).
- Track polyline + speed/heading chip for the selection — exists
  (`tracks` store, `signal-track` source).
- **NEW receiver layer**: one marker per `$sdrs` device. Solid dot +
  green ring = `active`; hollow = idle. A **dashed rotating ring** marks a
  device currently sweeping (from `fetchSDRStatus`, §7.4 — same data
  `SDRControl` already fetches; cached in the `sdrs` store so the map,
  rail, and spectrum view share one fetch). Marker click → select device
  (opens the receiver card popover: freq, gain, sweep toggle).
- **NEW layer toggles** (bottom-left `[layers ▾]` popover): signals /
  receivers / tracks / labels. State in the `ui` store; defaults all on.

**Unlocated strip (A1, §9.3):** a single chip above the table, right side:
`○ 3 signals without position`. Click filters the table to unlocated rows.
They are never plotted — the chip is the honest representation.

**Signal table (bottom, ~15 rem):** absorbs `SignalList` + `SearchPanel`
(the orphan finally ships). Columns: FREQ (mono, sortable), CLASS (chip),
MOD, POWER + honest unit, VERIFIED ✓, AGE (`lastSeen` relative, sortable),
SDR. Row click selects (drives map + inspector). The count header shows
the exact active count even when the render window caps at 250
(`+N more not rendered` footer retained, §14.3). Sort defaults
`lastSeen DESC` — matching the REST order — so the window is
self-consistent with the store.

**States:** empty map + `waiting for first events…` copy while WS is
connecting; `no signals detected` only after connection is `live`;
saturation note when `signals.length ≥ 450` ("near the 500-row API cap —
narrow with search or map extent" — the bbox filter exists in
`fetchSignals(bbox)` and becomes the "filter to map extent" toggle).

---

## 6. Inspector (shared right rail)

One component, used by Operations and Signals (overlay drawer on narrow
screens). Reorganizes today's `SignalDetail` into collapsible sections,
reordered by frequency of use:

```text
┌ INSPECTOR ────────────────────────────────┐
│ 145.500 MHz                    ✓ verified │  ← identity (open)
│ amateur · FM (NFM) · 12.5 kHz · conf 0.82 │
│ −62.1 dB (rel.)   rtlsdr-0 · seen 2s ago  │
├───────────────────────────────────────────┤
│ ▾ LIVE AUDIO                              │  ← promoted: most-used
│   [▶ listen]  ▁▂▅▇▅▂▁  streaming · 24k    │
├───────────────────────────────────────────┤
│ ▸ LOCATION                                │  ← open when populated
│   37.7749, −122.4194 ±120 m               │
│   ▸ track: 34 km/h · 214° · Moving        │
├───────────────────────────────────────────┤
│ ▸ TDOA     fix · 41 ns · 3 pairs · 8 km   │  ← latest attempt (§9.6)
├───────────────────────────────────────────┤
│ ▸ RECORDINGS (2)   09:14:03 · 8.2 s · IQ  │
│                    [play] [analyze ↗]     │
├───────────────────────────────────────────┤
│ ▸ ACTIONS   copy id · center map ·        │
│             spectrum span ↗               │
├───────────────────────────────────────────┤
│ ▾ NOTES (1)                    [add note] │
└───────────────────────────────────────────┘
```text

- **Identity** — existing fields, one card: frequency (large mono), class
  chip, modulation + subType, bandwidth, confidence (thin bar, §6.1
  value), verified badge, power with the honest unit (§5.6), source SDR,
  first/last seen (relative + absolute on hover).
- **Live audio** — `LiveAudioPlayer` + `VUMeter`, moved from the middle of
  the panel to second position. Existing states kept: connecting /
  streaming / ended / opus-unavailable note (the recorder's opus-tag
  absence is already surfaced honestly by the component; keep that copy).
  The `audio.level`-fed `signalLevels` meter (§10.6, 1.5 s prune) stays.
- **Location** — lat/lon + accuracy radius when placed (§9.2); when
  `lat/lon` are null the section reads "No position — signal not
  placable from current receivers (A1)" instead of hiding. Track block
  (speed/heading/moving) as today, fed by `fetchTrack` + `track.update`.
- **TDOA (§9.6)** — the latest `signal.tdoa` attempt for the signal
  (last event wins, §14.3; `tdoa` store, cleared on `signal.removed`).
  An accepted fix shows the fix coordinates plus the quality surface
  (residual ns, pairs used, max baseline, covariance flag) and, when
  persisted, the reference receiver and the §9.6 flip-flop note. An
  ungated solve shows the locus note — its dashed violet segment
  renders on the map (rides the Tracks layer toggle). A rejection
  shows the engine's reason verbatim. No attempts reads "No
  multilateration attempts — the engine needs tdoa.enabled and ≥2
  receivers with positions".
- **Recordings** — per-signal list (existing `fetchRecordings(signalId)`),
  WAV → inline `AudioPlayer` playback via `GET /api/recordings/{id}/audio`;
  IQ → **`analyze ↗` routes to `/analysis?recording=<id>`** (replacing the
  inline `TfrPanel` expansion — an analysis instrument stops living inside
  a list row). See §10.
- **Actions** — copy id; center map on signal; *spectrum span ↗* (enabled
  only when the signal lies inside the selected SDR's current frame span:
  `freqHz ± sampleRate/2` from the `spectrum` store — routes to
  `/spectrum` with the frequency marked).
- **Notes** — annotations exactly as today (§12.5): list newest-first,
  add form with retry-on-failure (text preserved).

---

## 7. Spectrum workbench (route `/spectrum`)

Purpose: promote §18 — the system's richest live feed — from a 160 px
sidebar widget to a first-class view, and co-locate the RF controls that
shape it.

```text
┌ top bar ─────────────────────────────────────────────────────────────────┐
├───────────────┬──────────────────────────────────────────────────────────┤
│ SOURCES       │  spectrum line (dB grid, center marker, signal ticks)    │
│ ● rtlsdr-0 2s │  [autoscale]  span 145.000 MHz ±0.5 · dB (rel.)          │
│ ○ rtlsdr-1 ▨  │  ┌────────────────────────────────────────────────────┐  │
│               │  │                                                    │  │
│ RECEIVERS     │  │            WATERFALL  (300 rows, §18.3)            │  │
│ rtlsdr-0      │  │   ▓▓▒░░▒▓▓▓░░  (drag ⇒ sky selection overlay)      │  │
│  145.5 MHz    │  │   ░░▒▓█████▓▒░░                                    │  │
│  24.4 dB  ●   │  └────────────────────────────────────────────────────┘  │
│  [■ parked]→  │  freq axis ticks (canvas)                                │
│  [resume ▶]   │  selection: 145.312–145.438 MHz · 12.0–28.4 s            │
│ rtlsdr-1 …    │            [analyze ↗]  [clear]                          │
└───────────────┴──────────────────────────────────────────────────────────┘
```text

- **Sources** — the existing per-SDR picker (`spectrumSdrIds` /
  `selectedSdrId` stores), kept and extended with staleness: a source is
  `live` when its latest frame is <3 s old, `stale` (amber) otherwise,
  `no frames` (muted) when the hub has delivered nothing. Population
  rule unchanged: **sources appear only when `spectrum.frame` events
  arrive** — never from the SDR list alone (that rule saved us
  yesterday; keep it visible in the UI copy).
- **Canvas block** — `SpectrumView`'s internals move here unchanged in
  behavior: line + quarter-dB grid, center-frequency marker, waterfall
  (300-row ring, heat palette, dB-rel axis), autoscale toggle, tick
  axis. The rAF dirty-flag loop is untouched. Autoscale defaults on
  (quantized to 10 dB steps): the §5.2 uncalibrated floor rides near
  0 dBFS on a live feed, and a fixed −100…0 span painted the waterfall
  solid red.
- **Signal overlay (new, passive)** — active signals from `$signals`
  whose `freqHz ± bandwidthHz/2` intersects the frame span render as
  small ticks + freq labels on the spectrum line. Labels pack into
  staggered lanes to avoid collisions; ones that fit nowhere are
  counted into a `+N` marker instead of overlapping. Derived once per
  coalesced flush (no per-event work, §14.3).
- **Receiver controls (left rail)** — per device: freq/gain readout,
  frequency input (`retuneSdr`), **fine-tune nudges (±1 kHz)** flanking
  a `fine` label — one click = one `retuneSdr` PUT at
  `freqHz ± 1000`, the honest finest step of the RTL2832U tuner PLL,
  for hand-locking a signal; **gain input (new)** — the endpoint has
  always accepted `gainDb` (§13.1 `PUT /api/sdrs/{id}`); add
  `setGain(id, db)` to `client.ts` alongside `retuneSdr`. Sweep state +
  park/resume button (`setScan`), with the §7.4 semantics stated in
  situ: "manual tune parks the sweep; resume continues from the current
  frequency". Errors use the shared fail-loud phrases (P2).
- **Selection bar** — the drag gesture already produces
  `spectrumSelection` (`dragToSelection`); the bar renders
  `[f0–f1 MHz · t0–t1 s]` with `analyze ↗` (routes to `/analysis` with
  the seed intact) and `clear`. Hint copy when no selection: "drag
  across the waterfall to pick a span for time-frequency analysis"
  (existing text, kept).
- **Honest empty state** — when the selected source has no frames:
  "no frames for rtlsdr-1 — the receiver may be parked, or sdr-capture
  is down", with the health chips as evidence. Never a blank canvas.

---

## 8. Signals view (route `/signals`)

Full-width triage table for saturated bands — the same data as the
Operations table, but with room to breathe.

- All columns of §5 plus: confidence, bandwidth, first/last seen
  (absolute), signal id (truncated mono, click to copy).
- Sticky header; the 250-row render window and `+N more not rendered`
  footer carry over unchanged (§14.3).
- Shared search + class-chip filter row (one component used by both
  tables); density toggle (comfortable / compact rows).
- "Copy as CSV" — client-side over the filtered, sorted store slice.
  No new endpoint.
- Row selection syncs `selectedSignal`; the inspector opens as an
  overlay drawer (§3.3).

---

## 9. Recordings (route `/recordings`)

Purpose: the cross-signal library that `GET /api/recordings` has always
supported but no screen shows.

```text
┌ RECORDINGS ──────────────────────────────────────────────────────────────┐
│ filter: [all formats ▾] [signal: 145.500 MHz amateur ▾]  showing 50 ⏳30s │
├──────────────────────────────────────────────────────────────────────────┤
│ STARTED         SIGNAL                      DUR    FMT  RATE    ACTIONS   │
│ 09:14:03        145.500 MHz · amateur       8.2s   IQ   48 kS/s [analyze ↗]│
│ 09:14:03        145.500 MHz · amateur       8.2s   WAV  48 kS/s [▶][⭳]     │
│ 08:51:22        162.400 MHz · marine        12.0s  WAV  48 kS/s [▶][⭳]     │
└──────────────────────────────────────────────────────────────────────────┘
```text

- Data: `fetchRecordings` extended to accept `{ signalId?, limit? }` —
  the API already takes `?limit` (default 50, cap 500) and `?signalId`
  (§13.1); the client wrapper just never exposed `limit`.
- Columns: started (time, absolute date on hover), signal (freq + class
  chip, click-through selects the signal), duration, format badge
  (WAV/IQ), sample rate.
- Actions: **▶ Play** streams `GET /api/recordings/{id}/audio` into the
  `AudioPlayer` (WAV only — honest about formats: IQ rows get download +
  analyze, no fake play button); **⭳ Download** (same endpoint);
  **analyze ↗** for IQ → `/analysis?recording=<id>`.
- Filter by signal via `?signal=` deep link; "all formats / wav / iq"
  client-side.
- Pagination is honest: "showing latest 50" + a "load more" that raises
  `limit` toward the 500 cap — the endpoint's actual contract, no
  invented infinite scroll.
- **No record button.** Recordings are automatic (§11.1 in-band trigger);
  the empty state says so: "recordings are created automatically while a
  demodulated signal is active — none yet."
- Refresh: no WS event exists for new recordings — poll every 30 s while
  the view is visible, and expose an explicit refresh button (P3
  "poll only where there is no event").

---

## 10. Analysis (route `/analysis`)

Purpose: the §19 time-frequency instrument at full size — where the
inline `TfrPanel` canvas graduates to a real view.

```text
┌ ANALYSIS ────────────────────────────────────────────────────────────────┐
│ recording: 09:14:03 · 145.500 MHz · IQ · 48 kS/s · 8.2 s        [param ▾]│
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│        TFR canvas — full width, ~55 vh, axes + dB (rel.) colorbar        │
│        ▓▓▒░▒▓██▓▒░▒▓▓  (stft | reassigned | spwvd | cwt-morlet)          │
│                                                                          │
│  ⚠ artifact note (§19.2) rendered beside every image, per method         │
├──────────────────────────────────────────────────────────────────────────┤
│ PARAMETERS  [stft ▾] window [hamming ▾] nfft [1024] overlap [50]%        │
│  t0 [0.0] t1 [8.2] s  freq crop [full]         [render]   status: ready  │
└──────────────────────────────────────────────────────────────────────────┘
```text

- **Parameter form** — identical controls to today's `TfrPanel`:
  method (with each method's artifact description from the
  `TFR_METHODS` meta shown *under the method selector*, not hidden),
  window (`TFR_WINDOWS`), `nfft` (clamped to `TFR_MIN_NFFT`…
  `TFR_MAX_NFFT`), overlap, time window, freq crop.
- **Guards, unchanged semantics:** client-side span check against
  `TFR_MAX_SPAN_S` with a warning before submit; the server's
  rejection (`413` span, `404` recording/tfr-disabled) surfaces as a
  human phrase in the status line.
- **Waterfall seed** — arriving with `?recording=<id>` prefills the
  form via `spanForRecording` / `freqSpanForRecording` (existing
  logic); arriving from the spectrum workbench prefills from
  `spectrumSelection` (existing logic).
- **Render** — `requestTFR` POST; while computing, the status line
  shows the in-flight state (this is a server-side compute, §19.1 —
  the UI must never fake progress); on success the canvas draws the
  image with axes, the dB (rel.) colorbar legend, and the artifact
  note. The `analysis_id` + method used are stamped under the image.
- **Metadata header** — the inspected recording's signal, format,
  rate, duration; `↗ signal` links back to the inspector selection.

---

## 11. Setup (route `/setup`)

The §20 wizard exists and its semantics are correct (per-section save,
validation errors inline via `SettingsValidationError.field`, restart-
to-apply notice naming `SaveResult.restart` services — D11). Design
changes are structural only:

- Step navigation becomes a persistent left column (today's numbered
  stepper), each step showing its save state (clean / dirty / error).
- `SystemCheck` (`GET /api/setup/status`) promotes from "a panel inside
  step 1" to a persistent footer visible on every step — the wizard is
  where a broken db/capture gets diagnosed, so the probes should never
  scroll away.
- Completion (`completeSetup(false)`) returns to `/` with the first-run
  banner cleared; re-arming (`completeSetup(true)`) is the existing
  "re-run wizard" affordance, restyled as a quiet link in the shell ⚙
  menu.

---

## 12. Interaction flows

**F1 — select a signal (all views coherent).** Row click / map dot /
`?signal=` deep link → `selectedSignal.set(sig)` → inspector opens, map
centers softly (only on explicit "center map" — selection alone must not
yank the viewport), track fetch fires (`fetchTrack`), recordings +
annotations fetch with the existing cancellation guard.

**F2 — tune a receiver.** Frequency/gain edit → `retuneSdr`/`setGain`
(`PUT /api/sdrs/{id}`) → success: card + map marker update from the
returned `SDRStatus`; `sdr.status` WS events keep the rest coherent.
**Fine tune:** `−1 kHz` / `+1 kHz` buttons under the inputs nudge the
receiver one kilohertz per click (a full §7.4 retune each — and
therefore park a sweeping device, like any manual tune), so a signal
can be hand-locked inside its passband without typing; nudges reuse
the card's busy/error display and clamp to the input's 24 MHz–6 GHz
range. Manual tune on a sweeping device parks the sweep (§7.4): the
UI shows `■ parked` (amber) everywhere the device appears (rail, map
marker ring, spectrum source row), with the resume button at each
location. Failures render the fail-loud phrase (`502` capture
unreachable, `404` unknown at capture) inline in the card, and never
clear the input.

**F3 — waterfall → analysis.** Drag on the waterfall → `dragToSelection`
→ selection overlay + bar → `analyze ↗` → `/analysis?recording=…` (if a
matching recording exists) or `/analysis` with the span prefilled from
`spectrumSelection`. Back-link on the analysis header returns to
`/spectrum` with the selection intact (store survives navigation).

**F4 — live audio lifecycle.** Inspector `listen` → `LiveAudioPlayer`
dials the `/ws/audio` relay (A3) for that signal only; `audio.level`
events feed the VU meter (`applyAudioLevel`, 1.5 s `pruneSignalLevels`
window); pause/stop closes the socket. Selecting another signal tears
down the old stream first — one live stream at a time (existing
behavior, made explicit in the UI: the previously playing row returns to
idle).

**F5 — connection drop.** `onclose` → connection pill goes amber
`reconnecting (N)`; the existing exponential backoff loop retries;
`onopen` re-runs the REST bootstrap (`fetchSignals`, `fetchSDRs`) —
already implemented in `+page.svelte`, now surfaced in the pill and
logged as a one-line banner ("connection restored — state reloaded").
No toast storm: one banner, self-dismissing.

**F6 — first run.** `fetchSetupState().first_run` → banner on every view
→ `/setup` → probes green → sections saved → complete → banner clears,
land on Operations.

**F7 — new recording appears.** No WS event exists, so the library
polls at 30 s while visible; a subtle "refreshed 12s ago" caption keeps
the staleness honest rather than pretending real-time.

---

## 13. Component & store inventory

**New components** (`frontend/src/lib/…`):

| Path | Responsibility |
| --- | --- |
| `shell/AppBar.svelte` | Top bar: brand, view tabs, connection pill, health chips, ⚙ menu |
| `shell/ConnectionPill.svelte` | WS state display (from `connection` store) |
| `shell/HealthChips.svelte` | db/ws-hub/recorder/capture dots + popover (from `health` store) |
| `shell/FirstRunBanner.svelte` | Global first-run CTA (existing logic, moved) |
| `ui/ClassChip.svelte` | Class label in its color (from `classColor.ts`) |
| `ui/StatusPill.svelte` | ok/warn/danger pill with text (never color-only) |
| `signals/SignalTable.svelte` | Absorbs `SignalList` + `SearchPanel`; sortable, filtered, windowed |
| `inspector/Inspector.svelte` | The §6 rail (refactor target of `SignalDetail.svelte`) |
| `spectrum/SpectrumWorkbench.svelte` | §7 view; wraps existing canvases + receiver rail |
| `recordings/RecordingsLibrary.svelte` | §9 library view |
| `analysis/AnalysisView.svelte` | §10 view; reuses the TFR renderer extracted from `TfrPanel` |

**New stores** (`frontend/src/lib/stores/…`): `connection.ts` (ws state,
attempt count, lastEventAt — the logic moves here from `+page.svelte`),
`health.ts` (30 s `fetchSetupStatus` poll + derived chip states),
`ui.ts` (active view, layer toggles, inspector open, table density).

**Modified:** `+layout.svelte` (gains the shell; today it is empty),
`+page.svelte` (shrinks to the Operations composition; WS loop moves to
`connection` store), `MapView.svelte` (receiver layer, layer toggles,
palette from `classColor.ts`), `SpectrumView.svelte` (internals kept,
hosted by the workbench), `SignalDetail.svelte` (becomes `Inspector`),
`SDRControl.svelte` (adds gain input; keeps fail-loud phrases, promoted
to a shared `describeStatus()` util), `TfrPanel.svelte` (renderer
extracted so `AnalysisView` can reuse it), `client.ts`
(`fetchRecordings({signalId?, limit?})` extension, new `setGain`).

**Unchanged:** stores `signals` / `sdrs` / `audio` / `tracks` /
`spectrum` / `tfr` logic (including coalesced ingest, ring buffer, idempotent
frame application — they are the system's load-bearing pieces);
`AudioPlayer`, `LiveAudioPlayer`, `VUMeter`, `SystemCheck`,
`FieldInput`, `SdrListEditor`, `setup/+page.svelte` semantics.

**Delete after migration (all deleted):** `SignalList.svelte` and
`SearchPanel.svelte` (absorbed by `SignalTable`); `SignalDetail.svelte`
(replaced by `Inspector`); `TfrPanel.svelte` (replaced by `AnalysisView` +
the extracted `TfrCanvas`). `sdrCount` remains (store tests reference it;
the shell derives counts from `$sdrs` directly).

---

## 14. States & error matrix

| Situation | Surface | Copy / behavior | Data source |
| --- | --- | --- | --- |
| WS offline | Connection pill (red) | `○ offline — retrying`; view still renders last state | `connection` store |
| WS reconnecting | Pill (amber) | `● reconnecting (3)` | attempt counter |
| WS restored | One-line banner | "connection restored — state reloaded" | `onopen` bootstrap |
| db down (503 everywhere) | Health chip + view banner | "database unavailable — live events only, nothing persists" | `fetchSetupStatus`, REST catch |
| capture down | Receiver card, health chip | "capture unreachable (502)" — P2 phrases | `PUT`/`POST` responses |
| device unknown at capture | Receiver card | "known to db but not to capture — restart capture or re-probe" | `404` from control API |
| scan toggle on non-scan device | Receiver card | "device has no scan loop (409)" | `setScan` response |
| No SDRs configured | Operations/spectrum rails | "no receivers — add one in setup" → `/setup` | `fetchSDRs` empty |
| SDR present, no frames | Spectrum source row | "parked or capture down — no frames yet" | frame absence ≠ error |
| Empty signals (connected) | Table/map | "no signals detected" — only when pill is `live` | `connection` + `signals` |
| Saturated band | Table footer | "showing 250 of 662 — narrow by search or map extent" | store length |
| Unlocated signal selected | Inspector location | "No position — not placable from current receivers (A1)" | `lat/lon === null` |
| Uncalibrated receiver | All power labels | unit reads `dB (rel.)` everywhere, incl. spectrum axis | `powerCalibrated` (§5.6) |
| Recorder opus missing | Live audio block | existing honest note from `LiveAudioPlayer` | `/ws/audio` hello |
| Recording WAV fetch fails | Row action | inline "playback failed (recorder 502)" | audio endpoint |
| TFR disabled (404) | Analysis status line | "tfr service not configured" + setup link | `requestTFR` |
| TFR span too long (413) | Analysis status line | existing clamp warning + server phrase | `TFR_MAX_SPAN_S` guard |
| Setup save fails (400) | Field inline | server error text + offending field highlight | `SettingsValidationError` |

---

## 15. Keyboard shortcuts

Implemented via the `ui` store + `svelte:window` in the shell; every
shortcut is listed in a `?` overlay (and `aria-keyshortcuts` where
applicable).

| Key | Action |
| --- | --- |
| `1`…`6` | Switch view (Operations…Setup) |
| `/` | Focus the signal search input |
| `j` / `k`, `↑`/`↓` | Next / previous signal in the current sort order |
| `Enter` | Open inspector for the highlighted row |
| `Esc` | Clear selection / close overlay / close popover |
| `Space` | Play / pause the live audio stream (inspector open) |
| `s` | Park / resume sweep on the focused receiver |
| `?` | Shortcut overlay |

---

## 16. Accessibility

- Focus-visible rings on every interactive element (the codebase already
  uses `focus:ring-1` in places — make it uniform).
- Status is never color-only: pills carry text (`parked`, `stale`,
  `offline`), class chips carry the class name.
- Icon-only controls (▶, ⭳, ▾) get `aria-label`s — the pattern already
  exists (`aria-label="Frequency (MHz) for {sdr.id}"` in `SDRControl`).
- Canvases get `role="img"` + `aria-label` summarizing state ("waterfall
  for rtlsdr-0, 145 MHz ±0.5, live"), plus an sr-only line with the
  latest peak frequency/level.
- Table hit targets ≥ 32 px row height in comfortable density; the
  compact density keeps ≥ 28 px.
- `prefers-reduced-motion`: pause the waterfall scroll (draw the latest
  frame statically with a "paused" caption); disable map fly-to
  animation.

---

## 17. Performance alignment (§14.3, §18.1)

Nothing in this design adds per-event work; every loop is bounded:

| Concern | Budget / mechanism |
| --- | --- |
| Signal ingest | unchanged — coalesced 200 ms batch flush (`upsertSignals`), one render per flush |
| Table rendering | 250-row window + exact-count footer; sort/filter are store-slice ops |
| Canvases | rAF with dirty flag (existing); waterfall capped at 300 rows; frames ≤ `spectrum.rate_hz` |
| Map | source data rebuilt on flush boundaries only; no per-event layer writes |
| REST | ≤500 rows per call (API cap); polling only where no event exists: health 30 s, recordings 30 s |
| TFR | server-side compute (§19.1); UI shows in-flight state, never simulates |
| WS | three sockets max: `/ws`, one `/ws/audio` at a time; A3 relay only |

---

## 18. Implementation roadmap

Each phase ships independently; none changes an API contract. **All four
phases shipped on 2026-10-05** (single implementation pass).

- **[x] P1 — Shell & truth:** routes + `+layout` shell, `AppBar`,
  `connection`/`health` stores, connection pill + health chips,
  fail-loud util extraction, `SearchPanel` filters wired into the
  shared table.
- **[x] P2 — Spectrum workbench:** `/spectrum` route,
  `SpectrumWorkbench` hosting the existing canvases, receiver rail with
  gain + sweep, selection bar → `/analysis` deep link, receiver map
  layer + layer toggles.
- **[x] P3 — Data surfaces:** `SignalTable` (absorbing
  `SignalList`/`SearchPanel`), `Inspector` reorganization,
  `RecordingsLibrary` + `client.ts` `fetchRecordings`/`setGain`
  additions, `/recordings` route.
- **[x] P4 — Analysis & polish:** `AnalysisView` with extracted TFR
  renderer, keyboard shortcuts, a11y pass (§16), empty-state copy pass
  (§14), reduced-motion, superseded components deleted.

---

## 19. Appendix A — element → data source cross-reference

| UI element | Store / function | SPEC |
| --- | --- | --- |
| Signal table rows | `signals` store; `fetchSignals(bbox)` bootstrap | §12.2, §13.1 |
| Class chip colors | `classColor.ts` (new, dedupes 2 copies) | §6.1 classes; palette itself is a frontend convention |
| Unlocated chip | `signals` where `lat === null` | A1, §9.3 |
| Map dots / halos / track | `MapView` sources; `tracks` + `fetchTrack` | §9.2–9.4 |
| Receiver markers / rings | `sdrs` store; `fetchSDRStatus`; `setScan` | §7.4, §13.1 |
| Tune / gain | `retuneSdr`; `setGain` (new); `PUT /api/sdrs/{id}` | §7.4, §13.1 |
| Spectrum line/waterfall | `spectrum` store (`applySpectrumFrame`, `WATERFALL_ROWS`) | §18 |
| Source picker + staleness | `spectrumSdrIds`, `selectedSdrId` | §18.3 |
| Waterfall drag selection | `dragToSelection` → `spectrumSelection` | §18.3, §19.4 |
| Live audio + VU | `LiveAudioPlayer`; `signalLevels` (`audio` store) | §10.4, §10.6 |
| Recording rows / playback | `fetchRecordings`; `GET /api/recordings/{id}/audio` | §11, §12.3, §13.1 |
| TFR form guards | `TFR_METHODS`, `TFR_WINDOWS`, `TFR_MIN/MAX_NFFT`, `TFR_MAX_SPAN_S` | §19.1–19.3 |
| TFR render | `requestTFR` → `TFRResult`; artifact note | §19.1–19.2 |
| Notes | `fetchAnnotations`, `addAnnotation` | §12.5 |
| Health chips | `fetchSetupStatus` → `SetupStatus.components` | §13, §20 |
| First-run banner | `fetchSetupState` / `completeSetup` | §20.4–20.5 |
| Settings wizard | `fetchSettings`, `saveSettings`, `SettingsValidationError` | §20.1–20.3 |
| Connection pill | `connectWebSocket` lifecycle (moved to `connection` store) | §14, A3 |
| Inspector TDOA section · map locus segment | `tdoa` store ← `signal.tdoa` WS (§14.2) | §9.6 |

## 20. Appendix B — deliberate non-features

- **No record button.** Recordings are triggered in-band by the
  detector (§11.1); a UI record switch would promise a control path that
  does not exist.
- **No manual position entry.** Placement is a single-receiver
  computation (§9.2); letting an operator type coordinates would corrupt
  an automatic product.
- **No config editing outside `/setup`.** §20.1 scopes runtime
  configuration to the settings API; no view writes config files.
- **No direct ws-hub / recorder sockets.** Everything via the gateway
  relays (A3) — the frontend never learns internal topology.
- **No waterfall interpolation.** Gaps are gaps (§18.3); a "smooth"
  waterfall would lie about what arrived.
- **No phone layout.** Out of scope by §1; the console targets a desk
  display ≥1024 px (§3.3).

---
