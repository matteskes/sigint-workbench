# SIGINT Workbench — System Specification

**Status:** target specification. **Version:** 0.1
**Scope:** normative contracts for the SIGINT Workbench pipeline.
**Audience:** implementers. Keywords **MUST** / **SHOULD** / **MAY** are
normative per RFC 2119.

## Status legend

Every section carries a status tag:

- **`[implemented]`** — code in this repository satisfies the contract.
- **`[planned]`** — locked design decision; not yet implemented.
- **`[gap]`** — current behavior is wrong, dead code, or missing; the
  contract below is what it must become.

Unmarked subsections are descriptive context (no requirement).

A **Design references** index in Appendix A maps the locked decisions
(D1–D11, H1–H3, A1–A6) to the sections that carry their normative text.

## 1. Purpose & Scope

**Status: `[implemented]` core pipeline; `[planned]` items as tagged.**

SIGINT Workbench is a single-host, multi-service SDR monitoring pipeline.
It captures raw IQ samples, detects signals in the FFT spectrum,
classifies modulation by frequency rules and (optionally) ONNX inference,
attaches location from per-SDR coordinates, streams events to a web
dashboard, and records decoded audio.

### In scope (v1)

- Receive-only monitoring on one host (or one Docker network).
- 1–N SDR receivers; reference deployments use one or two.
- 24 MHz – 1.7 GHz (RTL-SDR class); 1 MHz – 7250 MHz with the HackRF
  driver (see §15).
- Signal detection, band identification, modulation classification,
  2-SDR cross-verification, per-SDR location, retention, web UI.
- The synthetic simulator driver as a hardware-independent testbed.

### Out of scope (v1)

- Any transmit path. **TX is prohibited by policy (H2); drivers MUST
  report `HasTX = false`.**
- Multi-host deployments and PTP/NTP-based time synchronization
  (deferred; see §15.4, Phase 4).
- Precise geolocation beyond single-receiver placement plus 2-SDR
  verification (no TDOA/AOA engine in v1; TDOA multilateration is
  deferred to Phase 4, §17.4).
- Decoding of digital payload modes (digital modes report only their
  modulation class; payloads are not demodulated in v1).

## 2. Architecture

**Status: all six services `[implemented]` — D2 complete (the legacy
`classifier`/`location-service` stubs and compose entries are
removed) — plus both single-ingress relays (A3, §2.2: `GET /ws`,
`GET /ws/audio`) and the control-path retune proxy (§7.4, §13.1).**

### 2.1 Topology (D2)

The system consists of **six application services**:

1. `sdr-capture` — driver read loop, frame encoding, UDP transmit,
   per-device control API, scan loop.
2. `iq-ingest` — UDP receive, frame validation, fan-out to consumers.
3. `signal-processor` — DSP, classification, location, verification,
   persistence, event publishing. **Absorbed the former `classifier`
   and `location-service` binaries (D2) — both stubs are removed.**
4. `recorder` — audio demodulation, recording, live audio streaming.
5. `api-gateway` — the **single client ingress (A3)**: REST API plus
   WebSocket relays for both event and audio streams.
6. `ws-hub` — internal event broadcast fan-out (not client-facing).

Infrastructure (not application services): `db` (PostGIS), `tiles`
(tileserver-gl), `frontend` (nginx-served SvelteKit SPA).

The tree ships exactly the six service binaries above (D2 complete;
the former `classifier`/`location-service` stubs and their compose
entries are removed), plus bench tools (`rtl-list`, `rtl-calibrate`)
and the `smoke-frames` ONNX fixture.

### 2.2 Single client ingress (A3)

`api-gateway` on `:8080` is the **only** HTTP/WS endpoint that clients
(browsers, scripts, the frontend) connect to. It MUST proxy:

- `GET /ws` → `ws-hub` `:8081` `/ws` (signal events), and
- `GET /ws/audio` → the recorder's internal audio WS.

`ws-hub` and the recorder MUST be reachable only from within the Docker
network. The former in-process `/ws` hub inside `api-gateway` (no
producers, dead code) was removed in Phase 1 (§13.2.4). Both relays
are `[implemented]` (A3, Phase 2): `GET /ws` → `ws-hub:8081/ws` and
`GET /ws/audio?signal=<id>` → `ws://recorder:9012/ws/audio` (§10.4).
Each dials the upstream before the client handshake, so an
unreachable upstream answers **502 JSON**
(`{"error":"<upstream> unreachable"}`), and the same origin
allowlist as ws-hub applies (foreign origins get **403**); frames
pass through untouched.

The frontend's nginx proxies `/api/` and `/ws` (the latter's prefix
match covers both `/ws` and `/ws/audio`) to `api-gateway:8080`, so
all API and WebSocket traffic enters through the gateway; static
assets are served directly `[implemented]`.

### 2.3 Data & control flows

```texthardware/simulator
      |  driver API
      v
sdr-capture ──UDP SDR1 frames──> iq-ingest ──fan-out──> signal-processor
  :9090 ctrl (internal)           :9000/9001        :9010
                                          |
                                          +──────────> recorder :9011
                                                         |
signal-processor --POST /api/events--> ws-hub :8081
                                             |
browser <──:8080 REST + /ws + /ws/audio── api-gateway <── recorder /ws
db (PostGIS :5432, internal) <── signal-processor, api-gateway
```

- **Capture → ingest:** one UDP datagram stream per SDR
  (§4 wire protocol).
- **Ingest → consumers:** byte-for-byte fan-out of validated frames to
  a configured consumer list. Consumers are independent; a slow or
  dead consumer MUST NOT block others.
- **Processor → hub:** HTTP POST of event JSON (queue + timeout;
  hub loss never back-pressures the DSP loop).
- **Control path:** `api-gateway` → `sdr-capture` control API
  `:9090` (tune/gain/status/scan) — `[implemented]` (Phase 2, §13.1):
  `PUT /api/sdrs/{id}` forwards `freqHz`/`gainDb` so they retune
  hardware, `GET /api/sdrs/{id}/status` proxies live device
  state, and `POST /api/sdrs/{id}/scan` parks/resumes the sweep
  (§7.4); capture unreachable ⇒ `502`, never silently DB-only
  (§13.2.3).

## 3. Deployment & Ports

**Status: `[implemented]` for the current compose files. The only
interfaces published on all host interfaces are `8080` (gateway),
`8082` (tiles), `3000` (frontend) and UDP `9000`/`9011`; `ws-hub:8081`
and `db:5432` stay published on `127.0.0.1` only — loopback dev-bench
exceptions documented below, not the "publish removed" state earlier
revisions claimed (A5, §17.2).**

### 3.1 Deployment modes

- **Dev (macOS host):** `sdr-capture` runs as a native binary
  (`make build-capture`) so the SDR is reachable; all other services run
  via `docker compose` (profile `dev`). The SvelteKit dev server runs
  with `npm run dev` on `:5173`.
- **Prod (Linux):** everything, including `sdr-capture`, runs in Docker
  (`--profile prod`); the SDR device is passed through into the
  `sdr-capture` container.

### 3.2 Port map (target)

| Port | Transport | Service | Exposure | Status |
| ------ | ----------- | --------- | ---------- | -------- |
| 8080 | TCP | api-gateway | **published — only client ingress** | `[implemented]` |
| 8082 | TCP | tiles | published | `[implemented]` |
| 3000 | TCP | frontend (nginx) | published | `[implemented]` |
| 9000 | UDP | capture → ingest (one port serves all SDRs, §16.4) | published (host-side dev) | `[implemented]` |
| 9010 | UDP | processor IQ input | internal only | `[implemented]` |
| 9011 | UDP | recorder IQ input | published (UDP; `CONSUMERS` fan-out; used when the recorder joins a host dev bench) | `[implemented]` |
| 9012 | TCP | recorder live audio WS (`/ws/audio`) | internal only | `[implemented]` (§10.4) — via the gateway `/ws/audio` relay |
| 9013 | TCP | recorder TFR API (`/api/recordings/{id}/tfr`) | internal only | `[implemented]` (§19) — via the gateway TFR proxy |
| 8081 | TCP | ws-hub (events) | loopback only (`127.0.0.1:8081`, native macOS publisher bench) | `[implemented]` — browsers via the gateway `/ws` relay |
| 9090 | TCP | capture control API | internal only | `[implemented]` — loopback bind in dev, unpublished in compose; gateway proxies tune/gain/status (§13.1) |
| 5432 | TCP | db (PostGIS) | loopback only (`127.0.0.1:5432`, native macOS bench DB) | `[implemented]` — container-internal otherwise (`make db-migrate` / `exec psql` for host access) |

The surface exposed on all host interfaces is exactly: `8080`, `8082`,
`3000` (TCP) and UDP `9000`/`9011`. Two loopback-only publishes exist
for the native macOS bench (§3.1): `127.0.0.1:8081` (ws-hub, the
native signal-processor's event feed) and `127.0.0.1:5432` (db, its
host-side database) — both unreachable off-host (§17.2).

### 3.3 Environment

The Postgres container itself is configured with
`POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`; Go services
connect with `DB_URL` (inside the compose network the host is `db`).
Frontend build/runtime reads `VITE_API_URL`,
`VITE_TILE_URL`, `VITE_WS_URL` (the target `VITE_WS_URL` is the gateway,
`http(s)://<host>:8080`, not the hub).

## 4. IQ Wire Protocol

**Status: `[implemented]` — normative byte layout below is the exact
current behavior (`internal/sdr/protocol.go`). §4.5's frame v2 layout
is `[implemented]` too (A6): v2 encode/decode shipped, every consumer
decodes it, and `stream_format: sdr2` is the TDOA-enabled wire format
(§9.6).**

### 4.1 Framing

Each UDP datagram carries exactly **one IQ frame**: a 40-byte fixed
header followed by the interleaved sample payload. All fields are
**little-endian**.

```textOffset  Size  Field
------  ----  ----------------------------------------------
0       4     magic        uint32, value 0x31524453 (wire bytes 53 44 52 31 = ASCII "SDR1")
4       16    sdr_id       [16]byte ASCII, NUL-padded
20      8     freq_hz      uint64, absolute center frequency
28      4     sample_rate  uint32, samples per second
32      4    sample_cnt   uint32, number of I/Q *pairs*
36      4     timestamp    uint32, seconds since Unix epoch
40    4*n     samples      int16[n*2], interleaved I0 Q0 I1 Q1 ...
```

### 4.2 Constraints (normative)

- `n = sample_cnt` MUST be `1 <= n <= 1024`
  (`MaxIQSamplesPerFrame`). Maximum frame size is
  `40 + 1024*4 = 4136` bytes.
- `sdr_id` is a stable per-device identifier (`S1`, `S2`, ...); it
  NUL-pads the 16-byte field and MUST match the SDR row in the
  database.
- `freq_hz` is the **absolute** center frequency the receiver was
  tuned to when the samples were taken — not an offset. Consumers
  derive absolute peak frequencies as `freq_hz + offset_hz`
  (§5.3).
- `timestamp` is coarse (1-second resolution). It identifies the
  capture instant; it MUST NOT be used for sub-second timing or
  TDOA in v1.
- Samples are int16 LE, I then Q per pair, roughly full-scale when the
  AGC/driver gain is applied.
- The whole datagram — header and samples — is little-endian. There is
  no big-endian component anywhere in the frame.

### 4.3 Validation & loss semantics

`iq-ingest` MUST drop, and count, any datagram that:

- has a bad magic,
- is shorter than the header (40 bytes),
- declares `sample_cnt` inconsistent with the datagram length
  (`40 + sample_cnt*4 != len`),
- declares `sample_cnt == 0` or `> 1024`.

UDP is best-effort. Consumers MUST tolerate missing, out-of-order, and
duplicate frames: each frame is self-describing and independent. No
sequence number exists in v1; `timestamp` is the only ordering hint.

### 4.4 Producer behavior (`sdr-capture`)

- Each SDR has one dedicated read-loop goroutine that accumulates up to
  1024 pairs, encodes one frame, and transmits it to
  `stream_host:stream_port` (default `localhost:9000 + i`).
- On a driver read error the loop backs off **200 ms** and retries; it
  MUST NOT crash the process.
- Every successful `ReadIQ` sets the device active; the control API
  (§7.4) reports per-device status.

### 4.5 Frame v2 — sample-accurate timing (`SDR2`) — **[implemented]**

**Implemented (slice 5, §17.4):** the codec is dual-format
(`internal/sdr` — `IQMagicV2` encode/decode with per-datagram magic
sniffing, so one mixed v1/v2 stream decodes frame by frame);
`sdr-capture` emits v2 when `stream_format: sdr2` (default `sdr1`,
§9.6 rollout), stamping `seq`/`sample_index`/CLOCK_REALTIME anchor in
the driver read loop; every consumer (iq-ingest, recorder,
signal-processor) accepts both formats, keeps per-sender gap
counters, and sizes its UDP buffers to the 4160-byte v2 bound
(`MaxIQDatagramSize`). Normative wire layout below is unchanged from
the approved draft.

v1 (§4.1) cannot carry TDOA: its 1-second `timestamp` is far coarser
than the nanosecond-scale delays TDOA measures, and there is no
sequence number to detect gaps or quantify reordering. Frame v2 is a
**parallel format**, not an in-place mutation: a new magic value, a
fixed 64-byte header, and the same int16 interleaved payload.

**Versioning strategy.** The magic distinguishes versions on the
wire: v1 = `0x31524453` ("SDR1"), v2 = `0x32524453` ("SDR2").
Validation rules (§4.3) apply to both with the per-version header
size and length bound (v2 maximum datagram: `64 + 1024*4 = 4160`
bytes). During rollout a mixed stack is safe by construction — a v1
ingest counts v2 datagrams as bad-magic drops (visible in the drop
counters), and the v2 ingest still decodes v1. The encoder emits v2
only after the deploy of this slice; there is no negotiation. The
implementation boundary (encoder flip, dual-format acceptance,
rollout) is §9.6 (slice 5).

Cost scaling is linear per receiver: ingest bandwidth (~8 MB/s per
SDR at 2 MSPS int16) and per-frame processing in the
signal-processor. The frame format itself is per-sender and carries
no pairwise state, so receiver count is unbounded by the protocol.
Multi-host topologies (remote capture hosts → central `iq-ingest`)
and the clock-sync-quality surface are §16 (slice 6);
unicast-per-capture-host is the supported topology,
multicast/compression is future work.

**Header layout (64 bytes, little-endian, as in §4.1):**

```text
Offset  Size  Field
------  ----  ----------------------------------------------
0       4     magic          uint32, 0x32524453 ("SDR2")
4       16    sdr_id         [16]byte ASCII, NUL-padded (§4.2)
20      8     freq_hz        uint64, absolute center frequency (§4.2)
28      4     sample_rate    uint32, samples per second
32      4     sample_cnt     uint32, number of I/Q pairs, 1..1024
36      8     seq            uint64, per-sender, starts at 0, +1 per frame
44      4     t_sec          uint32, Unix seconds at the FIRST sample
48      4     t_nsec         uint32, nanosecond part of that instant
52      8     sample_index   uint64, samples produced for this SDR
                             since stream start (first sample of frame)
60      4     reserved       uint32, MUST be 0 (senders) / ignored (receivers)
64    4*n     samples        int16[n*2], interleaved I Q (identical to §4.1)
```

**Timing semantics (normative for senders):**

- `t_sec`/`t_nsec` MUST be stamped by the capture host clock
  (CLOCK_REALTIME read in the driver read loop) at the **first
  sample** of the frame. Together the pair is the frame's
  sample-accurate UTC anchor.
- `sample_index` MUST count samples without wrapping (uint64) in
  stream order. A receiver reconstructs the exact capture instant of
  any sample as `t_ns + (sample_idx − frame_sample_idx) ·
  1e9/sample_rate`, so short-term clock drift is invisible inside a
  frame run and only the anchor timestamps carry host-clock error.
- Receivers MUST treat `sample_index` as authoritative for
  contiguity (dropped frames show up as gaps in both `seq` and
  `sample_index`) and `t_sec`/`t_nsec` as authoritative for
  cross-host alignment. Sequence gaps MUST be counted; reordered
  datagrams MUST be detected via `seq` (UDP may reorder; the sample
  ranges decide overlap handling, `seq` only reports it).
- Cross-host TDOA additionally requires synchronized host clocks.
  The sync-quality surface (NTP/PTP offset, dispersion) is §16
  (Phase 4 slice 6); TDOA results MUST carry the sync quality they
  were computed under (§9.5).

## 5. DSP Pipeline

**Status: `[implemented]`.**

### 5.1 Per-frame processing (`signal-processor`)

For every validated IQ frame:

1. **Gate:** frames with fewer than **64 IQ pairs** are skipped
   (FFT on shorter records is not meaningful here).
2. **Scale:** each int16 sample is converted to float64 by dividing
   by 32768 (range ≈ −1 … +1).
3. **FFT:** complex FFT (`gonum fourier.NewCmplxFFT`) of length
   `nfft = 2^ceil(log2(n))` with zero padding. Bin spacing is
   `df = sample_rate / nfft`.
4. **Peak detection** (§5.4) on the power spectrum.
5. **Per-peak post-processing:** absolute frequency, band ID
   (§5.5), classification (§6), event assembly (§14).

### 5.2 Spectrum representation

- Real-input `ComputeFFT` is **one-sided** with `nfft/2` bins:
  `Frequencies[i] = i * df`, `PowerDB[i] = 10*log10(|X[i]|²)`,
  floored at **−300 dB** for zero-magnitude bins.
- The IQ pipeline (§5.1 step 3; §5.3) uses `ComputeIQFFT`, which
  returns the **full `nfft`-bin wrapped spectrum** with a signed
  `peak_to_freq()` mapping. `PositiveHalf()` projects that wrapped
  spectrum onto the one-sided `0 … +fs/2` view used by one-sided
  consumers and the train.py-compatible feature vector.
- `Magnitudes[i]` is the squared magnitude (`|X[i]|²`), not the
  linear amplitude.

### 5.3 Negative-frequency coverage (D4) — `[implemented]`

