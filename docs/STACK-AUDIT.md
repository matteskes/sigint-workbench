# SIGINT Workbench — Full-Stack Audit

**Date:** 2026-10-05. **Baseline:** commit `b0c6449e` (UI P1–P4), tree
clean. **Scope:** every layer from SDR hardware to the browser, plus
the deploy/CI/docs surface. **Method:** full reads of all six `cmd/`
mains, `internal/` packages, `db/init.sql` + all five migrations, all
`config/*.yaml`, `docker-compose*.yml`, the Makefile, CI, `.env.example`,
nginx, and all four docs; contract-tracing each §-referenced data path
end-to-end; re-running every verification gate live (results in §6).

Nothing here changes behavior — this document is the input for the
next work cycle. Evidence cites `file:line` at the audit baseline.

## 1. Inventory (what ships today)

| Layer | Artifact | State |
| --- | --- | --- |
| Hardware | `internal/sdr` rtlsdr + hackrf + simulator, `cmd/rtl-*` | RTL validated on-air; HackRF compile-only |
| Capture | `cmd/sdr-capture` (675 ln), control API `:9090` | healthy |
| Wire | §4.1 v1 + §4.5 v2 frames, `seq.go` gap counters | v2 live (`stream_format: sdr2`) |
| Ingest | `cmd/iq-ingest` (208 ln) | healthy, minimal |
| DSP | `internal/dsp` assembler/FFT/peaks/bands/spectrum | matches §5 |
| Classify | rules + ONNX (`-tags onnx`), train.py parity | matches §6 |
| Location | §8 verifier, §9.4 tracks, §9.5–9.6 TDOA engine | engine live, on-air pending |
| Recorder | in-band sessions, WAV + raw IQ, Opus mux, TFR | matches §10–11, §19 |
| DB | PostGIS, 6 tables + `app_settings`, 5 migrations | schema == §12 |
| ws-hub | fan-out hub, origin allowlist | one real gap (A1) |
| Gateway | REST + 2 WS relays + capture proxy + §20 | matches §13 |
| Frontend | SvelteKit SPA, 6 routes, stores, vitest 109 | matches UI-DESIGN |
| Deploy | compose 9 services, 5 Dockerfiles, nginx, CI 4 jobs | drift in docs (A4/A5) |

## 2. Findings summary

Severity: **blocker** (breaks a normative contract today), **gap**
(missing/ineffective behavior), **inconsistency** (doc vs code vs
config), **polish** (hygiene, no user impact).

| ID | Sev | Area | One-line summary |
| --- | --- | --- | --- |
| A1 | blocker | ws-hub | broadcast writes block the whole hub; §14.3's per-client drop queue does not exist |
| A2 | gap | setup (§20) | four editable knobs are never read by their services |
| A3 | inconsistency | ingress (A3) | nginx `/ws/` proxies to ws-hub, bypassing the gateway relay |
| A4 | inconsistency | env/§16.6 | `.env.example` + §16.6 carry retired or unconsumed vars |
| A5 | inconsistency | ports (§3.2) | loopback publishes of db/ws-hub contradict "publish removed" |
| A6 | inconsistency | SPEC §4 | v2 frames marked "DRAFT, not yet implemented" but shipped |
| A7 | inconsistency | SPEC §18 | §18 `[implemented]` vs §17.4/§16.1 still "[planned]" |
| A8 | gap | config | `signal-processor.yaml` `scan:` block is parsed and dead |
| A9 | gap | recorder | `audio.sample_rate` unvalidated against the 48 kHz demod stack |
| A10 | polish | recorder | `SelectPurge` test-only; `PurgeFiles` duplicates it, different order |
| A11 | polish | db | two near-duplicate recordings-list queries |
| A12 | polish | ws-hub | no read deadline/ping on hub client conns (zombie peers) |
| A13 | polish | frontend | dead `map/config.ts` exports; WS fallback targets the hub |
| A14 | polish | config | decorative keys: `features.*`, `audio.format`, `iq.format` |
| A15 | polish | dsp | entropy computed but unused; O(n²) noise floor; kept-import |
| A16 | polish | build | Makefile `.PHONY` missing seven targets |
| A17 | polish | docs | §-ref typos (settings schema §8→§6; AGENTS decision range) |
| A18 | polish | docs | §10.5 names `EncodeWAV`; production uses `WAVWriter` |
| A19 | polish | deploy | `docker-compose.dev-macos.yml` override is now a no-op |
| A20 | dead-end | events | `signal.tdoa` has zero consumers; locus never rendered |
| A21 | polish | ops | dev/prod logs unbounded (no rotation, no compose log caps) |
| A22 | polish | config | `sdr-capture.yaml` scan block mixes commented + explicit zeros |
| A23 | gap | CI | `docs/UI-DESIGN.md` failed the markdownlint gate at baseline (red `markdown` job); mechanical fixes applied in this commit |