**Implemented:** `ComputeIQFFT` returns the full `nfft`-bin wrapped
spectrum, so bins `nfft/2 … nfft−1` — which represent **negative
offsets** `−fs/2 … 0` relative to the tuning center — are no longer
dropped. A signal arriving **below** the center frequency is detected,
and its absolute frequency (`center + offset`) is correct.

**Contract:** the spectrum MUST cover the full range
`[−fs/2, +fs/2)`. Bins `i >= nfft/2` are wrapped:

```textoffset(i) = i*df                      if 0 <= i < nfft/2
offset(i) = (i - nfft)*df             if nfft/2 <= i < nfft
peak_hz  = frame.freq_hz + offset(i)  (may be below center)
```

Peak detection and noise-floor estimation run on the wrapped full
spectrum; feature extraction runs on `PositiveHalf()`, which stays
byte-compatible with `models/train.py`. `peak_hz < 0` clamps to 0 (and
is reported). Below-center coverage is pinned at two levels: a
`cmd/signal-processor` unit test (`center = 10 MHz, offset = −2 MHz →
8.0000 MHz`) and a `smoke-test.sh` E2E fixture (`smoke-frames -mod cw
-offset -1500000` → a `13.0000 MHz` `SIGNAL` line), guarding the
wrapped upper-half bin → negative offset → `int64` center+offset
arithmetic.

### 5.4 Peak detector

Normative defaults (overridable via flags, see §16):

| Parameter | Meaning | Default |
| ----------- | --------- | --------- |
| `ThresholdDB` | minimum power for a candidate | −60 |
| `MinSpacing` | min bins between two returned peaks | 10 |
| `TopN` | max peaks returned | 20 |

Algorithm (two passes, MUST keep this order):

1. **Candidate pass:** every strict local maximum
   (`p[i] > p[i±1]`) with `p[i] >= ThresholdDB` is a candidate.
   Candidates are collected **across the whole band first** so a
   strong high-offset signal can never be crowded out by weak noise
   peaks.
2. **Selection pass:** sort candidates by power, descending; emit at
   most `TopN`, enforcing `MinSpacing` bins between any two emitted
   peaks.

**Bandwidth estimate:** walk left and right from the peak bin while
neighbouring bins stay within **−3 dB** of the peak; bandwidth
`= (hi − lo) * df`. The walk semantics (`>=` compare, stop one bin
short of the edge) MUST match `estimate_bandwidth_hz` in
`models/train.py` byte-for-byte — the ONNX model was trained on those
features.

### 5.5 Band identification

`IdentifyBand(hz)` returns the first row of the normative table whose
`FreqMin <= hz < FreqMax`:

| Band | Range (Hz) | Category |
| ------ | ------------ | ---------- |
| LF | 30 k – 300 k | low_frequency |
| MF | 300 k – 3 M | medium_frequency |
| HF | 3 M – 30 M | high_frequency |
| VHF-Low | 30 M – 50 M | vhf |
| VHF-Mid | 50 M – 174 M | vhf |
| UHF | 174 M – 1 G | uhf |
| L-Band | 1 G – 2 G | microwave |
| S-Band | 2 G – 4 G | microwave |
| C-Band | 4 G – 8 G | microwave |

Out-of-table frequencies report band `Unknown`.

### 5.6 Power values — calibration — `[implemented]`

**Two power domains.** Every detected signal carries both:

- `power_db` — **uncalibrated relative dBFS** (10·log10 of the
  squared FFT magnitude, §5.2). Peak thresholds (§5.4), peak sorting,
  bandwidth estimates, and verification SNR (§8) all stay in this
  domain; the DSP pipeline never converts.
- `power_dbm` — the calibrated absolute estimate, valid only when the
  detecting SDR is calibrated:

  ```text
  power_dbm = power_db − applied_gain_db + calibration_offset_db
  ```

  `applied_gain_db` is the SDR's gain **at the time of the frame**,
  tracked per SDR by signal-processor: seeded from `default_gain`,
  refreshed every 5 s from the capture control status endpoint
  (`api_port` → `GET /api/v1/status`, §7.4) so a runtime
  `POST /api/v1/gain` change stays honest. `calibration_offset_db`
  is the per-device offset from the sdr-capture config (§16.2),
  measured against a known reference (a signal generator or calibrated
  receiver — measuring it is bench work, runbook and tooling in
  docs/HARDWARE.md, not part of this contract).

**Honesty flag.** A signal is calibrated **iff** its SDR config
contains `calibration_offset_db` — presence of the key, not its value
(`0.0` is a valid offset; the simulator fixture uses it because the
simulator is calibrated by construction). The boolean
`power_calibrated` (§12.2) travels with every REST/WS payload
(`powerCalibrated`, §12.7); the UI renders "dBm" only when it is true
and "dB (rel.)" otherwise, and the signal-processor log line labels
power the same way. Consumers MUST NOT treat `power_dbm` on an
uncalibrated signal as an absolute level.

**SNR is calibration-invariant:** the noise floor passes through the
same per-SDR transform as the peaks, so ratios do not move when
calibration is switched on.

### 5.7 Noise floor & frame-level extras

- `DetectNoiseFloor`: median of the **lower 50 %** of bins
  (sorted median, O(n log n) — mirrors train.py's `np.sort`).
- AGC (used for live audio level, §10.6): target peak **0.95**,
  attack **1 sample**, release **50 samples**, hard-clip at ±1.0.
- **FFT geometry (wired):** `fft.size` (default 4096 pairs) is the
  assembled FFT record length: consecutive ≤ 1024-pair frames from one
  SDR are concatenated until `size` pairs are ready (a retune starts a
  new record — a device has one tuner, so its partial is discarded;
  UDP loss inside a record is a spectral discontinuity, not an error —
  §4.3 has no sequence number). `fft.window` is applied before the
  FFT; the shipped value is `rectangular`, matching the unwindowed
  ONNX training (models/train.py). Any other window requires
  retraining the model with the same window AND a §6.2 recalibration.
- **Geometry coupling:** the FFT is unnormalized, so absolute power
  scales with the record length (+3 dB noise floor, +6 dB tone bin per
  doubling). The §5.4 −60 dB threshold and the §5.6 calibration
  offsets are valid only at the calibrated geometry: changing `fft.*`
  forces a rtl-calibrate re-run (docs/HARDWARE.md §6.2 mirrors the
  geometry via `-fft-size`).
- Noise-floor estimates feed the §8 verification SNR. Under §5.6
  calibration the floor transforms identically to the peaks
  (same gain + offset), so SNR is calibration-invariant.

### 5.8 Event throttling

Per `(sdr_id, freq_bucket)` where `freq_bucket = peak_hz / 1000`
(kHz), at most **one** processed signal event is emitted per
**2-second** window. Logging and publishing share the throttle. This
bounds the event rate to ≈ 20 events/s regardless of peak count.

## 6. Signal Classification

**Status: rules `[implemented]`; ONNX `[implemented]` (optional build
tag); the `class`-field contract in §6.5 is `[implemented]`.**

### 6.1 Result shape

Every classification produces (Go: `classify.Result`; JSON:
camelCase):

```textmodulation   string   e.g. "FM", "AM", "SSB", "CW", "CDMA", "OFDM",
                      "Noise", "Unknown"
subType      string   e.g. "WFM", "NFM", "USB" ("" if none)
bandName     string   from §5.5 ("Unknown" if out of table)
source       string   see §6.4
confidence   float    0.0 .. 1.0
method       string   "rules" | "onnx"
bandwidthHz  float    −3 dB estimate (§5.4)
frequencyHz  uint64   absolute peak frequency
```

### 6.2 Rule table (normative, evaluated in order)

The switch is **first-match-wins**; later overlapping ranges lose to
earlier ones (e.g. 156–174 MHz classifies as `marine`, never
`land_mobile`).

| # | Condition | modulation | subType | source | conf |
| --- | ----------- | ------------ | --------- | -------- | ------ |
| 1 | 118–137 MHz | FM | WFM | aviation | 0.85 |
| 2 | 156–174 MHz | FM | WFM | marine | 0.85 |
| 3 | 146–174 MHz or 420–512 MHz | FM | WFM if BW > 15 kHz else NFM | land_mobile | 0.70 |
| 4 | 3–30 MHz | SSB | USB if BW < 3 kHz, else AM (source `unknown`) | amateur if BW < 3 kHz | 0.50 |
| 5 | 530 kHz–1.7 MHz | AM | — | broadcast | 0.90 |
| 6 | 88–108 MHz | FM | WFM | broadcast | 0.95 |
| 7 | 1575–1576 MHz | CDMA | — | gnss | 0.95 |
| 8 | 2.4–2.483 GHz | OFDM | — | wifi | 0.80 |
| 9 | anything else | Unknown | — | unknown | 0.30 |

### 6.3 ONNX classifier (optional, build tag `onnx`)

- **Build:** `go build -tags onnx` with cgo. Without the tag the
  processor runs rules only (compile-time fallback; no runtime
  behavior change).
- **Runtime:** ONNX Runtime is loaded via **dlopen**
  (`ORT_LIBRARY_PATH` or default loader path); it is never a link
  dependency. If the library or model file is absent, `Load` returns
  an error and the processor falls back to rules **per peak** — it
  MUST NOT crash.
- **Model I/O contract** (must match `models/train.py` export):
  - input tensor `features`, shape `(1, 134)`, float32
  - output tensor `classification`, shape `(1, 5)`, float32
    (probabilities; argmax = class, value = confidence)
- **Classes (index order):** `am`, `cw`, `fm_narrow`, `fm_wide`,
  `noise`.
- **Label → display mapping:** `am→AM`, `cw→CW`,
  `fm_narrow→FM/NFM`, `fm_wide→FM/WFM`, `noise→Noise`.

### 6.4 The 134-dim feature vector (normative)

`ToVector()` layout — MUST stay in sync with `extract_features` in
`models/train.py`:

| Dim | Value |
| ----- | ------- |
| 0 | `log2(FreqHz / 1000)`, with `FreqHz/1000` clamped to ≥ 1 (kHz domain) |
| 1 | bandwidth, Hz (−3 dB estimate) |
| 2 | peak power, dB |
| 3 | noise floor, dB (median of lower-half bins) |
| 4 | SNR = peak − noise floor, dB |
| 5 | crest factor (peak/RMS of linear power spectrum) |
| 6–133 | 128-bin spectral shape: source bins resampled by integer index (`srcIdx = i*srcLen/128`, clamped), `clamp(p − noiseFloor, 0, 60) / 60` |

Notes:

- Dim 0 is **log2 of kHz**, not raw Hz (the 500 kHz–1.7 GHz sweep
  range is unlearnable after global standardization in raw Hz).
- The 60 dB clip defines the model's dynamic range; out-of-range
  inputs saturate rather than extrapolate.
- Training SNR range is 18–30 dB (`models/train.py`,
  `SAMPLE_RATE = 4.096 MHz`, `NFFT = 4096` → df = 1 kHz/bin);
  inference frames are ≤ 1024 pairs (df ≈ 4 kHz/bin at 4.096 MHz).
  This bin-count mismatch is a known model-fidelity limitation, not
  a protocol issue.

### 6.5 `signals.class` contract — `[implemented]`

**Implemented:** the ONNX path sets `source = rules.Source` (never
`"onnx:<label>"`), so `signals.class` and every `signal.new`/
`signal.update` event carry a **source category** — the `onnx:` prefix
is never persisted or streamed, and the `SIGNAL` log line reports the
method separately.

**Contract:** `signals.class` MUST always be a **source
category** from the fixed enum:

```textaviation, marine, land_mobile, amateur, broadcast,
gnss, wifi, unknown
```

- `method` carries `rules` | `onnx` (already separate field).
- On the ONNX path, `class` is derived from the **frequency rules**
  (§6.2, source column only) while `modulation`/`subType`/
  `confidence` come from the model.
- The string prefix `onnx:` MUST NOT appear in any persisted or
  streamed field.

## 6.6 Classifier v2 — IQ-window research ladder (non-normative)

**Status: research note — no contract, no obligations, no
D-decision. Promotion to a contract follows the §20 pattern
(design lock + D-decision + §17.3 obligations) only after the
ladder below produces a winning rung.**

The §6.3 model (MLP over the §6.4 134-dim features) has two
structural limits: the features are hand-crafted spectral
summaries, and training data is synthetic-only — the
simulation-reality gap that the RF-fingerprinting literature
names as the main blocker for deployed models (Jagannath et al.
2022, §VI). This note tracks the staged research path toward a
direct-IQ classifier — Scholl (2019) reaches 94% mean accuracy
over −10…25 dB SNR (~98% above 5 dB) on 18 HF transmission modes
from 2048-sample IQ windows with ~1.4M-parameter CNNs trained
purely on synthetic Watterson-channel data — and records the
conditions under which GAN-assisted training is worth its cost.

### Ladder (adopt a rung only if it beats its predecessor)

| Rung | Training data / method | Evidence expected | Admission criterion |
| ----- | ---------------------- | ----------------- | ------------------- |
| B0 | current §6.3 MLP (baseline) | — | — |
| B1 | supervised CNN on raw IQ windows, synthetic only (`train.py`) | does direct-IQ beat 134-dim features at all? | per-class accuracy on the on-air fixture > B0 |
| B2 | B1 + real §11.3 recordings (§6.2 weak labels, §12.5 clean labels) + traditional augmentation (frequency-offset rotation, time shift, SNR/gain jitter) | quantifies our simulation-reality gap | > B1 per class |
| B3 | B2 + AC-WGAN augmentation (auxiliary-classifier head, Wasserstein objective) | scarce-class / impairment residuals closed | > B2 per class AND B2 error analysis shows scarce-class/impairment residuals |
| B4 | OOD scoring for the Unknown path | critic Wasserstein distance vs cheaper scores | critic beats temperature-scaled max-softmax / energy score on the same OOD fixtures |

Core protocol: **a rung is adopted only if per-class accuracy on
the same held-out on-air fixture beats its predecessor.** Rungs
that tie or lose are not merged.

### Validation protocol

- Fixture: held-out on-air recordings from §11.3, frozen per
  model swap. Labels: §6.2 frequency rules as weak labels,
  overridden by §12.5 user annotations where present.
- Reporting: per-class accuracy is mandatory — aggregate numbers
  hide the scarce classes (CW, SSB on air) where adversarial
  augmentation would have to earn its keep. SNR-stratified
  accuracy follows Scholl's −10…25 dB convention. The
  Unknown/noise path is reported separately from in-class
  accuracy.
- Adversarial rungs additionally report seed/run variance; a
  single-run win is not an adoption.

### AC-WGAN assessment (honest)

At inference, an auxiliary-classifier Wasserstein GAN's
discriminator *is* a plain CNN — the GAN changes how the model
is trained (Wasserstein objective for stability,
class-conditional generation for scarce classes), not what is
deployed; only the classifier head exports to ONNX. The
surveyed evidence (Jagannath et al. 2022, §V.C.1, reviewing
small-UAV AC-WGAN classification) shows wins over SVM and
vanilla AC-GAN on 4-way indoor UAV ID at 5 dB SNR, but no
head-to-head against a supervised CNN on equal data. The
literature prior is that adversarial augmentation roughly ties
supervised baselines at abundant labels and helps in
scarce-label / distribution-coverage regimes. This product
already owns a physics-based synthesizer (`train.py`), so the
GAN's only marginal job is modeling real impairments the
synthesizer misses — a property of the real recordings,
measurable only after B2. B3 is therefore conditional on B2's
error analysis, not scheduled.

### Constraints inherited from the SPEC

- Nothing enters the §5 hot path; §6.3 per-peak inference keeps
  the 134-dim contract until a rung wins and a §20 design lock
  changes it deliberately (§18.1/§19.1 precedent).
- Deployment stays ONNX-in-Go via dlopen (§6.3); training stays
  offline PyTorch in `models/train.py`, extending the existing
  `ToVector()`/`extract_features` parity discipline to any new
  IQ-window contract.
- The §6.5 source-category contract (`class` = source enum, no
  `onnx:` prefix) is unchanged by every rung.
- Receiver-impairment caveat (survey §VI): captures etch the
  *receiver's* IQ imbalance, phase noise, and clock offsets
  into training data; recordings from multiple SDRs require
  per-receiver normalization (§16.2 per-device offset
  precedent) or training across the receiver pool.

### References

- S. Scholl, "Classification of Radio Signals and HF
  Transmission Modes with Deep Learning," arXiv:1906.04459
  (2019).
- A. Jagannath, J. Jagannath, and P. S. Pattanshetty Vasanth
  Kumar, "A Comprehensive Survey on Radio Frequency (RF)
  Fingerprinting: Traditional Approaches, Deep Learning, and
  Open Challenges," arXiv:2201.00680 (2022) — §V.C.1 (AC-WGAN
  classification), §VI (open challenges).

## 6.7 Emitter identity — transient fingerprinting research (non-normative)

**Status: research note — no contract, no obligations, no
D-decision. Promotion to a contract follows the §20 pattern
(design lock + D-decision + §17.3 obligations) only after the
staged probes below produce positive evidence.**

§6 classifies *modes*; identifying *which emitter* is speaking is
a different, harder problem. The classical feature is the turn-on
transient (PA bias ramp, PLL lock, key-up click), but the survey
(Jagannath et al. 2022) shows fingerprint accuracy collapsing when
the channel differs between training and validation (§V-B6 Task 3)
or when the receive chain does (§VI — the transmitter's fingerprint
"varies across the receivers") — the "fingerprint" is
device+receiver+channel entangled. This note records the staged,
evidence-gated probes for deciding whether emitter identity is
tractable *through our receive chains* before any capture-mode
design is attempted.

### E0 — corpus feasibility (no new hardware, no pipeline change)

Transient-lite pass over existing §11.3 recordings: slice the
first ~500 ms of each §10.2 session and extract per-session
features — CTCSS/CDCSS turn-on timing, turn-on data bursts,
first-syllable envelope, key-up frequency settling. These are the
*late*-transient artifacts that survive §10.2's
detector-confirmed session opens (the µs-scale turn-on itself is
already lost; see the pipeline-gap note below). Proceed only if
within-emitter similarity exceeds between-emitter similarity on
multi-session emitters.

Labeling dependency: this assumes we can group sessions by
emitter — §6.2 frequency rules and §12.5 annotations as labels,
with spoken repeater IDs as self-labeling anchors. If the local
population cannot be labeled, E0 starts from a hand-labeled
subset.

### E1 — cross-receiver probe (blocked on §17.4 hardware items)

Capture the same on-air emitter's key-up simultaneously on 2×
RTL-SDR + 1× HackRF (§15.4). Within-receiver vs cross-receiver
feature correlation is the go/no-go: correlated features say
emitter identity survives our chains; uncorrelated features say
the "fingerprint" is our dongles (the survey's Task-3 collapse,
project edition). The same captures double as a receive-chain
calibration probe.

H2 note (§15.4, §1.2): a controlled-transmitter bench source, if
used, lives outside this repository — `TestHackRFH2Guard` forbids
transmit APIs anywhere in the driver tree, and the HackRF's own
key-up is calibration stimulus, not a stand-in for the monitored
PMR/ham/marine population.

### Capture-pipeline gap (design candidate, not a promise)

§10.2 opens sessions on detector confirmation; there is no IQ
pre-trigger buffer (the §16 ring buffer holds waterfall rows
only); §7.1 retune queue-flushes make a scanning receiver
structurally blind to pre-detection samples. A real transient mode
is therefore a *new capture mode* — an IQ pre-trigger ring buffer
beside the §10.5 raw-IQ writer, or scheduled wideband dumps
(~80 MB/s at 20 MSPS) — and gets its own design lock. Nothing here
modifies §5, §7, or §10 normative behavior.

### Hardware notes (as-built §15.4)

- Pro: 20 MSPS sees the wideband key-up click signature that an
  RTL-SDR's ±1.2 MHz window cannot; manual-only gain (VGA+LNA)
  means no AGC pumping — the RTL driver's auto-gain mode is a
  transient killer and would need manual gain staging for any
  RTL-side work; the IF = LNA + VGA chain is deterministic
  (HARDWARE.md §8).
- Con: 8-bit samples limit amplitude-precision features; the
  stock clock is a CFO confounder (TCXO/CLKIN upgrade territory).

### Constraints on the §6.7 probes

- Nothing enters the §5 hot path; §10.2 session lifecycle
  unchanged.
- H2 intact: §15.4 stays RX-only; the guard test is untouched.
- Any adopted capture mode gets its own section and D-decision.
- §6.6 rungs and the §6.5 source-category contract are
  unaffected.

### §6.7 references

- A. Jagannath, J. Jagannath, and P. S. Pattanshetty Vasanth
  Kumar, "A Comprehensive Survey on Radio Frequency (RF)
  Fingerprinting: Traditional Approaches, Deep Learning, and Open
  Challenges," arXiv:2201.00680 (2022) — §IV.C (transient-based
  approaches), §V-B6 (Tasks 1–4), §V.F (open datasets).

## 7. Scanning

**Status: `[implemented]` (D3). `sdr-capture` runs the §7.1 loop for
`scanner`/`both` devices — `scan.*` config with §7.1 defaults, range
defaulted to and clamped into the driver capability range at startup.
The §7.4 control surface is live: manual tune parks the loop,
per-device `scanning`/`scan_paused` status, runtime park/resume via
`POST /api/v1/scan` (gateway `POST /api/sdrs/{id}/scan`), and
per-device `scan_autostart: false` boots a `scanner`/`both` device
parked at its default frequency.**

### 7.1 Scan loop (normative)

Devices with `mode: scanner` (or `both`) MUST run this loop inside
`sdr-capture`:

```textf = scan.min_hz        (default: driver FreqMin)
loop:
    SetFrequency(f)
    dwell scan.dwell_ms          (default 50 ms)
    f += scan.step_hz            (default 100 kHz)
    if f > scan.max_hz: f = scan.min_hz
```

- **Step:** `scan.step_hz`, default **100 000 Hz**.
- **Dwell:** `scan.dwell_ms`, default **50 ms**.
- **Range:** `scan.min_hz` / `scan.max_hz`, defaulting to the driver's
  capability range (RTL-SDR: 24 MHz – 1.7 GHz).
- While tuning is in flight, frames keep being produced at the
  *previous* center; each frame's `freq_hz` is authoritative, so
  consumers never need scan state.
- The loop MUST survive `SetFrequency` failures (log + retry next
  step); a failing device MUST NOT stop the loop for other devices
  (each device has an independent goroutine).
- With `mode: monitor` (or a `scanner`/`both` device whose loop is
  parked — manual tune or `POST /api/v1/scan`, §7.4) the device holds
  a fixed frequency until retuned or the sweep is re-enabled.

### 7.2 Why in capture, not in ingest or processor

- Only `sdr-capture` owns the driver handle; tuning is a local,
  zero-cost operation.
- No extra service, no new wire protocol: the scan manifests purely
  as changing `freq_hz` in otherwise identical frames.
- Downstream (processor, recorder) is completely scan-unaware.

### 7.3 Verification interaction

Dual-SDR verification (§8) is **asynchronous**: the two SDRs scan on
independent loops and no coordination message exists. A signal counts
as "verified" when both SDRs' processed observations fall into the
same frequency bucket within the verifier window — see §8. No
synchronous "sweep together" handshake is part of v1.

### 7.4 `sdr-capture` control API

HTTP on `:9090` (internal):

| Method & path | Body | Effect |
| --------------- | ------ | -------- |
| `GET /api/v1/status` | — | per-device: id, active, freq_hz, gain_db, sample_rate, mode, scanning, scan_paused |
| `POST /api/v1/frequency` | `{"id":"S1","freq_mhz":145.5}` | retune device (parks its scan loop) |
| `POST /api/v1/gain` | `{"id":"S1","gain_db":40}` | set LNA gain |
| `POST /api/v1/scan` | `{"id":"S1","enabled":true}` | resume (`true`) or park (`false`) the device's scan loop at runtime; idempotent; replies with the device status. Unknown id ⇒ `404`; device without a scan loop (mode `monitor`, or no usable sweep range) ⇒ `409` |

A manual `frequency` command **parks** that device's scan loop — the
park is runtime state and resets when `sdr-capture` restarts. The
`scan` command **resumes** it: the sweep continues from the currently
tuned frequency (a manual tune or the boot default), not from a stale
pre-park cursor. While sweeping, the loop still steps past failing
tunes (§7.1).

Per-device config (§16.2): `scan_autostart: false` attaches the scan
loop but boots the device parked at `default_freq` — the
"monitor by default, sweep on demand" receiver. Keep the device mode
`both` (not `monitor`) when using it: a `monitor` device never
attaches a scan loop, so there is nothing to resume without a
restart. An absent key defaults to `true` (the historical behavior).

The gateway proxies into this API (§13.1): `PUT /api/sdrs/{id}`
forwards `freqHz`/`gainDb` to `frequency`/`gain`, `GET
/api/sdrs/{id}/status` proxies `status` filtered to one device, and
`POST /api/sdrs/{id}/scan` forwards `{"enabled": bool}` to `scan` —
no DB write, it is runtime state (§12.1 has no scan column).

## 8. Dual-SDR Verification

**Status: `[implemented]`. `signal-processor` pairs detections with
`location.PairTracker` (sliding window, §8.2 decision table) on every
publish; a verified pair latches `signals.verified` — sticky across
re-observations (`UpsertSignal` OR-latch, §8) — records `verifications`
evidence rows, and re-broadcasts full `signal.update` payloads.**

### 8.1 Where it runs (target)

Verification runs **inside `signal-processor`**. The processor keeps a
short sliding window of recent observations per SDR. For each new
observation `(sdrA, fA, pA, tA)` it checks the window for a matching
observation from a *different* SDR `(sdrB, fB, pB, tB)` and runs the
§8.2 decision table. On success it:

1. sets `verified = true` on the affected signal row(s),
2. inserts a row into the `verifications` table,
3. emits `signal.update` with the updated `verified` flag.

An unverified signal stays `verified = false` indefinitely (v1 has no
un-verify; a later contradicting observation is out of scope).

### 8.2 Decision table (normative)

Inputs: observation frequencies (Hz), powers (dB, relative §5.6),
observation times.

| Check | Threshold | Failure result |
| ------- | ----------- | ---------------- |
| `sdrA == sdrB` | rejected | error (never verify against self) |
| `abs(fA − fB)` | ≤ **5 000 Hz** | `verified=false`, confidence 0.0 |
| `abs(tA − tB)` | ≤ **2 s** (`MaxTimeDiff`) | `verified=false`, confidence 0.2 |
| `abs(pA − pB)` | scoring, not a gate | see below |

Power scoring (after the two gates pass):

```textconfidence = 0.9
if |pA − pB| > 3 dB:  confidence -= 0.2
if |pA − pB| > 6 dB:  confidence -= 0.3
verified     = confidence >= 0.5
```

So: ≤ 3 dB → 0.9 (verified); 3–6 dB → 0.7 (verified);
> 6 dB → 0.4 (not verified). Confidence is clamped to `[0, 1]`.

**Timing note:** frame `timestamp` has 1-second resolution; the
processor MUST compare its own observation times (wall clock at
processing), not frame timestamps, to get the full 2 s window.

### 8.3 Frequency matching detail

The 5 kHz gate is against **detected peak frequencies** (not tuning
centers), so independent scan loops (§7.3) still match: both SDRs
detect the true emitter frequency within their own bin resolution.
Because signal IDs already bucket at 10 kHz (§9.1), a verified pair
always maps to at most two DB rows (one per SDR) — the `verified`
flag is stored per row, and both rows are updated on success.

## 9. Location & Signal Identity

**Status: identity `[implemented]`; single-SDR placement
`[implemented]`; unlocated-signal handling in §9.3 is `[implemented]`
(A1); tracking `[implemented]` (§9.4); TDOA multilateration — engine
`[implemented]` (§9.5), wiring `[implemented]` (§9.6); on-air
validation pending (slice 5 exit criterion).**

### 9.1 Signal ID — deterministic UUIDv5

```textbucket = freqHz / 10_000                       // 10 kHz bucket
name   = "sigint-workbench/<sdrID>/<bucket>"   // e.g. .../S1/14550
id     = UUIDv5(NameSpaceURL, name)
```

- Same SDR + same 10 kHz bucket ⇒ **same ID**, across restarts.
- Different SDR at the same frequency ⇒ different ID (per-SDR
  rows; verification links them, §8).
- The ID is the database primary key **and** the identifier used in
  REST, WS events, and recording filenames.
- Drift within ±5 kHz around a bucket boundary can split one emitter
  into two rows; this is accepted in v1 (documented limitation).

### 9.2 Single-receiver placement

A signal inherits the location of its SDR row (`lat`, `lon` from
`sdr-capture` config):

```textmethod = "sdr_position"
accuracy = 0     // means "exactly at the SDR" in v1
```

The UI renders an accuracy circle from a client-side default when
`accuracy == 0` (current default: 1000 m circle radius fallback in
`MapView`).

Multi-receiver geolocation (TDOA multilateration) is out of scope in
v1; it is deferred to Phase 4 (§17.4) and will use the `tdoa` value
already reserved in `Location.Method`.

### 9.3 Unlocated signals (A1) — `[implemented]`

**Implemented:** in `cmd/signal-processor`, signals from an SDR
**without** configured `lat`/`lon` are no longer dropped. `lat`/`lon`
are nullable (`*float64`), the query matches on `location IS NULL`,
and there is no early-return for unknown SDRs, so an unlocated signal
flows through detect → classify → persist → publish → list.

**Contract:** an unlocated signal MUST be:

- **detected and classified** as usual,
- **persisted** with `location NULL`,
- **listed** by `GET /api/signals` and in the dashboard signal list
  (list entries never depend on location),
- **omitted from the map layer** (GeoJSON source excludes
  `location IS NULL` rows),
- **still eligible** for 2-SDR verification (§8 needs no coordinates).

No silent drops anywhere in the pipeline.

### 9.4 Tracking

`[implemented]` (Phase 4). `Track` (`internal/location`) — haversine
distance between consecutive fixes, `speed` in km/h, `IsMoving` at
**> 1 km/h**, heading from `atan2(dLon, dLat)`. signal-processor
appends every placement of a located signal to its in-memory track,
persists one `tracks` row per signal (path = the last 200 fixes as a
`LINESTRING`, `UNIQUE(signal_id)` upsert) at ≤ 1 Hz, writes a final
row when the TTL sweep retires the signal (the row remains as
history, §11.2), and emits a coarse `track.update` event (movement
summary + last fix, ≤ 1 Hz, §14.2). REST: `GET /api/signals/{id}/track`
returns the persisted path (§13.1).

### 9.5 TDOA multilateration — **APPROVED 2026-10-04, slice 4**

**Design review passed 2026-10-04 (the §17.4 slice 4 gate).
Implementation is slice 5 (§17.4) — simulator first, then wiring,
then on-air validation.**