## 3. Per-layer audit

### 3.1 Hardware → capture

Drivers report `HasTX=false` and `TestHackRFH2Guard` enforces the TX
ban (internal/sdr/hackrf_test.go:69); RTL gain snapping (§15.3 defect
4) rejects unreachable requests instead of silently clamping
(internal/sdr/gain.go:26). The scan loop matches §7.1/§7.4: park on
manual tune, resume from the current frequency, per-device goroutines,
`409` for scan-less devices. `appliedGainDB` keeps §5.6 honest
against snapped gains.

- A22: `config/sdr-capture.yaml:69-76` carries commented default
  lines *and* explicit `min_hz: 0` / `max_hz: 0` — harmless (zeros
  mean "driver default") but confusing next to the comments.

### 3.2 Wire protocol (§4) → ingest

v1/v2 layouts match the SPEC byte-for-byte (internal/sdr/protocol.go
vs §4.1/§4.5); the magic-sniffing decoder accepts mixed streams;
ingest validates, counts drops, tracks per-sender seq gaps
(cmd/iq-ingest/main.go:186-197). One doc defect:

- A6: `docs/SPEC.md:189-192` still says §4.5 v2 is "a DRAFT … not
  yet implemented". Reality: v2 encode/decode shipped, every
  consumer decodes it, and `stream_format: sdr2` has been the
  shipped config since 2026-10-05 (config/sdr-capture.yaml:14).
  §4's status line and the §9.6 "inert until v2 flows" caveats
  should be reconciled to "implemented, default on".

### 3.3 DSP → classify → verification → TDOA

Peak detection is the normative two-pass (collect-then-space) form;
the −3 dB walk mirrors `models/train.py` including the `>=` compare
(internal/dsp/peak.go:99-121, models/train.py:139-165). The feature
vector order (log2 kHz, bw, peak, floor, SNR, crest, shape[128]) is
identical on both sides — 134 floats in, byte-compatible. §8's
decision table is implemented exactly, including the 0.2 time-miss
penalty and the two-stage power scoring
(internal/location/verify.go:39-90). §9.1 UUIDv5 bucketing matches
(internal/db/signalid.go:17-21). The TDOA engine (§9.5/§9.6) is
wired behind `tdoa.enabled` with persistence, flip-flop guard, and
the §14.2 event; on-air validation remains the declared exit gate.

- A15: `classify.SpectralEntropy` is computed per peak
  (internal/classify/features.go:46) but consumed by nothing —
  `ToVector` omits it and so does `train.py`. Also
  `DetectNoiseFloor` is an O(n²) selection sort per FFT record
  (peak.go:139-148; §5.7 acknowledges it) and peak.go:163 keeps
  `math.Abs` alive with `var _ =`. Cheap cleanups.

### 3.4 Recorder, audio, retention, TFR (§10, §11, §19)

Session lifecycle, empty-session discard, raw-IQ side files, the
duration cap, phase-continuous mixer, and the demod registry's
pair-first selection all match the contracts. The live Opus path is
faithful to §10.4: one text hello then binary-only, 3 s subscribe
grace, 5 s write deadlines, ping keepalive, close 1000 semantics
(internal/record/ws_server.go, streamer.go). §19.3 status codes
(400 / 404 / 404-disabled / 413 / 502 at the proxy) are exact
(internal/tfr/http.go:74-134, internal/api/server.go:433-437).

- A9: the Opus streamer is built from `audio.sample_rate`
  (cmd/recorder/main.go:112-116) while every demodulator emits a
  fixed 48 kHz (internal/audio/registry.go:78-92). Any other value
  produces a stream whose declared rate lies about its PCM —
  pitch-shifted playback. Validate at load (the §16.5 default keeps
  today's behavior correct).
- A10: `record.SelectPurge` (retention.go:21) has no production
  caller; `PurgeFiles` (recorder.go:139-193) reimplements the same
  policy inline and evicts size-cap victims in ReadDir name order,
  not oldest-first. Unify on `SelectPurge`.
- A18: §10.5 names `EncodeWAV` as the WAV writer; the session path
  actually writes via `WAVWriter` (encoder.go's `EncodeWAV` is
  test-only). Rename the contract reference or the function.
- A21: retention deletes files hourly, but nothing bounds logs: dev
  scripts append to unrotated `/tmp/sigint-workbench-*.log`, and
  compose sets no `logging.max-size`, so the json-file driver's
  platform default (often unlimited) applies.

### 3.5 Database (§12)

`db/init.sql` is column-for-column identical to §12.1–12.7, and all
five migrations are idempotent complements of it. Upsert semantics
(first_seen kept, verified OR-latched, TDOA quality columns sticky
via COALESCE) match §8/§9.6 (internal/db/queries.go:23-55). The
archive purge preserves recordings via `ON DELETE SET NULL`.

- A11: `GetRecordings` (API; default 50, cap 500) and
  `ListRecordings` (recorder; default 200) are near-duplicates with
  different defaults — collapse into one parameterized query.

### 3.6 ws-hub and api-gateway (§2.2, §13, §14, §17.2)

The gateway matches §13.1 route-for-route — no missing endpoint, no
undocumented route. Relays dial upstream before upgrading (502 JSON
on dead upstream), origin checks mirror the hub, the capture proxy
surfaces 404/409/502 per §7.4/§13.2.3, and the TFR proxy preserves
upstream statuses. The CORS/WS origin allowlist (set-but-empty
denies all) is implemented identically on both sides
(internal/ws/origin.go, internal/api/server.go:85-97).

- **A1 (the one blocker):** §14.3 promises a per-client 256-frame
  broadcast channel where "a slow client has its overflow frames
  dropped (non-blocking send)". The actual hub has **one** shared
  256-slot channel and writes synchronously: `Broadcast` is
  non-blocking into the shared channel (internal/ws/hub.go:75-81),
  but `Run` then does a blocking `WriteMessage` per client with
  **no write deadline** (hub.go:53-69), and the upgrade path sets
  no deadlines or pings (cmd/ws-hub/main.go:40-57). One stalled
  TCP peer freezes event delivery to *every* client, and once the
  shared buffer fills, broadcasts drop for everyone. The recorder's
  audio server already demonstrates the correct pattern — per-sink
  32-slot channel, shed-oldest, 5 s write deadline
  (internal/record/streamer.go:205-220). Port it.
- A12: same file — the read loop has no deadline either, so dead
  peers linger until a write fails. Subsumed by the A1 fix.
- A20: `signal.tdoa` is produced per §9.6 and accepted by the hub
  (cmd/ws-hub/main.go:32) — and consumed by nothing: the frontend
  switch ignores it (frontend/src/lib/stores/connection.ts:59-95),
  no REST endpoint exposes attempts, and UI-DESIGN has no TDOA
  surface at all. The map learns about fixes only via
  `signal.update`. Either render the locus/quality payload or
  demote the event to log-only and say so in §9.6.

### 3.7 Frontend

The SPA is contract-clean at every seam traced: bootstrap +
backoff reconnect, §5.6 dBm honesty flags in the Signal type,
§18.3 idempotent-per-`t` spectrum application, the §10.4 client
with jitter buffer and 1000-as-"ended", and the §20 client mapping
400s to field errors. `svelte-check` and 109 vitest tests pass.

- A13: `frontend/src/lib/map/config.ts:7-9` exports `API_URL` and
  `WS_URL` that nobody imports; the `WS_URL` fallback still points
  at `ws://localhost:8081` — the pre-A3 hub address. Delete the
  dead exports (keep `TILE_URL` / `mapStyle`).

### 3.8 Config, deploy, CI

Compose runs exactly the six app services plus three infra
services; §20's directory-mount rationale is followed; profiles and
health checks are sane. CI's four jobs run vet, untagged tests,
real-ORT onnx inference with skip-guards, real-libopus tagged tests
with skip-guards, driver-tag builds, svelte-check, markdownlint,
and trivy scans — all green at this baseline (§6).

- A3: `docker/nginx.conf:28-36` proxies `location /ws/` to
  `http://ws-hub:8081` — the pre-A3 topology. SPEC §2.2 (lines
  103-106) says nginx proxies `/ws` to the **gateway**, and A3's
  whole point is that browsers never reach the hub directly. Today
  it is latent (the browser uses the absolute `VITE_WS_URL` aimed
  at the gateway) and the trailing slash would not match bare
  `/ws` anyway — but the day someone flips to same-origin relative
  WS URLs, event traffic silently bypasses the gateway's origin
  enforcement. Point the location at `api-gateway:8080` or delete
  the block.
- A4: `.env.example` lists the retired `CLASSIFIER_PORT` /
  `LOCATION_SERVICE_PORT` (the D2 stubs are gone), documents
  `RECORDER_PORT=9012` (the UDP IQ port is 9011; 9012 is the WS
  port), and omits consumed vars `RECORDER_UDP_PORT`,
  `RECORDER_WS_PORT`, `CAPTURE_API_HOST`, `RECORDER_TFR_PORT`.
  SPEC §16.6 documents `SDR_CAPTURE_UDP_PORT_0/1`,
  `SIGNAL_PROCESSOR_PORT` and `WS_HUB_PORT`, none of which any
  code or compose file reads. One pass over §16.6 + `.env.example`
  against `grep -rn 'GetEnv'` fixes the cluster.
- A5: `docker-compose.yml:153,170` publish `127.0.0.1:8081` (hub)
  and `127.0.0.1:5432` (db). Both are loopback-only with written
  dev-bench justifications, but §3.2's status column still says the
  db publish was "removed" and §17.2 claims the exposed surface is
  exactly `8080/8082/3000 + UDP 9000/9001`. Update §3.2/§17.2 to
  describe the loopback exceptions (and UDP 9011's host-dev
  publish) instead of denying them.
- A19: `docker-compose.dev-macos.yml` exists only to re-publish
  ws-hub on loopback, which the base file already does
  (docker-compose.yml:153); the override is a no-op merge today.
  Fold it away or give it a distinct purpose.
- A16: `.PHONY` omits `build-capture-linux`, `deploy`, `stop`,
  `db-migrate`, `db-shell`, `tidy`, `setup`.
- A23: the repo's own CI markdown job is red at this baseline:
  `npx markdownlint-cli2 "**/*.md"` (the exact CI command) reported
  8 issues in docs/UI-DESIGN.md — six bare ``` fences (MD040), a
  doubled blank line (MD012), and a missing trailing newline
  (MD047) — introduced with the UI-DESIGN doc commits and never
  caught locally. Fixed mechanically in this commit (added `text`
  languages, collapsed the blank, terminated the file); both docs
  now lint clean. Worth noting why CI missed it: the `markdown`
  job lints every `*.md`, so it has been failing on `main` since
  e5817522 — check the Actions tab, not just local `make test`.

## 4. Cross-cutting consistency checks

### 4.1 Contract seams traced end-to-end (all clean)

- `signal.new/update/removed`: processor throttle (§5.8, 2 s per
  SDR+kHz bucket, shared by log+publish) → hub ingest allowlist →
  gateway relay → `connection.ts` flush → store upsert by `id`.
- `sdr.status`: frame observation → dedup (first frame, retune,
  reactivation, ≥1 s spacing) → DB upsert → UI `applySDRStatus`
  merge from both REST rows and events.
- IQ frame: capture encode → ingest validate/fan-out → processor
  assemble → recorder demod — every hop validates independently and
  buffers are sized for the v2 maximum.
- Retune: UI `PUT /api/sdrs/{id}` → DB row → capture control API
  (`freq_mhz` conversion) → scan park → next `sdr.status` —
  failure paths surface 404/409/502, never DB-only.
- Live audio: recorder mux hello/binary/close ↔ gateway passthrough
  ↔ `LiveAudioPlayer` state machine ↔ §19 TFR proxy statuses.

### 4.2 §20 setup screen vs service reality

- **A2:** the schema exposes four knobs their services never read:
  - `iq-ingest.yaml` `buffer_size` and `stats.interval_s` are
    parsed into `IQIngestConfig` but `cmd/iq-ingest/main.go`
    hardcodes the 256-frame receiver buffer and the 5 s stats
    ticker; the setup screen even documents "shipped 256" while the
    key is inert (internal/settings/schema.go:303-309,
    cmd/iq-ingest/main.go:131,154).
  - `classifier.yaml` `rules.enabled` and `onnx.enabled` are parsed
    but never consulted: rules always run and the ONNX path is
    keyed purely off a non-empty `model_path`
    (cmd/signal-processor/main.go:786-800). The schema's default
    for `onnx.enabled` is `false` while the shipped YAML says
    `true` — an operator toggling either key changes nothing.
  Fix direction: make the services honor the keys (buffer size →
  `IQReceiver` bufSize; stats interval → ticker; `onnx.enabled:
  false` → skip model load) or remove them from YAML + schema.

### 4.3 Dead config, parsed or not

- **A8:** `signal-processor.yaml:28-33` carries a `scan:` block and
  `SignalProcessorConfig.Scan` (internal/config/config.go:62-67)
  parses it — nothing reads `procCfg.Scan`. Scanning is a capture
  concern (§7, `sdr-capture.yaml`), so this is D2-era leftovers.
  Delete the YAML block and the struct field.
- **A14:** `classifier.yaml:13-18` has a `features:` block that is
  not even in `ClassifierConfig` (silently ignored by the YAML
  decoder). `recorder.yaml` `audio.format` / `iq.format` are
  parsed into structs but unenforced — the recorder only ever
  writes WAV/raw. Trim or enforce.

### 4.4 Doc-vs-doc contradictions inside the SPEC

- **A7:** §18's status is `[implemented] (Phase 4 slice 9,
  2026-10-04)` (docs/SPEC.md:2262-2266), but the §17.4 roadmap row
  still reads "slice 9 … design locked (D9), implementation
  planned", and §16.1's known-conflicts list still marks
  `spectrum.*` as `[planned] (§18.4)` (docs/SPEC.md:1982). Two
  stale spots to flip.
- **A17:** minor reference rot: the settings schema's classifier
  section describes classification but cites §8 (verification;
  internal/settings/schema.go:279), and AGENTS.md still bounds the
  decision register at "D1–D8, H1–H2, A1–A6" while Appendix A now
  carries D9–D11 and H3.

### 4.5 Environment and ports (see A4, A5)

`docker-compose.yml` is internally consistent and matches §16's
defaults; the drift is entirely between those files and the two
documents that describe them (§3.2, §16.6, §17.2, `.env.example`).
A single reconciliation pass closes A4 + A5.

## 5. Dead-end register

Code or config that exists but serves no purpose at this baseline.

| Item | Where | Verdict |
| --- | --- | --- |
| `signal.tdoa` consumers | backend produces, frontend ignores | A20 — render or demote |
| `record.SelectPurge` | internal/record/retention.go | A10 — test-only; make it the real path |
| `audio.EncodeWAV` | internal/audio/encoder.go | A18 — test-only; contract names it |
| `SignalProcessorConfig.Scan` | internal/config/config.go:62 | A8 — delete with the YAML block |
| classifier `features.*` | config/classifier.yaml:13-18 | A14 — not even parsed |
| `rules.enabled` / `onnx.enabled` | classifier.yaml + schema | A2 — wire or remove |
| ingest `buffer_size` / `stats.interval_s` | iq-ingest.yaml + schema | A2 — wire or remove |
| `map/config.ts` `API_URL`/`WS_URL` | frontend | A13 — dead exports, stale fallback |
| nginx `/ws/` → ws-hub block | docker/nginx.conf | A3 — dead-but-wrong; fix or delete |
| `docker-compose.dev-macos.yml` | repo root | A19 — no-op override today |
| `CLASSIFIER_PORT`, `LOCATION_SERVICE_PORT` | .env.example | A4 — retired with D2 |
| `SDR_CAPTURE_UDP_PORT_*`, `SIGNAL_PROCESSOR_PORT`, `WS_HUB_PORT` | §16.6 / .env.example | A4 — never consumed |
| `SpectralEntropy` compute | internal/classify/features.go:46 | A15 — unused on both sides |
| `signal-processor.yaml` `scan:` block | config | A8 — see above |

## 6. Test coverage map (vs §17.3) — all green at baseline

Verified live during this audit on the `b0c6449e` tree:

- `go vet ./...` — clean.
- `go test ./... -count=1` — all 17 packages pass (incl. the
  gateway, settings, TDOA, TFR, ws, record, classify suites).
- Tagged builds/vets: `-tags opus`, `-tags rtlsdr`, `-tags hackrf`,
  `-tags onnx` — all compile clean locally (CI additionally runs
  the real-ORT and real-libopus test paths with skip-guards).
- Frontend: `vitest run` — 10 files, 109/109 pass; `svelte-check`
  was green at `b0c6449e` (3 intentional `role="img"` canvas
  warnings); `npm run build` green across all six routes.

Coverage is faithful to §17.3's table: frame codec + validation,
simulator, FFT/peaks/bands/window/AGC/assembler, rules + ONNX
(untagged fallback and tagged real inference), demods + registry +
encoder, recorder sessions/retention/streamer (fake packetizer),
gateway handlers + settings round-trips, §8 verifier + pair
window, TDOA solver/gate/correlator + sim E2E, TFR methods + HTTP
statuses, hub broadcast + origin policy, plus the frontend's
stores/WS/audio/settings suites. Gaps no test covers today (and
which would have caught the findings above):

- A1: no test drives two clients where one stops reading — the
  hub's head-of-line block is untested behavior.
- A2: no test asserts that a §20-saved knob actually changes
  service behavior (the settings tests verify file rewriting, not
  effect).
- A9: no test pins `audio.sample_rate` against the demod stack.

## 7. Prioritized remediation backlog

1. **A1 (+A12)** — give ws-hub per-client bounded queues, write
   deadlines, and pings (port the recorder's sink pattern). Highest
   value: today one stalled browser tab degrades the event stream
   for every client, contradicting §14.3. Add the two-client
   head-of-line test.
2. **A2** — wire or remove the four inert §20 knobs; while in
   there, reconcile the `onnx.enabled` default with the shipped
   YAML. The setup screen must not offer no-op switches.
3. **A3** — fix or delete the nginx `/ws/` upstream (one line,
   closes the latent A3 bypass).
4. **A4 + A5** — one docs/env reconciliation pass: §16.6, §3.2,
   §17.2, `.env.example` vs actually-consumed variables and actual
   published ports.
5. **A6 + A7** — flip the stale SPEC status lines (§4 v2, §17.4
   slice 9, §16.1 spectrum) to `[implemented]` wording.
6. **A23** — confirm the `markdown` CI job returns green on the
   next push and check whether earlier `main` runs have been
   silently red since e5817522 (they should have been).
7. **A8 + A14 (+A13, A17, A22)** — dead config/code sweep
   (processor scan block, classifier features block, dead frontend
   exports, §-ref typos, YAML comment cleanup).
8. **A9** — validate `audio.sample_rate` (or derive the stream rate
   from the demod) + pin with a test.
9. **A10 + A11 + A18** — small deduplications (retention path,
   recordings query, WAV writer naming).
10. **A20** — decide signal.tdoa's fate: a TDOA panel consuming
    locus/quality (UI-DESIGN has none today) or demote to log-only.
11. **A15, A16, A19, A21** — hygiene batch (unused compute,
    `.PHONY`, redundant compose override, log bounds).

Everything else observed — wire format, DSP parity with the ONNX
training path, verification math, recorder/TFR contracts, DB
schema and upsert semantics, gateway routing and error mapping,
frontend seams — matches its normative section exactly and needs
no action.