TDOA locates a signal from the **difference** of its arrival time at
pairs of receivers. It complements §9.3 (single-SDR placement from
the detecting receiver's position) with a receiver-geometry-based
fix, and it produces `SignalLocation.Method = "tdoa"` (the location
model already carries the enum).

**Prerequisites (normative):**

- Frames per the §4.5 v2 format — sample-accurate anchors and
  sequence numbers; the 1-second v1 timestamp MUST NOT be used
  (§4.2).
- ≥ 2 receivers observing the same emission in the same band. A
  2-receiver pair yields a hyperbolic locus only (no point fix);
  a point fix requires ≥ 3 receivers forming ≥ 2 independent
  baselines. The engine MUST report which case produced a result.
- Receivers in a solution MUST share one timing domain (single host,
  or hosts whose sync quality is known, §16). Mixed-rate receivers
  MUST be resampled to a common rate before correlation.

**Mechanism (slice 5):**

1. **Window selection.** The engine aligns a common observation
   window (target ~10 ms of IQ, scaled to the modulation) across
   receivers using the §4.5 anchors, taking the intersection of the
   receivers' contiguous sample runs (gap-free in `sample_index`).
2. **Delay estimation.** Per receiver pair, Generalized
   Cross-Correlation with Phase Transform (GCC-PHAT) on the
   band-shifted complex baseband; the peak gives coarse τ, refined
   to sub-sample delay by parabolic interpolation of the correlation
   peak.
3. **Fix.** Each pair contributes a hyperbola
   `‖x − rᵢ‖ − ‖x − rⱼ‖ = c·τᵢⱼ` (c = speed of light). With ≥ 2
   independent baselines the engine solves the nonlinear
   least-squares (Gauss–Newton, seeded from the §9.3 SDR-position
   estimate when available); with exactly 2 receivers it publishes
   the locus endpoints only. The hyperbola itself is unbounded, so
   the endpoints are a viewing convention, not a measurement: the
   branch implied by the sign of τᵢⱼ is clipped to a
   `locus_radius_m` circle about the baseline midpoint (default 3×
   the pair's baseline length).
4. **Quality reporting.** Every fix carries: `residual_ns`
   (post-solve RMS time residual), `pairs_used`,
   `max_baseline_m`, and the clock `sync_quality` of the involved
   hosts (§16). The gate to overwrite a §9.3 placement is
   configurable and defaults to requiring a positive-definite
   solution covariance; otherwise the TDOA fix is published as an
   event only.

**Receiver networks (N > 2, normative):**

- **Pair selection.** The engine computes GCC-PHAT delays for
  **all** receiver pairs while `C(N,2) ≤ pair_cap` (configurable,
  default `pair_cap = 15`), which yields N−1 independent baselines
  plus redundancy. Above the cap it MUST fall back to a
  **reference-receiver spanning tree** (N−1 pairs).
- **Reference receiver.** Chosen per solve, in order: (1) best
  clock sync quality, (2) longest gap-free sample run in the
  window, (3) most central position (minimum total distance to the
  others). Reported as `reference` in the fix.
- **Weighting.** Each pair's observation equation is weighted by
  `1/σᵢⱼ²`, where `σᵢⱼ` at minimum combines the host-clock
  uncertainty of both receivers (§16) and correlation-peak
  quality. Until slice 6 ships sync-quality data, single-host
  solves are uniformly weighted (shared clock ⇒ zero relative
  skew by construction).
- **Eligibility gate.** A pair is eligible only if its combined
  clock uncertainty, scaled by `c ≈ 300 m/µs`, stays inside the
  configured accuracy budget. Ineligible pairs are excluded from
  the solve and counted in the result.
- **Outlier rejection.** After the first solve, the pair with the
  largest normalized residual is dropped and the solve repeated
  while any residual exceeds a configurable threshold (default
  3σ) and ≥ 2 independent baselines remain. Falling below 2
  baselines degrades the result to locus-only (or no-fix with
  reason) — never a silent point fix.
- **Geometry model.** Receiver lat/lng are projected to a local
  ENU frame (origin = reference receiver), the solve is 2-D, and
  the fix is converted back to lat/lng. Mixed receiver altitudes
  introduce a small known bias; a 3-D solve is a future
  refinement, out of scope for slices 4–6.
- **Alignment buffer.** Per-receiver contiguous runs are buffered
  for a configurable horizon (default 500 ms) sized to exceed max
  cross-host skew + jitter + window length; window matching is by
  UTC-anchor intersection of gap-free `sample_index` runs
  (mechanism step 1).

**Validation path (slice 5, §17.4):** simulator first, and
N-generic from day one — a 5-receiver virtual network with
randomized geometry, analytically synthesized geometry-true delays
(exact to sub-sample by construction), a deterministic seeded
multitone waveform at a stated simulator SNR (**20 dB**), and one
receiver given a deliberately wrong anchor offset — its pairs
inherit the error — must recover the known transmitter position
within the accuracy budget **and** reject the corrupted pairs via
outlier rejection; a solver-level test additionally corrupts
exactly one pair's measurement and asserts that pair alone is
dropped. The 2-receiver locus-only case is exercised as the
degenerate solve, including the endpoint-clipping convention above,
as is a mixed-rate solve (engine-side resample per the
prerequisite). On-air validation follows against a known continuous
transmitter — three receivers (2× RTL-SDR + HackRF as designed, or
3× RTL-SDR on a shared narrowband carrier until HackRF hardware
arrives; runbook: docs/HARDWARE.md §7), single-host first.

### 9.6 TDOA engine wiring — **APPROVED 2026-10-04, slice 5 — `[implemented]`**

**Design review passed 2026-10-04. The engine (`internal/tdoa`) and
its simulator land under slice 5; wiring, §4.5 v2 delivery, and
on-air validation follow. Implemented (slice 5):** the §4.5 v2
delivery (codec, capture stamping, dual-format consumers, per-sender
gap counters), migration 004 quality columns, the `tdoaEngine`
(`cmd/signal-processor/tdoa.go`: band-shift + decimate to a
bandwidth-derived common solve rate, gap-free run assembly, the
trigger/quality/overwrite policy below, fix persistence on the
reference receiver's row, `signal.tdoa` events) and the hub
allowlist. On-air validation remains the slice-5 exit criterion
(runbook: docs/HARDWARE.md §7). One
deliberate deviation: the engine consumes v2 frames directly at UDP
decode rather than dual-porting the §5.1 FFT assembler — same intent
(no duplicated IQ stream, no new service) with its own gap-aware
contiguous runs.

**Placement.** The engine is a pure-Go package `internal/tdoa`
with three parts: `Buffer` (per-receiver contiguous-run store —
the §9.5 alignment buffer), `Correlator` (GCC-PHAT pair delays
with parabolic refinement), and `Solver` (weighted Gauss–Newton
with outlier rejection). `internal/tdoa` imports no service and no
database code; simulator tests exercise it directly with injected
delays. The wiring lives in signal-processor: a `tdoa` goroutine
consumes assembled windows (dual-ported from the existing per-SDR
assembler, §5.1) and emits fixes — no new service, no duplicated
IQ stream.

**Protocol v2 delivery.** Slice 5 implements §4.5 end to end:
`sdr-capture` emits `SDR2` frames (64-byte header, CLOCK_REALTIME
anchor at the first sample, per-sender `seq`/`sample_index`);
`iq-ingest`, `recorder`, and `signal-processor` accept both
formats and keep per-sender gap counters from `seq`. The encoder
stays on v1 until every consumer accepts v2 (config
`stream_format: sdr1 | sdr2`, default `sdr1`, flipped in compose
once rollout completes).

**Trigger.** A solve attempt runs when, for a signal persisting
≥ 2 s, ≥ 2 receivers simultaneously cover its frequency (monitor
mode or parked sweep) and the alignment buffer holds intersecting
gap-free runs for the pair set. Attempts are rate-limited to
≤ 1 Hz per signal. Receivers swept off the emission contribute no
window and are simply absent from that solve.

**Results.**

- Persistence: fixes are `signal_locations` rows with
  `method = 'tdoa'` (migration 004 adds nullable `residual_ns`,
  `pairs_used`, `max_baseline_m`, `reference` columns; NULL for
  non-TDOA rows). TDOA rows feed §9.4 tracking like any placement.
- The §9.5 placement-overwrite gate decides whether a fix
  replaces the receiver-position placement or is event-only.
- Events: `signal.tdoa` (§14.2) carries signal id, fix or locus,
  the quality surface, `reference`, and receiver ids; hub
  allowlist extended. Locus-only results emit
  `locus: {lat1,lng1,lat2,lng2}` and persist nothing.
- REST: TDOA quality fields ride existing location payloads when
  `method = 'tdoa'`; no new endpoint.

**Accuracy budget.** A fix is accepted if the post-solve RMS
residual maps to ≤ `accuracy_budget_m` (defaults: **50 m**
simulator gate, **200 m** on-air) — otherwise a `signal.tdoa`
event with `accepted: false` and a reason is emitted, and nothing
persists. Budgets are config, not constants.

**Config (signal-processor):** `tdoa.enabled` (false),
`tdoa.window_ms` (10), `tdoa.buffer_horizon_ms` (500),
`tdoa.pair_cap` (15), `tdoa.outlier_sigma` (3),
`tdoa.accuracy_budget_m` (200), `tdoa.overwrite_placement` (true,
covariance-gated), `tdoa.locus_radius_m` (0 ⇒ 3× the pair's
baseline).

**Testing obligations (§17.3):** the §9.5 validation gate as
tests — deterministic-seed generator (seeded multitone,
analytically exact delays, AWGN at the stated SNR, anchor-offset
corruption plus solver-level single-pair corruption; assert
fix-within-budget + rejection), 2-receiver locus case (endpoint
clipping), mixed-rate resample solve, v2 encode/decode round-trip +
v1/v2 mixed acceptance + gap counters, live-DB migration test for
the new columns.

## 10. Audio

**Status: demodulators (incl. SSB), the WAV encoder, and the
streaming `WAVWriter` are `[implemented]`; in-band capture (D1) is
`[implemented]` in `cmd/recorder` (§10.2, DB-poll tracked sessions);
the live Opus transport (D1b, §10.4) is `[implemented]` end to end —
per-signal mux + internal `:9012/ws/audio` server, relayed by the
gateway's `GET /ws/audio` (§2.2), consumed by the dashboard's
`LiveAudioPlayer` (WebCodecs, slice 7); the §10.6 `audio.level` feed
is `[implemented]` end to end, frontend store included (Phase 4).**

### 10.1 Demodulator contract

Go interface (`audio.Demodulator`):

```textName()            string    "WFM" | "NFM" | "AM" | "USB" | "LSB"
Demodulate(iq []complex64, sampleRate uint32) ([]float32, error)
AudioSampleRate() uint32
CanHandle(mod string) bool

// optional SubtypeAware — implemented by every built-in demod:
CanHandlePair(mod, subType string) bool
```

Output is **mono float32 in −1.0 … 1.0** at the output rate. Demods
require ≥ 4 IQ samples; fewer is an error (the recorder skips the
frame).

| Demod | Algorithm | Deviation | Output rate | Status |
| ------- | ----------- | ----------- | ------------- | -------- |
| WFM | cross-product phase discriminator (`atan2(cross, dot)`, unwrapped), ÷ deviation | 25 000 Hz | 48 kHz | `[implemented]` |
| NFM | same discriminator | 2 500 Hz | 48 kHz | `[implemented]` |
| AM | envelope `sqrt(I²+Q²)`, DC removal, peak normalize | — | 48 kHz | `[implemented]` |
| USB/LSB | frequency-domain sideband select (FFT, keep wanted sideband ≤ 3 kHz, conjugate-mirror, inverse FFT, real part) | — | 48 kHz | `[implemented]` |

Common post-processing (FM, AM and SSB): moving-average low-pass of
length `decimateFactor` (odd-padded, symmetric), then decimate by
`round(sampleRate / 48000)`, hard-clip at ±1.0.

**Registry selection (§10.1 fix, `[implemented]`):**
`Registry.Get(modulation, subType)` selects on the full pair — exact
`SubtypeAware.CanHandlePair` match first, then modulation-family
fallback in registration order (an empty or unrecognized `subType`
falls back). The default registry maps `FM/NFM → 2.5 kHz demod`,
`FM/WFM → 25 kHz`, `SSB/USB|LSB → SSB demod`, `AM → envelope`;
`FM` with no subtype resolves to WFM (first registered).

### 10.2 In-band capture (D1)

The **default** audio path: `iq-ingest` fans its stream to the
recorder (`:9011`) exactly like the processor. The recorder:

1. reads frames (§4 protocol) and refreshes an **active-signal
   snapshot from the database every 5 s** (`GetSignals`) — it does
   not subscribe to the hub; every tracked signal is a candidate,
2. for each tracked signal inside the streamed band (frame center
   ± sampleRate/2), carves the in-band IQ around the detected peak
   (baseband shift by the peak offset; the mixer is
   phase-continuous across frames so FM does not click at frame
   boundaries),
3. demodulates with the registry (§10.1),
4. feeds the live Opus stream (§10.4) and the WAV writer (§10.5).

**Session lifecycle** (per signal): a session opens on the first
in-band frame of a tracked, registry-demodulable signal. It
finalizes after `capture.close_silence_s` (default **10 s**) without
a fed frame — which is also how a signal leaving the active set
(TTL, §11.2) ends its recording, since the next poll simply stops
feeding it. At `capture.max_concurrent` (default **8**) new signals
are skipped until a session closes (strongest-first compaction is
future work). Empty or damaged sessions are discarded — files
removed, no `recordings` row.

No SDR retuning ever happens for audio — the capture/scan behavior
is completely unaffected.

### 10.3 Dedicated-tune monitor mode (D1b, optional)

A device may be pinned to a frequency (`mode: monitor` in
`sdr-capture.yaml`, §16.2, or a manual retune — operator-facing via
the gateway `PUT /api/sdrs/{id}`, §13.1, which forwards to capture's
`POST /api/v1/frequency`, §7.4) to get a known-capture-frequency
live audio channel — e.g. a fixed aviation or marine frequency. The
in-band path works identically; the peak sits at DC and the baseband
shift is trivial. At most the pinned channel streams audio; this
mode is an operator convenience, not a pipeline change. A manual
frequency command parks that device's scan loop (§7.4); the runtime
`POST /api/sdrs/{id}/scan` resume brings the sweep back from the
pinned frequency for the rest of the session.

### 10.4 Live transport — Opus over WebSocket (D1b, normative)

- **Path:** browser → `api-gateway` `GET /ws/audio?signal=<signalId>`
  → gateway relays to the recorder's internal
  `ws://recorder:9012/ws/audio?signal=<signalId>`.
- **Codec:** Opus, **mono**, 48 kHz input, **24 kbps CBR**,
  20 ms frames (960 input samples per packet). Implemented with
  libopus via cgo; the recorder gets its **own Debian-based image**
  (`Dockerfile.recorder`) because `Dockerfile.services` is
  `CGO_ENABLED=0`.
- **Framing (normative):**
  1. On connect, the recorder sends exactly **one text (JSON)
     hello**:
     `{"type":"audio.meta","signalId":"<uuid>","centerHz":<n>,`
     `"modulation":"<FM|AM|SSB>","subType":"<WFM|NFM|USB|LSB>"`
  2. Then **binary messages only**: each binary WebSocket message is
     **exactly one Opus packet** (TOC byte + payload, per RFC 6716).
     No length prefix, no timestamp, no extra header.
  3. Playback order = message order. Missing messages = audio gaps
     (UDP-like tolerance; no retransmission).
  4. Stream end: recorder closes the stream with close code 1000.
- The gateway relay is transparent (frames pass through untouched);
  it performs no decoding.
- One live stream per signal per client; the recorder multiplexes
  per-signal encoder state. (`subType` in the hello is the signal's
  stored value and **may be empty** — the §10.1 registry fallback
  already resolved the demodulator.)
- **Semantics (normative):** audio flows one way — client messages
  are ignored (ping/pong keepalive only). With **no subscriber** on
  a signal, its audio is **dropped, not buffered**: a new listener
  hears from now on, never a backlog. Subscribe carries a **3 s
  grace** (the session opens on the next in-band frame); no stream
  by then ⇒ close code 1000 ("no live stream"). A missing or
  non-UUID `signal` parameter ⇒ `400` before upgrade. Each write
  has a **5 s deadline**, so a stuck client can never wedge the
  packet pump.
- **Build gate:** without the `opus` build tag (libopus) the
  recorder skips the live server entirely; recording (§10.2/§10.5)
  is unaffected.
- **Client** (`[implemented]`, slice 7): the dashboard's
  `LiveAudioPlayer` (SignalDetail) opens the relayed stream via
  `audioWsUrl()`, decodes with WebCodecs `AudioDecoder`
  (feature-detected; browsers without it show the live button as
  unsupported, and recordings remain playable via
  `GET /api/recordings/{id}/audio`, §13.1), and plays through
  Web Audio on a ~120 ms jitter-buffer playhead — late audio is
  dropped forward, never replayed (§10.4.3). Close code 1000 renders
  as "ended", not an error; one live stream at a time. Vitest covers
  the state machine and framing (FakeWebSocket + stub decoder).
- **Client (history):** before slice 7 no browser consumer existed —
  the dashboard played **recordings** only.

### 10.5 Recorded files (D1)

Per recording session the recorder writes:

- **WAV** (`[implemented]`, always): 16-bit PCM mono, 48 kHz —
  `WAVWriter` (RIFF, `fmt` chunk PCM/1ch/16-bit, `data` chunk, all
  little-endian; byte-identical to the test-only `EncodeWAV`, §10.2).
- **Raw IQ** (`[implemented]`, slice 7, when `iq.enabled`, §16.5):
  `int16[2n]` interleaved I/Q, headerless, little-endian, at the
  capture sample rate — the per-signal **frequency-shifted baseband**
  (post-mixer, pre-demod), not the full tuned band. It lands as a
  second file beside the WAV and gets its own `recordings` row with
  `file_format='iq'` (§12.3: `iq` is no longer reserved-only). Empty
  files are removed (§10.2); §11.3 retention sweeps `.iq` with
  `.wav`.

**FLAC is not supported and MUST NOT be claimed** (the README's
former "raw IQ + decoded audio (WAV/FLAC)" claim was removed in the
README realignment). File names are **flat under `recordings_dir`**:
`<signalid8>-<yyyymmddThhmmss>.{wav,iq}` — first 8 characters of the
signal UUID + UTC timestamp, filesystem-safe and sortable. There are
no per-SDR or per-date subdirectories; §11.3 retention scans this
one directory.

### 10.6 Levels

Live level metering `[implemented]` (Phase 4): every actively
demodulated signal (an open §10.2 session) runs the §5.7 AGC
constants (target 0.95, attack 1, release 50) on its demodulated
float32 stream; the post-AGC RMS — clamped to [0,1] — is published as
a coarse `audio.level` event (100 ms ticker = 10 Hz cap), one per
demodulated signal, POSTed to the hub's `/api/events` ingest. The
recorder publishes when `WS_HUB_URL` is set (§16.1; hub errors are
logged at most once per 30 s); unset disables the feed. Signals that
are not being demodulated emit nothing.

Frontend `[implemented]` (Phase 4): the `signalLevels` store consumes
`audio.level` over the §14.4.2 socket (stale entries prune after
1.5 s, matching the feed cadence) and drives the `VUMeter` in
SignalDetail; recording playback streams the WAV via
`GET /api/recordings/{id}/audio` (§13.1) with its own local
AnalyserNode meter.

## 11. Recordings & Retention

**Status: sweep/TTL `[implemented]` (inactive-flag model, §11.2);
in-band recording trigger/writer is `[implemented]` in `cmd/recorder`
(§10.2: silence-hysteresis sessions, demodulator-registry gated),
with the §11.1 max-duration cap (`iq.max_duration_s`) enforced in the
same poll and raw-IQ output per §10.5 (`iq.enabled`); archive purge
and file retention are `[implemented]` (§11.3 `SelectPurge` +
`PurgeInactiveSignals`, 30 days / 50 GB).**

### 11.1 Recording trigger

A recording starts when a **demodulable** signal appears
(modulation ∈ {FM, AM, SSB} with a matching registry entry) and ends
when its §10.2 session finalizes — the `capture.close_silence_s`
silence hysteresis, typically because the signal left the active set
(TTL, §11.2) — or when it runs past the **max-duration cap**
(`[implemented]`, slice 7): `iq.max_duration_s` (default **300 s**,
§16.5; negative disables the cap) is checked on the recorder's 5 s
housekeeping poll, finalizes the session normally (row + §10.5
files), and a still-active signal rolls over into a fresh session on
its next in-band frame. Each recording writes a `recordings` row per
file of §10.5 (empty sessions write nothing). Multiple simultaneous
signals record independently, capped by `capture.max_concurrent`
(§10.2).

### 11.2 Signal lifecycle (D6)

- **Sweep interval:** 5 s (processor).
- **TTL:** a signal not re-observed for `SIGNAL_TTL` (default
  **30 s**) is retired: the row is flagged `active = false`
  (`[implemented]` — column `DEFAULT TRUE` + partial index
  `idx_signals_active`); `signal.removed` is still emitted (same
  semantics for clients: "no longer live"); the row is
  **preserved** for history. Processor startup deactivates rows
  left active by a previous process (restart reconciliation), so
  live views never show zombies; still-transmitting signals are
  re-activated by their next upsert. Existing databases apply
  `db/migrations/001_signals_active.sql` (`make db-migrate`).
- **Active views:** `GET /api/signals` and the dashboard list show
  only `active = true` rows.
- **Archive purge:** rows inactive for **30 days** are deleted by a
  scheduled purge (recorder or processor; runs daily).

### 11.3 File retention

- Recordings are kept for **30 days**, subject to a **50 GB** total
  cap across `recordings/`.
- When either limit is hit, the **oldest** files are deleted first
  (by `start_time`), and matching `recordings` rows are removed
  (keeping the signal row; `ON DELETE SET NULL` already protects
  signal integrity).
- Purge runs on the same daily schedule as §11.2.

## 12. Data Model

**Status: schema `[implemented]` (`db/init.sql`; existing databases
add `active`/`method` via `db/migrations/001_signals_active.sql`,
`make db-migrate`).**

PostGIS 16 / Postgres 16, extensions `postgis` + `uuid-ossp`.
Applied automatically on first start via
`docker-entrypoint-initdb.d`. Six tables:

### 12.1 `sdrs`

| Column | Type | Notes |
| -------- | ------ | ------- |
| id | TEXT PK | `S1`, `S2`, … — matches frame `sdr_id` |
| model | TEXT NOT NULL | e.g. `RTL-SDR`, `Simulator` |
| serial | TEXT | |
| lat / lon | DOUBLE PRECISION | NULL allowed (→ §9.3) |
| gain_db | DOUBLE PRECISION | default 40 |
| freq_hz | BIGINT | default 0 (last tuning) |
| active | BOOLEAN | default true |
| created_at | TIMESTAMPTZ | default now() |

### 12.2 `signals`

| Column | Type | Notes |
| -------- | ------ | ------- |
| id | UUID PK | **explicit UUIDv5** from §9.1 (default `uuid_generate_v4()` exists but is never used by the processor) |
| frequency_hz | BIGINT NOT NULL | absolute peak |
| bandwidth_hz | INTEGER | −3 dB estimate |
| modulation / sub_type | TEXT | §6.1 |
| class | TEXT | §6.5 enum — never a method tag |
| confidence | REAL | 0–1 |
| power_dbm | REAL | calibrated dBm when `power_calibrated`, else relative dB (§5.6) |
| power_calibrated | BOOLEAN | `[implemented]` — §5.6 honesty flag; default false |
| location | GEOGRAPHY(POINT, 4326) | NULL allowed (→ §9.3) |
| accuracy_m | REAL | 0 = exactly at SDR (§9.2) |
| first_seen / last_seen | TIMESTAMPTZ | upsert semantics: first_seen kept, last_seen refreshed |
| sdr_id | TEXT → sdrs(id) | |
| verified | BOOLEAN | default false, §8 |
| active | BOOLEAN | `[implemented]` — D6 lifecycle; partial index `idx_signals_active` (`last_seen DESC WHERE active`) |
| method | TEXT | `[implemented]` — `rules`/`onnx`; refreshed on every upsert |
| residual_ns / pairs_used / max_baseline_m / reference | nullable DOUBLE PRECISION / INTEGER / DOUBLE PRECISION / TEXT | `[implemented]` — §9.6 TDOA quality (migration 004); non-NULL only for accepted TDOA fixes, sticky across re-observations (COALESCE upsert) |

Indexes: GIST on `location`; B-tree on `frequency_hz`, `class`,
`last_seen DESC`, `sdr_id`; partial `idx_signals_active` on
`last_seen DESC WHERE active`.

### 12.3 `recordings`

`id` UUID PK; `signal_id` UUID → signals **ON DELETE SET NULL**;
`start_time` NOT NULL; `end_time`; `duration_s`; `sample_rate`;
`center_freq`; `bandwidth_hz`; `file_path` NOT NULL; `file_format`
(`wav` | `iq`); `size_bytes`. Indexes: `signal_id`, `start_time
DESC`.

### 12.4 `tracks`

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**
(`UNIQUE` — one current track per signal); `path`
GEOGRAPHY(LINESTRING, 4326); `speed_kmh`; `heading`; `updated_at`.
GIST on `path`. `[implemented]` — populated by signal-processor
(Phase 4, §9.4).

### 12.5 `annotations`

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**;
`user_note` TEXT; `created_at`. REST surface `[implemented]`
(Phase 4): `GET/POST /api/signals/{id}/annotations` (§13.1) — lists
are newest-first, POST returns `201` with the created row, unknown
signal ⇒ `404`, blank/missing `userNote` ⇒ `400`.

### 12.6 `verifications`

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**;
`sdr1_id`, `sdr2_id` TEXT; `verified` BOOLEAN; `confidence` REAL;
`created_at`. Written by §8.

### 12.7 JSON mapping

REST and WS payloads use the **camelCase** keys of
`internal/db/models.go` (`freqHz`, `bandwidthHz`, `sdrId`,
`firstSeen`, `powerDbm`, `powerCalibrated`, `signalId`, …).
`location` is flattened to `lat`/`lon` (NULL when unlocated). No
snake_case in any client payload. `powerCalibrated` (§5.6) marks
whether `powerDbm` is an absolute level — false means relative dB.

## 13. REST API

**Status: all §13.1 endpoints `[implemented]` (Phase 2), including
the `GET /ws` and `GET /ws/audio` relays (A3, §2.2) and the
control-API proxy — `PUT /api/sdrs/{id}` forwards `freqHz`/`gainDb`,
`GET /api/sdrs/{id}/status` proxies live state, and
`POST /api/sdrs/{id}/scan` parks/resumes the sweep (§7.4); §13.2 gap
fixes — all 4 closed (item 3 closed with the control-API proxy).
Signal annotations (`GET/POST /api/signals/{id}/annotations`) were
the last `[planned]` row — `[implemented]` in Phase 4.**

All routes are served by `api-gateway` on `:8080` (chi router).
General contract:

- JSON everywhere (`Content-Type: application/json`), camelCase keys
  (§12.7).
- **DB down ⇒ `503`** on every `/api/*` route (gateway stays up;
  `/health` still answers).
- DB-backed handlers run with a **5 s** request timeout; expiry
  surfaces as `500`.
- CORS: origins from the `ALLOWED_ORIGINS` env (comma-separated;
  default `http://localhost:3000` and `http://localhost:5173`; unset
  vs set-but-empty semantics in §17.2), methods
  GET/POST/PUT/DELETE/OPTIONS, `AllowCredentials: false`
  (`[implemented]` — the `*` wildcard was removed, Phase 1).
- Errors: `{"error":"<message>"}` with 400/404/500/503 as
  appropriate.

### 13.1 Endpoint table

| Method & path | Status | Behavior |
| --------------- | -------- | ---------- |
| `GET /health` | `[implemented]` | `{"status":"ok"}` (no DB dependency) |
| `GET /api/signals` | `[implemented]` | Active signals; bbox filter `?minLat&minLon&maxLat&maxLon` (all default → whole world); **max 500 rows**; ordered `last_seen DESC` |
| `GET /api/signals/{id}` | `[implemented]` | One signal or `404` |
| `GET /api/recordings` | `[implemented]` | `?limit` (default 50, capped 500) and `?signalId` filters; `startTime DESC` |
| `GET /api/recordings/{id}/audio` | `[implemented]` | Streams the file (path-confined to `RECORDINGS_DIR`); `audio/wav` for wav; every other format (`iq`) serves as `application/octet-stream` (`flac` branch removed in Phase 1 — no FLAC support, §10.5) |
| `GET /api/sdrs` | `[implemented]` | All SDR rows |
| `PUT /api/sdrs/{id}` | `[implemented]` | Partial update (`model`, `serial`, `lat`, `lon`, `gainDb`, `freqHz`, `active`); `freqHz`/`gainDb` are also forwarded to the `sdr-capture` control API (§7.4) so they retune hardware — capture unreachable ⇒ `502`, unknown device at capture ⇒ `404`; never silently DB-only (§13.2.3) |
| `GET /ws` | `[implemented]` | Transparent bidirectional relay to `ws-hub:8081/ws` (§2.2, Phase 2); the hub is dialed before the client upgrade — hub down ⇒ `502` JSON `{"error":"ws-hub unreachable"}`; foreign origins ⇒ `403` (`ALLOWED_ORIGINS`, §17.2); close frames relayed with code/reason preserved (B17) |
| `GET /ws/audio` | `[implemented]` | Transparent relay to recorder `:9012/ws/audio?signal=<id>` (§10.4, Phase 2): one text `audio.meta` hello, then binary Opus packets pass untouched; recorder down ⇒ `502` JSON; foreign origins ⇒ `403`; the recorder's clean close reaches the browser with code/reason intact (§10.4.4, B17) |
| `GET /api/sdrs/{id}/status` | `[implemented]` | Proxy of capture control `GET /api/v1/status` filtered to the device (§7.4); capture down ⇒ `502`, unknown id ⇒ `404` |
| `POST /api/sdrs/{id}/scan` | `[implemented]` | Runtime park/resume of the device's sweep loop (§7.4): body `{"enabled": bool}` forwarded to capture's `POST /api/v1/scan`, device's live status relayed; no DB write (runtime state). Unknown id ⇒ `404`, device without a scan loop ⇒ `409`, capture down ⇒ `502`, DB down ⇒ `503` |
| `GET/POST /api/signals/{id}/annotations` | `[implemented]` | List a signal's user notes (newest first) / add one — POST body `{"userNote"}` ⇒ `201` + created row; unknown signal ⇒ `404`; blank or missing note ⇒ `400` (§12.5) |
| `GET /api/signals/{id}/track` | `[implemented]` | Current track `{"signalId","path":[{lat,lon}…],"speedKmh","headingDeg","updatedAt"}`; no track ⇒ `404` (§9.4, §12.4) |
| `POST /api/recordings/{id}/tfr` | `[implemented]` | On-demand time-frequency render over stored IQ (§19, D10): body `{"method","t0","t1","nfft","overlap?","window?","freqSpan?"}` ⇒ numeric int8-dB tiles + metadata (method, span, bin geometry, §19.2 dominant artifact), proxied to the recorder; unknown/no-IQ/purged recording ⇒ `404`, invalid params ⇒ `400`, span > `tfr.max_span_s` ⇒ `413`; `tfr.enabled: false` ⇒ `404` (§19.3) |
| `GET /api/settings` | `[implemented]` | §20 schema + current values for all five sections (values `null` when a file is missing or unparseable) |
| `GET /api/settings/{section}` | `[implemented]` | One section schema + values; unknown section ⇒ `404` (§20) |
| `PUT /api/settings/{section}` | `[implemented]` | Validate + commit a section (§20.3); field error ⇒ `400 {"error","field"}`, unknown section ⇒ `404`; the answer names the services to restart (D11) |
| `GET /api/setup/status` | `[implemented]` | Concurrent ≤2 s probes (db, ws-hub, recorder, capture) + config-dir writability — documented §13 exception: fan-out, not proxying (§20.4) |
| `GET /api/setup/state` | `[implemented]` | First-run flag (§20.5); DB down ⇒ `503` like every /api route |
| `PUT /api/setup/state` | `[implemented]` | `{"first_run": bool}` completes or re-arms the wizard (§20.5) |

### 13.2 Gap fixes to existing endpoints

1. **`GET /api/signals`** MUST add the `active = true` filter when
   the D6 column lands, and MUST include unlocated signals
   (`location IS NULL`) — those rows are simply without `lat`/`lon`
   (A1, §9.3) — **done** (unlocated inclusion: Phase 1; `active`
   filter: Phase 2, §11.2).
2. **`GET /api/recordings/{id}/audio`**: the `flac` content-type
   branch was removed (Phase 1 — FLAC unsupported, §10.5); `iq`
   files serve as `application/octet-stream`.
3. **`PUT /api/sdrs/{id}`**: on a control-API failure the gateway
   answers `502` (`404` when capture reports the id unknown) with a
   clear error body — never silently DB-only. **Done** (control-API
   proxy, Phase 2; the `502` variant was chosen).
4. **`GET /ws`**: the in-process hub implementation was deleted from
   `api-gateway` entirely (Phase 1); the `ws.Hub` dependency moved
   out of the gateway — the hub lives only in the `ws-hub` service.

## 14. WebSocket Events

**Status: `ws-hub` + producer POST path + `GET /ws` client relay
`[implemented]` (A3, §14.4.1); `sdr.status` producer
`[implemented]` (§14.4.3); frontend data wiring `[implemented]`
(§14.4.2); the `/ws/audio` relay is `[implemented]` (Phase 2) and
relays to the recorder endpoint (§10.4).**

### 14.1 Transport paths

- **Client → gateway:** `GET /ws` on `:8080` → relayed to
  `ws-hub:8081/ws` (target; §2.2).
- **Producers → hub:** `POST http://ws-hub:8081/api/events` with
  `{"type": "<event>", "payload": <json>}`. The processor's
  publisher uses a 256-slot queue and a **2 s** HTTP timeout; a
  failing hub is dropped (logged), never blocking DSP.
- **Hub → clients:** JSON text frames, one event per frame.

### 14.2 Event envelope (normative)

```text{ "type": "<event-type>", "payload": { ... } }
```

Allowed `type` values (hub allowlist — anything else is dropped and
counted):

| Type | Payload | Meaning |
| ------ | --------- | --------- |
| `signal.new` | full Signal object (§12.7) | first sighting of a signal ID |
| `signal.update` | full Signal object | throttled refresh (§5.8) incl. `verified` flips |
| `signal.removed` | `{"id":"<uuid>"}` | TTL expiry (§11.2) — client removes from live list |
| `sdr.status` | SDRDevice object (§12.1) | device active/frequency change |
| `track.update` | `{"signalId","lat","lon","speedKmh","headingDeg","isMoving"}` | movement refresh, ≤ 1 Hz per located signal (§9.4) |
| `audio.level` | `{"signalId":"<uuid>","level":<0..1>}` | coarse post-AGC RMS level, ≤ 10 Hz, one event per actively demodulated signal (§10.6) |
| `signal.tdoa` | `{"signalId","freqHz","at","accepted","persisted","reason?","reference?","receivers":["<sdr>",…],"fix":{"lat","lng","residualNs","pairsUsed","maxBaselineM","covPosDef"}?,"locus":{"lat1","lng1","lat2","lng2"}?}` | §9.6 solve outcome per attempt: accepted fix (persisted or event-only under the §9.5 covariance gate), 2-receiver locus (persist nothing), or rejection with `reason` |
| `spectrum.frame` | `{"sdrId","freqHz","sampleRate","t","bins","df","db":[<float>…]}` | decimated per-SDR power spectrum for the dashboard spectrum/waterfall (§18), ≤ `rate_hz` frames/s per SDR (§18.1) |

Full-object payloads (not deltas) make clients stateless: apply
payload-by-`id` upsert on `new`/`update`, delete on `removed`.

### 14.3 Delivery semantics

- **Lossy by design.** Hub per-client broadcast channel is 256
  frames; a slow client has its overflow frames **dropped**
  (non-blocking send). Clients MUST tolerate gaps and
  self-heal via `GET /api/signals`.
- **No initial snapshot over WS.** On connect, clients MUST do one
  REST bootstrap (`GET /api/signals`, `GET /api/sdrs`), then apply
  the event stream. (Events emitted during bootstrap may be missed
  — the next `signal.update` throttle cycle heals the list.)
- Ordering is FIFO per client but **not** guaranteed across
  producers (processor is the only v1 producer, so in practice
  total order holds).

### 14.4 Gap fixes

1. The dead in-process `/ws` hub was removed from `api-gateway`
   (§13.2.4, Phase 1); the `GET /ws` relay to ws-hub is now
   `[implemented]` (§2.2, A3) — the `/ws/audio` relay targets the
   now-`[implemented]` recorder endpoint (§10.4).
2. Frontend wiring — `[implemented]`: `+page.svelte` bootstraps via
   `$lib/api/client.ts` (`fetchSignals`, `fetchSDRs`), opens the
   event socket through the gateway relay, dispatches
   `signal.new`/`signal.update`/`signal.removed` + `sdr.status` +
   `audio.level` (§10.6 `signalLevels` store) into the stores, and
   re-bootstraps with exponential backoff after every reconnect;
   `SignalList`/`MapView`/`SDRControl` render from the stores, and
   `SignalDetail` adds the VU meter + recorded-WAV playback (§10.4
   client note; live Opus playback is slice 7).
3. `sdr.status` producer — `[implemented]`: the processor observes
   every incoming frame per SDR (§4) and emits a **deduplicated**
   event when a device's effective state changes (first frame,
   retune, reactivation; ≥1 s spacing between repeats), and the
   sweeper flags SDRs silent for more than `SIGNAL_TTL`/2 (clamped
   5–30 s) `active = false` in `sdrs` with a matching deactivation
   event. Payloads carry the §12.1 SDRDevice fields plus `bwHz`
   from the capture config.

## 15. Hardware

**Status: simulator `[implemented]`; RTL-SDR driver
`[implemented]` (build tag; §15.3 defect fixes fixed and
hardware-validated on the bench 2026-10-04 — V1–V8 pass,
per-device §5.6 calibration applied; run log: docs/HARDWARE.md §9);
HackRF driver `[implemented]`
(build tag; compile-validated, hardware-unverified; on-hardware
validation `[planned]`, deferred until hardware is available —
checklist: docs/HARDWARE.md §8) (H1/H2); Heimdall DAQ front end
`[planned]` (H3, §15.5 — Phase 4 slice 10).**

### 15.1 Driver contract

Go interface (`sdr.SDR`):

```textOpen() error
Close() error
SetFrequency(hz uint64) error
SetSampleRate(hz uint32) error
SetGain(db float64) error
ReadIQ(buf []int16) (int, error)   // int16 count; even (I,Q pairs)
Metadata() SDRMetadata
```

`SDRMetadata`: `id`, `model`, `serial`, `freqMin`, `freqMax`,
`maxBW`, `hasTX`. A `Registry` holds devices by unique ID (insertion
order preserved). Everything downstream of `sdr-capture` sees only
this interface.

### 15.2 Capability matrix

| Driver | Build tag | Freq range | Max BW | TX | Status |
| -------- | ----------- | ------------ | -------- | ---- | -------- |
| Simulator | (none) | 24 MHz – 1.7 GHz (declared) | 10 MHz | no | `[implemented]` — the CI testbed |
| RTL-SDR (RTL2832U) | `rtlsdr` (cgo, librtlsdr) | 24 MHz – 1.7 GHz | 3.2 MHz | no | `[implemented]` — §15.3 fixes hardware-validated 2026-10-04 (docs/HARDWARE.md §9) |
| HackRF One | `hackrf` (cgo, libhackrf) | **1 MHz – 7250 MHz** (H1) | ≤ **56 MSPS** (H1; practical cap ≈ 20 MSPS) | hardware has TX; **prohibited** | `[implemented]` — compile-validated, hardware-unverified |
| Heimdall DAQ (Kraken/KerberosSDR) | (none — pure TCP client) | 24 MHz – 1.7 GHz (RTL class) | ≤ 2.56 MSPS (daq ini) | no | `[planned]` — H3, §15.5 |

**Simulator normative defaults** (testbed fixture): center set per
launch; noise amplitude 0.005; gain factor `10^(gain/40)` with
default gain 40 dB (×31.6); three signals:

| Signal | Offset | Amplitude | Modulation |
| -------- | -------- | ----------- | ------------ |
| CW tone | +100 kHz | 0.5 | none |
| FM | +500 kHz | 0.4 | dev 5 kHz, 1 kHz mod |
| AM | **−200 kHz** | 0.3 | 800 Hz mod |

Note the AM fixture sits at a **negative** offset: with today's
one-sided FFT (§5.3 gap) it is invisible — the simulator is the
canonical reproducer for the D4 fix.

### 15.3 RTL-SDR defect fixes (`[implemented]`, hardware-validated)

All four defects are fixed, compile-validated with
`go build -tags rtlsdr ./cmd/sdr-capture`, and hardware-validated on
the bench (2026-10-04; run log: docs/HARDWARE.md §9):

1. **Byte/element confusion in `ReadIQ`:** `rtlsdr_read_sync` is now
   given `len(buf)` **bytes** — the RTL2832U delivers one unsigned
   8-bit I or Q sample per byte, matching one converted sample per
   int16 slot — and each byte is scaled to full-scale int16 via
   `(b-128)<<8`, so downstream keeps dividing by 32768. The returned
   count is the converted sample count, trimmed to complete I/Q
   pairs. (Requesting `len(buf)*2` bytes would overshoot
   `MaxIQSamplesPerFrame` and mis-pair I/Q.)
2. **Device selection:** `NewRTLSDR` stores the configured
   `usb_index`; `Open` opens that index (was hardcoded 0) and resets
   the demod buffer. Device name, product and serial are queried
   for metadata without opening the device.
3. **Error swallowing:** `set_center_freq`, `set_sample_rate`,
   `set_tuner_gain` (with `set_tuner_gain_mode`; gain in tenths of
   dB; negative dB selects auto gain) and `read_sync` return values
   are checked and surfaced; `sdr-capture` aborts startup if the
   initial frequency/rate/gain applies fail.
4. **Silent gain snap** (found by the V8 check on hardware,
   docs/HARDWARE.md §5): the r82xx tuner accepts any
   `rtlsdr_set_tuner_gain` figure and silently clamps out-of-table
   requests to its maximum (~49.6 dB) — startup proceeded at half
   the configured gain while status reported the configured figure,
   a ~49 dB error in §5.6 dBm. `SetGain` now queries the tuner's
   supported gain table, rejects a request more than 3 dB from the
   nearest supported step (so `sdr-capture` aborts startup), snaps
   nearer requests to that step, and reports the applied figure —
   not the request — via `AppliedGainDB` to the §7.4 status and
   §5.6 `applied_gain_db` (cmd/rtl-calibrate computes its offset
   from the applied figure for the same reason).

The cgo bindings were rewritten against the real librtlsdr ABI
(`rtlsdr_dev_t **` out-param from `rtlsdr_open`, argument-less
`rtlsdr_get_device_count`, `rtlsdr_get_device_usb_strings`, 4-arg
`rtlsdr_read_sync`) — the previous file referenced functions that do
not exist in the library. On-hardware validation passed on the bench
2026-10-04: V1–V8 all pass and both dongles' §5.6 offsets are
measured and applied (bench procedure, checklist and run log:
docs/HARDWARE.md; tooling: cmd/rtl-list and cmd/rtl-calibrate).

### 15.4 HackRF (H1/H2) — `[implemented]`, hardware-unverified

- RX-only in this project. The driver MUST report
  `hasTX = false` and MUST NOT call `hackrf_start_tx` — **TX is
  prohibited by policy (H2), regardless of hardware capability**
  (enforced at test time by `TestHackRFH2Guard`, §17.3).
- Range cap: 1 MHz – 7250 MHz; sample rate requests > maxBW are
  clamped (same behavior as the RTL driver's `SetSampleRate`).
- Config: `driver: hackrf` with `serial` selection; everything
  else (scan loop, frame protocol, downstream) is unchanged.
- Multi-host deployments and PTP/NTP clock sync are **out of v1
  scope** (Phase 4); single-host, one or two local SDRs is the only
  supported topology.

Implementation notes (`internal/sdr/hackrf.go`, `-tags hackrf`;
compile-validated against libhackrf 2026.01 — no HackRF hardware in
CI or the dev environment):

- `NewHackRF` validates the configured `serial` against
  `hackrf_device_list` and reads board name + serial for metadata
  without opening the device; an empty `serial` opens the first
  device found.
- RX is asynchronous: the libhackrf callback widens the signed 8-bit
  stream to full-scale int16 (`v << 8`, same convention as §15.3)
  and fills a small block queue that `ReadIQ` drains, preserving the
  synchronous `sdr.SDR` interface. A retune flushes the queue (up to
  one in-flight USB transfer may still hold pre-tune samples);
  queue overflow drops the freshest block, counts the lost pairs,
  and the driver logs the loss.
- `SetGain` maps one dB figure onto VGA first (0-62 dB, 2 dB steps),
  then LNA (0-40 dB, 8 dB steps); the 14 dB amp stays off so the
  gain chain is deterministic (§5.6 calibration prerequisite).
  Negative dB is rejected — the HackRF has no auto gain mode.
- Builds: CI compile-validates `-tags hackrf` and the combined
  `-tags "rtlsdr,hackrf"` build (`libhackrf-dev`);
  `Dockerfile.sdr-capture` ships the combined build with `libhackrf0`
  at runtime; macOS dev: `make build-capture-hw`.
- On-hardware validation (sweep, 2-SDR verification, throughput near
  the 20 MSPS practical cap) remains a Phase 3 exit gate — deferred
  until hardware is available; checklist in docs/HARDWARE.md §8.

### 15.5 Heimdall DAQ front end (H3) — `[planned]`

Target: KrakenRF's open-source **HeIMDALL DAQ firmware**
(github.com/krakenrf/heimdall_daq_fw, `main @ 1efc252`, DAQ config
v7; GPL-3.0) — the stock firmware for KerberosSDR (4 ch) and
KrakenSDR (5 ch) coherent multi-channel RTL-SDR front ends. The
contract below was extracted from the `Firmware/_daq_core` sources
(`iq_header.h`/`.py`, `eth_server.h`, `iq_server.c`,
`hw_controller.py`, `daq_chain_config.ini`). Upstream is consumed
as a **protocol client only**: no code linkage, no cgo, no new
library dependency, no build tag (simulator precedent), and no new
listening ports — sdr-capture makes **outbound** TCP connections to
the DAQ host (§3.2 unchanged).

**Unit model (normative).** One coherent DAQ unit (shared LO and
clock, N receiver channels) maps to **N logical SDRs** — one
`sdrs[]` row and one frame `sdr_id` per channel (`kraken-0` …
`kraken-4`), grouped by a shared `unit` key. The §4 wire model, §12
`sdrs` rows, per-channel §5.6 calibration and §9 placement are
unchanged. Consequences:

- **One connection pair per unit:** one IQ connection (TCP 5000)
  and one control connection (TCP 5001); the driver de-interleaves
  frames per channel and fans them out under each row's `sdr_id`
  (multi-SDR fan-out is rehearsed by the two-simulator fixture,
  §16.1).
- **Tuning and gain are unit-wide** (a single `FREQ`/`GAIN`
  command on 5001 reaches every channel): exactly one member of a
  unit — deterministically the lowest `channel` — owns the §7.1
  scan loop and receives §7.4 `frequency`/`gain` commands; the
  other members MUST NOT issue duplicate retunes. A unit-wide
  retune refreshes every member's §7.4 status row.
- **Same-unit verification is suppressed:** §8 MUST NOT verify two
  channels of one unit against each other (shared LO and clock
  make a pass vacuous). The processor needs the grouping, so the
  driver MUST expose `unit` in the §7.4 status payload.
- **Placement:** every channel row carries the array-center
  lat/lon; individual array elements have no independent
  coordinates in v1.

**Interface contract (normative).**

IQ data — `iq_server` on TCP 5000:

- The client connects and sends the ASCII token `streaming`; the
  server then pushes frames.
- **Stop-and-wait:** after each frame the server blocks; the
  client MUST send the ASCII token `IQDownload` to fetch the next
  frame. Any other command closes the connection. One client at a
  time.
- Frame = 1024-byte little-endian header + payload, beginning with
  sync word `0x2bf7b95a`. Fields the driver consumes:
  `frame_type` (0 data / 1 dummy / 2 ramp / 3 cal / 4 trig-wait),
  `active_ant_chs`, `rf_center_freq` (u64 Hz), `sampling_freq`
  (u64 Hz, post-decimation), `cpi_length` (samples per channel),
  `time_stamp` (u64 Unix seconds), `cpi_index`, `data_type`
  (0 int8 / 1 int16 / 2 float32), `if_gains[32]` (dB×10 per
  channel), `delay_sync_flag`, `sync_state`.
- Payload = `cpi_length × active_ant_chs` complex samples,
  **channel-major**, interleaved I/Q; the eth server serves CF32
  (data_type 2). The default kraken CPI is 2^20 pairs per channel.

Control — `hw_controller` on TCP 5001: 128-byte frames, 4-byte
ASCII command + 124-byte parameter area; every processed command
answers `FNSD` plus zero padding.

| Command | Parameter | Effect |
| --------- | ----------- | -------- |
| `FREQ` | u64 Hz | unit-wide retune |
| `GAIN` | N × u32 dB×10 | per-channel gains |
| `AGC` +space | none | auto gain — rejected (§5.6 needs fixed gain) |
| `STHU` | float32 | squelch threshold — unmapped in v1 |
| `INIT` | none | (re)initialize the DAQ chain |
| `EXIT` | none | server exits |

**Driver behavior (normative).**

- Accept only `frame_type` 0 (DATA) payloads; DUMMY/RAMP/CAL/TRIGW
  frames (noise-source calibration bursts and self-tests) MUST be
  dropped and counted (§4.3-style drop counter).
- Stamp each §4 frame's `freq_hz`, `sample_rate` and `timestamp`
  from the IQ header's `rf_center_freq`, `sampling_freq` and
  `time_stamp` — the header is authoritative per frame, so samples
  in flight during a retune are never mislabeled (§4.2).
- Convert CF32 → int16 with the §15.3 full-scale convention
  (`x·32768`, clamped) and re-chunk to ≤ 1024-pair §4 frames; a
  full CPI becomes 1024 frames per channel.
- Report applied gain honestly from `if_gains[]` (dB×10) via
  `AppliedGainDB`: upstream snaps requests onto the R820T gain
  table (~29 steps, 0–49.6 dB) — the same discipline as §15.3
  fix 4 and §5.6. Negative gain is rejected (no auto-gain mode);
  `AGC` — whose wire token carries a trailing space — stays
  unmapped.
- The kit is receive-only: report `HasTX = false` (H2 holds
  trivially).
- Surface `delay_sync_flag`/`sync_state` in §7.4 status as
  coherence health (informational in v1).

Out of v1 scope: bias-tee and noise-source control, DAQ ini
management, multi-unit arbitration beyond independent units, and
**coherent AOA/DF** on the calibrated array (the real prize —
future work building on §9.5; explicitly not a driver feature).

**Deployment & sizing.** The DAQ firmware runs on its own host
beside the receivers (RPi 4/5 or x86); sdr-capture stays on the
workbench host. This is the **bounded exception** to §1's
multi-host deferral: one remote IQ front end over plain TCP with
§4.2-coarse timestamps. Generic multi-host capture and NTP/PTP
sync-quality reporting remain Phase 4 slice 6 (§17.4) — this
driver does not enable them. Throughput: 2.4 MSPS × 5 channels ⇒
~11 700 §4 frames/s after re-chunking, far above the ~3 500 pkt/s
at which the macOS Docker UDP forwarder drops (docs/HARDWARE.md
§4) — such deployments MUST cut channel count or sample rate or
use the native ingest path (§17.1); the upstream `cpi_size` does
not change the §4 frame rate (it only sizes upstream frames,
which are re-chunked anyway). CI never needs a DAQ: the §17.3
obligations run against an in-process fake server; on the bench,
upstream's synthetic mode (`daq_synthetic_start.sh`) exercises
the same wire protocol without antennas. Implementation touches
at slice time: `internal/sdr/heimdall.go` (codec + client) and
the driver switch in `cmd/sdr-capture` plus
`internal/sdr/config.go` and `internal/settings/{schema,validate}.go`
(all of which reject `heimdall` today — §16.1); the bench
validation checklist lands in docs/HARDWARE.md with the driver
(HackRF precedent, §15.4).

## 16. Configuration Reference

**Status: the YAML reference is normative and loaded — iq-ingest,
signal-processor, and the recorder read their files with flag/env
overrides (§16.1); `fft.*` is wired (§5.7).**

### 16.1 Config surface — `[implemented]`

Runtime configuration is a **layered surface** (flag > env > YAML >
built-in default):

| Service | Reads |
| --------- | ------- |
| sdr-capture | `config/sdr-capture.yaml` (via `-config`), flags `-sim/-freq/-gain/-listen` |
| iq-ingest | `config/iq-ingest.yaml` (`-config`/`CONFIG`); env `LISTEN_PORT`, `CONSUMERS` and flags `-port/-consumers` override |
| signal-processor | `config/signal-processor.yaml` + `config/classifier.yaml` (`-processor-config`/`-classifier-config`); flags `-port/-threshold/-max-peaks/-model` and env `SIGNAL_TTL`, `SDR_CONFIG`, `MODEL_PATH`, `WS_HUB_URL`, `CAPTURE_API_HOST` (§5.6 polling) override |
| recorder | `config/recorder.yaml` (`-config`); flags `-port/-ws-port/-dir` and env `RECORDER_UDP_PORT`, `RECORDER_WS_PORT`, `RECORDER_TFR_PORT`, `RECORDINGS_DIR`, `WS_HUB_URL` (§10.6 `audio.level` publisher; unset = disabled) override |
| api-gateway | env `RECORDINGS_DIR`, `ALLOWED_ORIGINS` (CORS allowlist, §17.2), `WS_HUB_ADDR` (`/ws` relay, §2.2), `RECORDER_WS_ADDR` (`/ws/audio` relay, §10.4), `CAPTURE_CTRL_ADDR` (control proxy, §7.4), `CONFIG_DIR` (writable config dir backing the setup screen, §20) |

**Target:** the YAML files in `config/` are the **single source of
truth**; each service loads its own file (flags/env remain as
overrides for dev use). The defaults in §16.2–16.5 remain normative
and the YAML documents them.

Known YAML-vs-behavior conflicts:

- `classifier.yaml` `onnx.min_confidence: 0.5` — **enforced**
  (`[implemented]`): an ONNX result below the threshold falls back to
  the rules classification (whose `method` stays `rules`).
- `classifier.yaml` `rules.enabled` / `onnx.enabled` — resolved (A2):
  both are honored. `rules.enabled: false` classifies peaks Unknown
  unless ONNX refines them; `onnx.enabled: false` makes the YAML
  `model_path` inert unless `-model`/`MODEL_PATH` names one explicitly.
- `recorder.yaml` `audio.format` / `iq.format` — resolved (A14): the
  decorative `format` keys are removed — the recorder only ever writes
  WAV (§10.5) and raw int16 IQ.
- `iq-ingest.yaml` `buffer_size` — resolved (A2): removed; the
  256-frame receiver buffer it claimed to size never existed.
  `stats.interval_s` is wired (default 5 s, floored at 1 s).
- `signal-processor.yaml` `fft.*` — resolved (§5.7): `size` assembles
  the FFT record from ≤ 1024-pair wire frames; `window` ships as
  `rectangular` for ONNX-model parity.
- `signal-processor.yaml` `scan:` — resolved (A8): removed; the D3
  scan loop is configured in `sdr-capture.yaml` (§7.1), the processor
  never read this block.
- `signal-processor.yaml` `spectrum.*` — resolved (Phase 4 slice 9,
  §18.4): `enabled`/`bins`/`rate_hz` drive the §18 dashboard spectrum
  tap.
- `sdr-capture.yaml` `driver: simulator` — resolved (Phase 3,
  slice 0): the loader accepts `simulator`; the two-device dev
  fixture `config/sdr-capture.sim.yaml` shares one ingest port
  (§16.4).
- `sdr-capture.yaml` `driver: heimdall` — `[planned]` (§15.5, H3):
  the config loader and the settings schema/validator reject the
  value until the Heimdall slice lands.

### 16.2 `sdr-capture.yaml` (loaded)

```textapi_port       int      control-API port of this capture instance (the -listen
                         flag, default 9090); consumed by signal-processor's §5.6
                         gain polling (GET /api/v1/status every 5 s); unset = off

sdrs[]:
  id            string   required, unique; frame sdr_id + DB row
  driver        string   rtlsdr | hackrf | simulator(-sim flag);
                         heimdall [planned] (§15.5)
  usb_index     int      (rtlsdr)
  serial        string   (hackrf)
  unit          string   (heimdall) groups channels of one coherent
                         DAQ unit — tuning is unit-wide (§15.5)
  channel       int      (heimdall) DAQ channel index of this row
  host          string   (heimdall) DAQ host for both TCP links
  ctrl_port     int      (heimdall) control port, default 5001;
                         IQ rides stream_port (default 5000)
  default_freq  uint64   Hz
  default_gain  float    dB
  default_bw    uint32   Hz (clamped to driver maxBW)
  mode          string   scanner | monitor | both
  stream_host   string   default localhost
  stream_port   int      default 9000 + index
  lat, lon      float    optional — single-receiver location (§9.2)
  calibration_offset_db float  optional — PRESENCE marks the SDR calibrated;
                         power_dbm = power_db − gain + offset (§5.6)
```

### 16.3 `signal-processor` (flags/env)

| Key | Source | Default | Meaning |
| ----- | -------- | --------- | --------- |
| listen UDP port | `-port` | 9010 | IQ input |
| `ThresholdDB` | `-threshold` | −60 | peak threshold (§5.4) |
| `TopN` | `-max-peaks` | 20 | peak cap (§5.4) |
| `SIGNAL_TTL` | env | 30 s | retirement TTL (§11.2) |
| `SDR_CONFIG` | env | — | path to sdr-capture.yaml for location lookup |
| `MODEL_PATH` | env/`-model` | — (rules only) | ONNX model (onnx tag only) |
| `WS_HUB_URL` | env | — (no event publish if unset) | e.g. `http://ws-hub:8081` |
| `DB_URL` | env | — (no persistence if unset) | e.g. `postgres://sdr:sdr@db:5432/sdr?sslmode=disable` |
| MinSpacing | (code) | 10 | fixed in v1 |

### 16.4 `iq-ingest` (env)

| Key | Default | Meaning |
| ----- | --------- | --------- |
| `LISTEN_PORT` | 9000 | UDP receive port. **One port serves all SDRs** — frames carry `sdr_id`, so capture devices may point `stream_port` at the same ingest port (`config/sdr-capture.sim.yaml` does this); a per-device port list remains unnecessary in v1 |
| `CONSUMERS` | `signal-processor:9010` | csv of fan-out targets; add `recorder:9011` for audio demod/recording (compose default: both) |

Stats log interval: `stats.interval_s` (default 5 s, floored at 1 s;
§20). The old `buffer_size` knob is gone — the 256-frame receiver
buffer it pretended to size never existed (A2).

### 16.5 `recorder.yaml`

```textrecordings_dir        string  /recordings
audio.sample_rate     int     48000        (48 kHz only — startup-validated, §10.2)
audio.channels        int     1
iq.enabled            bool    true         (raw int16 interleaved, §10.5)
iq.max_duration_s     int     300          (session cap, §11.1; <0 uncapped)
tfr.enabled           bool    false        (§19 TF analysis; 404 when off)
tfr.max_span_s        int     30           (max requestable span, §19.3)
tfr.max_nfft          int     16384        (per-request nfft cap, §19.2)
tfr.listen_port       int     9013         (internal TFR API, §19.3)
retention.max_age_days int    30
retention.max_size_gb  int    50
capture.close_silence_s int   10           (§10.2 session hysteresis)
capture.max_concurrent  int   8            (§10.2 simultaneous sessions)
stream.listen_port     int    9012         (internal /ws/audio WS, §10.4)
stream.bitrate_bps     int    24000        (Opus CBR, mono, 20 ms frames)
```

### 16.6 Environment (`docker-compose` / `.env`)

| Var | Used by | Meaning |
| ----- | --------- | --------- |
| `POSTGRES_USER/PASSWORD/DB` | db | credentials (container) |
| `DB_URL` | processor, gateway, (recorder) | `postgres://sdr:sdr@db:5432/sdr?sslmode=disable` (compose default); host-side tooling uses `@localhost:5432` |
| `IQ_INGEST_UDP_PORT` | iq-ingest | 9000 (one port serves all SDRs, §16.4) |
| `RECORDER_UDP_PORT` | recorder | 9011 (UDP IQ in) |
| `RECORDER_WS_PORT` | recorder | 9012 (`/ws/audio` WS, §10.4) |
| `RECORDER_TFR_PORT` | recorder | 9013 (internal TFR API override, §19.3) |
| `RECORDER_BIND_HOST` | recorder | bind host for the :9012/:9013 TCP servers (default all interfaces for Docker; macOS bench sets `127.0.0.1`, HARDWARE.md §4) |
| `RECORDER_WS_ADDR` | api-gateway | `/ws/audio` relay target (§10.4); compose default `recorder:9012`, macOS bench `host.docker.internal:9012` |
| `RECORDER_API_ADDR` | api-gateway | `recorder:9013` (TFR proxy target, §19.3); macOS bench `host.docker.internal:9013` |
| `CAPTURE_API_HOST` | signal-processor | capture control-API host for §5.6 gain polling (`sdr-capture` default; macOS dev: `host.docker.internal`) |
| `API_GATEWAY_PORT` | api-gateway | 8080 |
| `TILE_SERVER_PORT` | tiles | 8082 |
| `VITE_API_URL` / `VITE_WS_URL` / `VITE_TILE_URL` | frontend build | gateway `:8080` (target, `/ws` relay), tiles `:8082` |
| `LOG_LEVEL` | all | zerolog level |

Per-device capture stream targets are YAML, not env
(`sdr-capture.yaml` `stream_host`/`stream_port`, §16.2). The retired
stub vars `CLASSIFIER_PORT`/`LOCATION_SERVICE_PORT` and the
never-consumed `SDR_CAPTURE_UDP_PORT_*`, `SIGNAL_PROCESSOR_PORT` and
`WS_HUB_PORT` are gone from `.env.example` (A4).

## 17. Non-Functional Requirements, Security & Quality

**Status: requirements-only — performance, security and quality
obligations over the contracts in §§4–16; each item is enforced
or measured where tagged. No single implementation contract.**

### 17.1 Performance & reliability (NFR)

- **Throughput (SHOULD):** 2 SDRs at 2.4 MSPS ⇒ ~2 300 UDP
  datagrams/s per SDR (1024 pairs/frame). Each consumer MUST sustain
  this without sustained queue growth; the processor's per-frame
  budget (FFT + peaks + features) is the critical path.
- **Event rate (MUST stay bounded):** ≤ ~20 events/s after the
  §5.8 throttle, regardless of peak density.
- **Degradation (MUST):** every external dependency is optional at
  runtime — no `DB_URL` ⇒ log-only persistence; no
  `WS_HUB_URL` ⇒ no events; hub down ⇒ publisher drops with a log;
  ONNX lib/model absent ⇒ rules. The pipeline never hard-depends on
  any single service.
- **Loss tolerance:** UDP IQ loss degrades peak quality, never
  correctness (frames are independent, §4.3). WS event loss is
  healed by REST bootstrap (§14.3).
- **Restarts:** all compose services use
  `restart: unless-stopped`; state that must survive restarts lives
  in Postgres or on disk (`recordings/`), never in process memory.
- **Latency (SHOULD):** capture → dashboard event < 1 s under
  nominal load (frame assembly ≤ ~42 ms at 2.4 MSPS + throttle ≤ 2 s
  in the worst case).

### 17.2 Security model

- **Trust domain:** single host / trusted LAN (v1). No
  authentication, no TLS — accepted risk, documented.
- **Exposed surface (A3):** `8080`, `8082`, `3000` (TCP) and UDP
  `9000`/`9011`. DB and hub are additionally published on
  `127.0.0.1` only — loopback dev-bench exceptions (§3.2); capture
  control and the recorder's TCP surfaces stay network-internal.
- **`[implemented]` CORS:** the gateway allowlists origins via
  `ALLOWED_ORIGINS` (default: the frontend origins
  `http://localhost:3000` / `http://localhost:5173`; set-but-empty
  denies all cross-origin — same-origin deployments behind the nginx
  proxy need no exceptions). The `*` wildcard is gone (Phase 1).
- **`[implemented]` WS origin check:** `ws-hub`'s `CheckOrigin`
  allows only same-origin or `ALLOWED_ORIGINS`-listed origins (and
  no-`Origin` non-browser clients) — aligned with the CORS decision
  (Phase 1; the gateway's in-process hub is gone).
- **Path safety:** recording file serving is confined to
  `RECORDINGS_DIR` (prefix check) — keep this invariant.
- **TX prohibition (H2):** no driver may transmit; enforced by
  `hasTX = false` metadata plus `TestHackRFH2Guard`, which fails if
  any HackRF driver source references the transmit API
  (`hackrf_start_tx`, §15.4).
- **Secrets:** Postgres credentials via `.env` (never committed);
  `gitignore` covers `.env` and `recordings/` content.
- **Legal:** operators are responsible for local frequency-monitoring
  regulations; the software is passive RX only.

### 17.3 Testing & CI

**Go tests (`make test`, ~40 tests, no hardware needed):**

| Area | Coverage |
| ------ | ---------- |
| `internal/sdr` | frame encode/decode round-trip, validation rejects (bad magic, truncated, oversize), simulator signal generation |
| `internal/dsp` | FFT bin placement, peak detector (threshold/spacing/TopN), bandwidth walk, noise floor, band table, AGC |
| `internal/classify` | rule table (all rows), feature-vector layout (134 dims, log2-kHz dim 0), ONNX E2E (real inference on the committed model) |
| `internal/location` | verifier decision table (§8.2), UUIDv5 stability, track movement |
| `internal/db` | migrations + upsert idempotency (Postgres via testcontainer/local); `TEST_DATABASE_URL`-gated integration suite (TTL deactivate/reactivate, `GetSignals(active)` filtering, SDR activation, verifications + verified latch, recordings/retention purge). **First live run green (2026-10-03)** — see §17.3 below |
| `internal/ws` | hub subscribe/broadcast, allowlist drop |

**ONNX real-inference guard:** the CI `go` job downloads ORT
1.29.0 and runs the tagged E2E test; a grep guard fails the build if
the ONNX test is skipped silently.

**Smoke test (`scripts/smoke-test.sh`, `make smoke-onnx`):** builds
`cmd/smoke-frames`, starts the real `signal-processor` binary, sends
5 CW frames (16.000 MHz on a 14.5 MHz center) and 5 WFM frames
(100.800 MHz on 100 MHz), and greps the log for the expected
`SIGNAL` lines. This is the end-to-end proof: UDP → FFT → peak →
features → ONNX → log.

**DB integration suite (live database, gated) — first live run green
(2026-10-03):** `internal/db/queries_integration_test.go` is skipped
unless `TEST_DATABASE_URL` is set, and it truncates its tables —
point it at a disposable database only. All 6 `TestIntegration*` tests
pass against a fresh `postgis/postgis:16-3.4` container (they caught
two real bugs now fixed: `ST_Y/ST_X(geography)` have no overloads in
PostGIS 3.4 — queries cast `location::geometry`; and `InsertRecording`
sent `""`/NULL for `id` instead of letting `gen_random_uuid()` fire).
Because compose keeps `db` internal-only (§3.2/A3), the host-run
suite needs a temporary bridge, e.g.:
`docker run --rm -d --name sdr-db-forward --network
sigint-workbench_sdr-net -p 127.0.0.1:5432:5432 alpine/socat
tcp-listen:5432,fork,reuseaddr tcp:db:5432`, then:

```text
TEST_DATABASE_URL='postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable' \
go test ./internal/db/ -run TestIntegration -v
```

`make db-migrate` (psql via `docker compose exec`) still works without
any published port. A stale `db-data` volume predating the current
`init.sql` must be reset (`docker compose down -v`) before the first
run — init scripts execute only on first volume init.

**CI matrix (`.github/workflows/ci.yml`, 4 jobs):**

| Job | Runs |
| ----- | ------ |
| `go` | `go vet`, `go test ./...` (with ORT 1.29.0, git-lfs for the model), onnx real-inference guard |
| `frontend` | `svelte-check` (type check) |
| `markdown` | `markdownlint-cli2` over all `*.md` (this document included) |
| `docker-scan` | Trivy: frontend image CRITICAL+HIGH, other images CRITICAL, `--ignore-unfixed` |

**Frontend tests:** `vitest` (`npm test` in `frontend/`) for
stores and the API client; `svelte-check` for types.

**Per-feature test obligations (normative for `[planned]` work):**

- D4 (§5.3): a below-center simulator fixture MUST fail before the
  fix and pass after; smoke-frames gains a negative-offset case.
- A1 (§9.3): an SDR with null lat/lon MUST produce an API-visible
  signal without a map location.
- §8: two synthetic SDR streams (S1/S2, same peak, independent
  clocks) MUST flip `verified` and write a `verifications` row.
- §10.4: an Opus frame-counter test (20 ms cadence, TOC byte 0xF8
  for mono 20 ms) on the recorder's WS — **delivered**: encoder
  contract test + end-to-end `TestWSAudioOpusFrameCounter` over the
  real libopus path (`-tags opus`, CI-guarded). Browser-side, the
  §10.4 client framing + state machine are covered by the `liveOpus`
  vitest suite (FakeWebSocket + stub decoder, slice 7).
- §10.5/§11.1 (slice 7): raw-IQ writer + max-duration cap —
  **delivered**: `TestSessionRawIQ` (byte-exact int16-LE interleave,
  capture-rate iq row), enabled/disabled file-shape tests, and
  `TestRecorderMaxDurationCap` + `TestRecorderCapDisabled`.
- §7.4/§13.1 (slice 8): the dashboard retune client path —
  `retuneSdr` PUTs `{freqHz}` to `/api/sdrs/{id}` and maps the
  returned device row — covered by the vitest API-client suite.
- §18 (slice 9): the spectrum tap — Go tests for max-pool
  decimation (group count, ordering, per-group max), the per-SDR
  rate cap, `spectrum.enabled: false` silence, and §18.2 payload
  validity; publisher-queue overflow MUST NOT block DSP (§14.1).
  Frontend: `spectrum` store upsert/eviction, gap + out-of-order +
  malformed-frame tolerance, `SpectrumView` canvas smoke (vitest).
- §19 (after §18): TF analysis — Go tests: `reassigned` on a linear
  chirp MUST place its ridge on the instantaneous frequency within
  tolerance; two-tone WVD vs `spwvd` cross-term suppression;
  gaussian-window STFT identity with the Gabor transform; API
  contract per §19.3 (400/404/413, `tfr.enabled: false` ⇒ 404) and
  single-ingress proxy compliance (§2.2, A3). Frontend: method
  picker/param-panel smoke (vitest).
- D6 (§11.2): TTL test asserting `active=false` + row survives;
  purge test for the 30-day/50 GB caps.
- H3 (§15.5, at slice time): IQ-header codec table tests (field
  round-trip, sync word), CF32→int16 full-scale conversion,
  channel de-interleave into per-sdr_id §4 frames, and an
  in-process fake `iq_server` + `hw_controller` integration test —
  `streaming` handshake, `IQDownload` stop-and-wait, `FNSD` ack
  parsing, DATA-only filtering with drop counters, and the
  one-retune-per-unit rule. Hardware-free; no new CI job.

### 17.4 Roadmap (phased)

| Phase | Scope | Exit criteria |
| ------- | ------- | --------------- |
| **0 — Core pipeline** | capture → ingest → DSP → classify → persist → events; dashboard shell; CI | smoke-onnx green; this spec written |
| **1 — Correctness** | D4 negative offsets; A1 unlocated signals; §6.5 class enum; dead `/ws` hub removal; FLAC-claim cleanup (code + README); CORS/origin tightening — all **done** | new tests per §17.3 green; docs match behavior |
| **2 — Features** | §15.3 RTL-SDR defect fixes; §10.1 real SSB + pair-aware registry; §11.2 active/TTL lifecycle; `sdr.status` producer (§14.4.3); `GET /ws` gateway relay (§2.2, A3); frontend data wiring (§14.4.2); YAML config loading + `min_confidence` enforcement (§16.1); **slices 1–3:** D3 scan loop + §7.4 control status; §8 dual-SDR verification with verified latch; recorder (D1 in-band WAV + §11.3 retention); first live `TEST_DATABASE_URL` integration run (§17.3); **slice 4:** Opus live streaming recorder side (D1b, §10.3–§10.4: per-signal mux + `/ws/audio` server + `Dockerfile.recorder`); **slice 5:** `/ws/audio` gateway relay (§2.2, §10.4); control-API proxy — `PUT /api/sdrs/{id}` retune forwarding + `GET /api/sdrs/{id}/status` (§7.4, §13.1, §13.2.3) — **all delivered** | §17.3 obligations green; dashboard live end-to-end |
| **3 — Hardware & fidelity** | RTL-SDR on-hardware validation (§15.3 defect fixes delivered in Phase 2); HackRF driver (H1/H2) — **delivered, compile-validated** (§15.4); power calibration contract (§5.6) — **delivered** (contract + mechanism + honesty flag; measuring each SDR's physical offset → docs/HARDWARE.md runbook, slice 3); **slice 0:** multi-SDR sim enablement — `driver: simulator` accepted via YAML + two-device shared-ingest-port rehearsal (§16.1, §16.4) — **delivered**; `min_confidence` enforcement (§16.1) — **delivered in Phase 2**; **slice 3:** RTL-SDR on-hardware validation runbook + calibration tooling — docs/HARDWARE.md, cmd/rtl-list, cmd/rtl-calibrate (§15.3, §5.6) — **delivered and executed 2026-10-04** (V1–V8 pass, offsets applied); **fft fidelity:** §5.7 `fft.size`/`fft.window` wired end-to-end — signal-processor assembles 4096-pair records, rtl-calibrate `-fft-size`, ONNX inference reachable on the native bench (`make ort-lib`, `-tags onnx`) — **delivered 2026-10-04** (offsets recalibrated at the 4096 geometry per §6.3; §8 session 2) | 2 real SDRs verified end-to-end; calibration documented — **met 2026-10-04** (RTL-SDR half; HackRF deferred, no hardware) |
| **4 — Deferred** | **in progress** — **slice 0:** D2 stub removal (`cmd/classifier`, `cmd/location-service`, compose entries; `Dockerfile.classifier` builds signal-processor only) — **delivered**; **slice 1:** annotations — `GET/POST /api/signals/{id}/annotations` + SignalDetail notes UI — **delivered**; **slice 2:** `audio.level` coarse feed recorder → hub → frontend — **delivered** (scope settled: one event per actively demodulated §10.2 session); **slice 3:** tracking — populate `tracks` from consecutive placements (§9.4, §12.4) — **delivered** (1 Hz persist + `track.update`, final row on TTL sweep, `GET /api/signals/{id}/track`, SignalDetail speed/heading, MapView polyline); **slice 4:** TDOA design — normative §9.5 + §4 frame v2 sample-accurate timing (design review gate) — **delivered** (review passed 2026-10-04); **slice 5:** TDOA engine — simulator first (injected offsets), then 3-SDR on-air fix — **engine + simulator, §4.5 v2 codec + dual-format consumers + per-sender gap counters, migration 004 quality columns, and §9.6 processor wiring (`signal.tdoa`, fix persistence, flip-flop guard) delivered** (on-air validation remains — runbook: docs/HARDWARE.md §7); **slice 6:** multi-host + NTP/PTP — remote capture hosts, sync-quality reporting (§16); **slice 7: audio parity — §10.5 raw-IQ writer (`iq.enabled`), §11.1 max-duration enforcement (`iq.max_duration_s`), §10.4 live browser Opus playback (dashboard consumes `/ws/audio`) — delivered 2026-10-04**; **slice 8:** dashboard retune UI wired to the `PUT /api/sdrs/{id}` control-API proxy (§7.4, §13.1) — delivered 2026-10-04; **slice 9:** spectrum analyzer + waterfall (§18) — delivered 2026-10-04 (tap + relay + dashboard canvas; §18 status above); **slice 10:** Heimdall DAQ front end (H3, §15.5) — `[planned]`: `driver: heimdall` TCP client (IQ :5000 + control :5001), per-channel logical SDRs with unit-wide tuning/scan, same-unit verification suppression | per-slice; slices 4–5: TDOA fix on a known on-air transmitter; slice 6: second capture host with NTP/PTP sync-quality reporting; slice 7: raw-IQ + duration-cap tests, live playback on the dashboard — **met 2026-10-04** (Go + vitest gates green; browser playback unit-tested against the §10.4 contract with stub WebCodecs — not verified in a real browser session); slice 8: retune client tests — met (vitest API-client suite, svelte-check clean); slice 9: §18 tap + display with §17.3 obligations green on the dashboard; slice 10: §17.3 H3 suite green against the in-process fake DAQ; bench validation on a KrakenSDR deferred to hardware availability (checklist → docs/HARDWARE.md) |

## 18. Spectrum Analyzer & Waterfall

**Status: `[implemented]` (Phase 4 slice 9, 2026-10-04): the §18.1
tap in the signal-processor, the `spectrum.frame` relay in ws-hub
(§18.2), and the dashboard spectrum/waterfall canvas (§18.3).**

A live per-SDR power-spectrum line plus a scrolling waterfall on the
dashboard — the README's remaining `*(planned)*` feature. The hard
part already exists: signal-processor computes a full `FFTResult`
(§5.7) for every assembled record and discards it after §5.4 peak
detection. §18 is a read-only side-tap on that result; detection is
untouched.

### 18.1 Producer (signal-processor)

- **Tap point.** After peak detection on each assembled FFT record
  (§5.7). The tap MUST NOT alter detection results, thresholds, or
  timing, and MUST NOT allocate on the hot path beyond the decimated
  bin vector.
- **Decimation.** Max-pool the record's `power_db` bins (§5.6
  relative-dBFS domain) down to `spectrum.bins`: group `i` reports
  the maximum of its member bins, so narrow tones survive decimation
  (average-pooling would erase exactly the peaks a spectrum display
  exists to show). Normative constraint: `fft.size % spectrum.bins
  == 0` (defaults: 4096 / 256 = 16 bins per group).
- **Pacing.** `spectrum.rate_hz` is a cap, not a guarantee: records
  complete only once `fft.size` pairs have assembled (≈ 4 wire
  frames at the 1024-pair default), a retune discards the partial
  record (§5.7), and UDP loss inside a record delays it. A waterfall
  therefore shows real gaps at retunes — honest, not a defect.
- **Transport.** The §14.1 producer POST path: `spectrum.frame`
  shares the 256-slot queue, the 2 s timeout, and the
  drop-never-block policy. Under saturation the newest frames drop
  first — correct for a display feed (§14.3 lossy semantics: a
  dropped frame is a skipped waterfall row, and the next frame is
  fresh data). `spectrum.enabled: false` MUST yield zero events and
  zero tap allocation.

### 18.2 Event contract (normative)

```text
{"type":"spectrum.frame","payload":{
  "sdrId":"sim0","freqHz":100000000,"sampleRate":2400000,
  "t":"2026-10-04T12:00:00Z","bins":256,"df":585.9375,
  "db":[-87.3,-84.1, ...]}}
```

| Field | Meaning |
| ----- | ------- |
| `sdrId` | §12.1 device ID; one frame per SDR per tick |
| `freqHz` | SDR center frequency; the span is `freqHz ± sampleRate/2`, including the below-center wrap (§5.3, D4) |
| `sampleRate` | device sample rate; with `df` it fixes the x-axis |
| `df` | bin width `= sampleRate / fft.size` |
| `bins` | decimated bin count; MUST equal `len(db)` |
| `db[i]` | max-pooled `power_db` dBFS (§5.6 uncalibrated domain); `db[0]` is the lowest-frequency bin of the span |

Honesty: the axis is "dB (rel.)" — the §5.6 per-signal calibration
flag does not extend per-bin in v1, and values inherit the §5.7
geometry coupling (comparable only within a fixed `fft.*` geometry)
and rectangular-window leakage. Frames MUST be envelope-valid and
rate-capped per §18.1.

### 18.3 Frontend

- `spectrum` store: latest frame per SDR plus a per-SDR waterfall
  ring buffer (300 rows default, FIFO eviction). Application is
  idempotent per `t`; out-of-order, gap, and malformed frames are
  tolerated (§14.3) — a gap renders as a blank waterfall row, never
  interpolated; a malformed frame is a no-op.
- `SpectrumView.svelte`: collapsible panel on the §13.4 dashboard
  with a per-SDR selector. Plain `<canvas>` + requestAnimationFrame —
  no new dependencies. Line trace for the latest frame; the
  waterfall scrolls one row per frame. dB axis defaults to
  −100…0 dBFS with a client-side autoscale toggle (rendering details
  beyond the store contract are non-normative); frequency ticks
  derive from `freqHz ± sampleRate/2`.

### 18.4 Config (`signal-processor.yaml`)

| Key | Default | Meaning |
| --- | ------- | ------- |
| `spectrum.enabled` | `true` | §18 tap on/off (§18.1) |
| `spectrum.bins` | `256` | decimated bin count; MUST divide `fft.size` (§18.1) |
| `spectrum.rate_hz` | `5` | max frames per second per SDR (§18.1) |

## 19. Time-Frequency Analysis

**Status: `[implemented]` (Phase 4 slice 10, 2026-10-05): the
recorder engine + endpoint (§19.1–§19.3, §19.5), the gateway proxy
(§13.1), and the §19.4 inspector + §18 waterfall drag-select.
Implementation notes: responses are numeric int8-dB tiles (≤ 512×512,
max-pooled online from full-rate frames) rather than PNG; the
integer-lag SPWVD is unaliased only over ±fs/4 (folded content is
named in the §19.2 artifact note); reassignment rejects the
rectangular window (no phase gradient ⇒ 400).**

On-demand, high-resolution time-frequency renders of stored IQ —
the analysis counterpart to §18's continuous dashboard feed,
inspired by the Panoradio SDR project's tutorial on high-resolution
TF representations (wavelet, Wigner-Ville, reassignment; Kodera et
al. and Flandrin et al. are the primary references). Computation
lives in the recorder (it owns the IQ files, §10.5/§11.3) and runs
only when a client asks — nothing here is continuous.

### 19.1 Scope & boundary (normative)

- Analysis runs **only on demand** over stored recordings
  (§10.5, §11.3). It MUST NOT alter, gate, or feed the §5 detection
  pipeline, the §14 event stream, or the §18 tap; §18.1's hot-path
  rules and the §5.7 geometry contracts are untouched.
- **Boundary with §18:** §18 is the continuous, cost-bounded
  monitoring feed (decimated, ≤ rate_hz, lossy); §19 is a
  higher-resolution recomputation of a chosen span from full-rate
  IQ, paid for per request. Neither substitutes for the other, and
  §19 output MUST NOT feed the dashboard's live waterfall.
- Requests reach the recorder only through the single client
  ingress (§2.2, A3), like every other client surface (§13.1).

### 19.2 Methods (normative)

| Method | Definition | Notes |
| ------ | ---------- | ----- |
| `stft` | Spectrogram; `window` is `rectangular`, `gaussian`, or `hamming` | `gaussian` is the Gabor transform (uncertainty-bound optimal, per the reference) |
| `reassigned` | STFT followed by reassignment: each (t, f) cell moves to its phase-gradient energy centroid (Kodera/Flandrin) | sharpest of the set; smears noise-dominated regions — artifact, not a bug |
| `spwvd` | Smoothed pseudo Wigner-Ville with separable time/frequency kernels | optional in v1; suppresses WVD cross-terms at resolution cost |
| `cwt-morlet` | Continuous Morlet wavelet transform | optional in v1; multi-scale, strong on SSB voice (§10.1) per the reference |

- `nfft` MUST satisfy `256 <= nfft <= tfr.max_nfft`; overlap is
  `0 <= overlap < 1`, default `0.75`.
- `t0`/`t1` select the span within the recording; the span MUST NOT
  exceed `tfr.max_span_s` (else `413`, §19.3).
- The `window` parameter applies to `stft`/`reassigned` only;
  `reassigned` rejects `rectangular` (`400`) — the phase gradient it
  reassigns by does not exist for a rectangular window.
- Each response MUST carry metadata naming the method's dominant
  artifact (reassignment smearing, WVD cross-terms, SPWVD kernel
  smoothing) so the UI can surface it (§19.4).

### 19.3 API (normative)

`POST /api/recordings/{id}/tfr` — gateway-proxied to the recorder
(§13.1):

```json
{"method":"reassigned","t0":0.5,"t1":4.0,"nfft":1024,
 "overlap":0.75,"freqSpan":[145900000,146100000]}
```

- Response: PNG render or numeric tiles, plus metadata (method,
  span, bin geometry, dominant artifact per §19.2).
- Errors: `404` unknown recording; `400` invalid params (method,
  `nfft`, overlap, span order); `413` span exceeds
  `tfr.max_span_s`; `tfr.enabled: false` ⇒ `404` — feature absent,
  not an error state.
- Synchronous only: request cost is bounded by the §19.5 caps; no
  queueing and no background jobs in v1.

### 19.4 Frontend

- Entry points: a SignalDetail **inspect** action on a recording
  (works standalone), and — once §18 ships — a drag-select on the
  §18 waterfall/spectrum canvas for the visible span.
  **Implementation order: §18 first**; §19's best entry point is a
  §18 gesture.
- Canvas rendering of the returned image/tiles; a method +
  parameter picker that surfaces the §19.2 artifact notes beside
  the render. No new dependencies.

### 19.5 Config (`recorder.yaml`, §16.5)

| Key | Default | Meaning |
| --- | ------- | ------- |
| `tfr.enabled` | `false` | feature switch — when off, `404` (§19.3) |
| `tfr.max_span_s` | `30` | largest requestable span |
| `tfr.max_nfft` | `16384` | per-request `nfft` cap (§19.2) |

### 19.6 Exploratory (non-normative)

Reassignment-derived instantaneous-frequency estimates could
become §6 classifier features (e.g., FSK/PSK discrimination).
That path requires `models/train.py` feature parity, retraining,
and a §6.2/§6.3 recalibration — explicitly out of scope for §19 v1.
Classifier-feature evolution (including TF-derived features) is
tracked in §6.6; emitter-identity research (transient capture,
cross-receiver probes) is tracked in §6.7.

## 20. First-Run Setup Screen

**Status: `[implemented]` (2026-10-05): the /setup wizard, the six
§20.4 endpoints, the comment-preserving YAML write path with D11
restart-to-apply, and the §20.5 app_settings store (migration 005)
ship together; a drift-guard test pins the shipped config/*.yaml
to the schema.**

### 20.1 Scope & semantics

The gateway serves a browser wizard (`/setup` in the frontend) that
configures every user-facing option of the five editable YAML files
without hand editing: `sdr-capture.yaml`, `signal-processor.yaml`,
`recorder.yaml`, `classifier.yaml` and `iq-ingest.yaml` (receiver
inventory, detection/processing, recorder/TFR, classifier and
ingest knobs; the values are the §16 reference). It is
intentionally NOT a deployment editor: listen ports, dirs, URLs,
model paths and compose wiring stay in the files and `.env`
because they must change in lockstep with docker-compose and
cannot be applied from a browser.

Services read their YAML once at startup, so every save is
**restart-to-apply**: the API answer and the UI name the services
to restart (D11). Exception: §7.4 live gain/frequency and §9.3
lat/lon/active remain live-appliable through their existing
endpoints.

### 20.2 Schema is server-authoritative

`internal/settings` owns the schema — typed fields with units,
bounds, display defaults, help and warning text, optional-key
semantics, and one list field (the SDR inventory). The frontend
renders it generically and never hard-codes keys, so schema and UI
cannot drift. Display values resolve exactly the way each service
resolves them at load time (§9.6 TDOA defaults, §18.4 spectrum
resolve, §7.1 scan defaults), and the shipped `config/*.yaml` are
snapshot-tested against the schema to catch drift.

### 20.3 Write path & safety

Saves go `coerce → yaml.Node edit → re-marshal → typed
re-validation → backup → atomic rename`:

- Edits preserve mapping comments; keys the wizard creates carry
  `# Configured via the setup screen (SPEC §20).`. Rewritten list
  items (the SDR list) are regenerated without inner comments.
- The marshalled result is re-decoded into the same typed configs
  the services load and re-validated against their domain rules
  (FFT power-of-two 64-16384, §18.1 `spectrum.bins` divides
  `fft.size`, §9.6 buffer horizon covers the window, lat/lon set
  together, §5.6 calibration presence semantics), so a save can
  never produce a file a service would reject at startup. Field
  failures answer `400 {"error", "field"}`; unknown sections
  answer `404`.
- The previous file is kept as `<file>.bak`; new content lands via
  `tmp + rename` (atomic). Nothing is committed on any error.
- Presence semantics: clearing an optional string removes the key
  (`stream_format` absent = `sdr1` default; an absent
  `calibration_offset_db` marks the device uncalibrated, §5.6).

Compose mounts `./config:/config:ro` on every service and the
single writable copy (`:rw`, `CONFIG_DIR=/config`) on the gateway.
Directory mounts are required: the atomic rename changes the inode,
and per-file binds would keep serving the stale old file until
restart.

### 20.4 Endpoints

The six routes are part of the §13.1 contract (table above);
§20.3 carries their behavior. `GET /api/setup/status` fans out
concurrent ≤2 s probes of db, ws-hub, recorder and capture plus a
config-dir writability check — the one documented §13 exception to
the proxy rule, so a first-run user sees every unhealthy service
at once instead of one failure at a time. First-run state lives in
`app_settings` (migration 005; §20.5) and drives a dismissible
banner on the dashboard until the wizard is completed or
dismissed. The flag is app state — §16 remains the source of
truth for service configuration.

### 20.5 First-run state

`db.app_settings` is a tiny gateway-managed key/value store
(`key`/`value`/`updated_at`), holding the first-run flag today and
nothing else. It exists so the wizard can be completed across
browsers without adding config-file state, and it is deliberately
not part of any service YAML.

## Appendix A — Decision Register

Locked design decisions and the section carrying their normative
text. (Decision points not listed here were folded into the
relevant section during spec consolidation or were drafting
defaults — they have no separate normative surface.)

| ID | Decision | Carried in |
| ---- | ---------- | ----------- |
| D1 | Audio: hybrid — in-band capture by default (recorder demodulates peaks from the shared IQ stream, no retuning) | §10.2 |
| D1b | Optional dedicated-tune monitor per SDR; live audio = Opus binary over WS | §10.3, §10.4 |
| D2 | Topology: classifier + location-service merged into signal-processor; dead in-process WS hub removed ⇒ 6 services — **delivered 2026-10-04** (stubs + compose entries removed) | §2.1, §2.2 |
| D3 | Normative scanner loop in sdr-capture (step/dwell); 2-SDR verification asynchronous | §7, §8 |
| D4 | FFT bins above fs/2 wrapped to negative offsets (below-center blind spot) | §5.3 |
| D6 | Retention: TTL → `active=false` (no hard delete); 30-day archive purge; recordings 30 days / 50 GB | §11 |
| H1 | Hardware: RTL-SDR + simulator now; HackRF 1–7250 MHz, ≤ 56 MSPS, delivered; RTL-SDR hardware-validated, HackRF hardware-unverified | §15.1, §15.3, §15.4 |
| H2 | TX prohibited by policy; drivers report `hasTX=false` | §1.2, §15.4, §17.2 |
| A1 | Unlocated signals are listed & verified, never silently dropped (map omits them) | §9.3 |
| A3 | Single client ingress: api-gateway proxies `/ws` and `/ws/audio`; hub + recorder internal-only | §2.2, §3.2 |
| D9 | Spectrum: hub JSON events (`spectrum.frame`), max-pool decimation of the §5.7 FFT to `spectrum.bins`, ≤ `rate_hz` frames/s per SDR, client-side waterfall history, canvas rendering with no new frontend dependencies; dedicated binary WS relay kept as the documented bandwidth fallback | §18 |
| D10 | Time-frequency analysis: on-demand renders over stored IQ only (§10.5, §11.3) — never in the §5 detection path, §14 events, or the §18 feed; methods `stft`/`reassigned` (+ optional `spwvd`, `cwt-morlet`); REST via the gateway proxy (A3); synchronous, canvas rendering, no new frontend deps; implementation after §18 | §19 |
| D11 | Setup screen: browser-edited YAML with restart-to-apply semantics; schema server-authoritative in `internal/settings`; deployment wiring deliberately not exposed | §20 |
| H3 | Heimdall DAQ: coherent N-channel RTL front end consumed as one logical SDR per channel (`unit` grouping, §16.2); one IQ + one control connection per unit; unit-wide tuning with a single scan-loop owner per unit; same-unit §8 verification suppressed; RX-only kit, `hasTX=false` — `[planned]`, Phase 4 slice 10 | §15.5 |

## Appendix B — Glossary

| Term | Meaning |
| ------ | --------- |
| IQ frame | one UDP datagram per §4 (40-byte header + int16 I/Q) |
| Baseband offset | frequency relative to the tuning center (may be negative after D4) |
| Band | §5.5 table row for an absolute frequency |
| Bucket | 10 kHz frequency bucket used for signal IDs and throttling |
| Verification | §8 dual-SDR cross-check result |
| Throttle window | 2 s per (SDR, kHz bucket) event cap (§5.8) |
| Heimdall DAQ | KrakenRF coherent multi-channel RTL-SDR firmware (KrakenSDR/KerberosSDR); TCP IQ :5000 + control :5001 — `[planned]` (§15.5) |
| CPI | Coherent Processing Interval — one Heimdall frame's worth of samples per channel (§15.5) |
