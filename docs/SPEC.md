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
(D1–D8, H1–H2, A1–A6) to the sections that carry their normative text.

## 1. Purpose & Scope

**Status: `[implemented]` core pipeline; `[planned]` items as tagged.**

SIGINT Workbench is a single-host, multi-service SDR monitoring pipeline.
It captures raw IQ samples, detects signals in the FFT spectrum,
classifies modulation by frequency rules and (optionally) ONNX inference,
attaches location from per-SDR coordinates, streams events to a web
dashboard, and (planned) records decoded audio.

### In scope (v1)

- Receive-only monitoring on one host (or one Docker network).
- 1–N SDR receivers; reference deployments use one or two.
- 24 MHz – 1.7 GHz (RTL-SDR class); 1 MHz – 7250 MHz when HackRF support
  lands (see §15).
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

**Status: 4 of 6 services `[implemented]`, plus the `GET /ws`
single-ingress relay (A3, §2.2); the merged classifier/location
topology (D2) and the control-path retune proxy are `[planned]`
(§13).**

### 2.1 Topology (D2)

The system consists of **six application services**:

1. `sdr-capture` — driver read loop, frame encoding, UDP transmit,
   per-device control API, scan loop.
2. `iq-ingest` — UDP receive, frame validation, fan-out to consumers.
3. `signal-processor` — DSP, classification, location, verification,
   persistence, event publishing. **Absorbs the `classifier` and
   `location-service` binaries (D2); both are now legacy stubs and are
   scheduled for removal.**
4. `recorder` — audio demodulation, recording, live audio streaming.
5. `api-gateway` — the **single client ingress (A3)**: REST API plus
   WebSocket relays for both event and audio streams.
6. `ws-hub` — internal event broadcast fan-out (not client-facing).

Infrastructure (not application services): `db` (PostGIS), `tiles`
(tileserver-gl), `frontend` (nginx-served SvelteKit SPA).

The current tree still ships 8 `cmd/` binaries. `cmd/classifier` and
`cmd/location-service` are near-empty stubs; their compose entries are
legacy. The target deployment runs exactly the six services above.

### 2.2 Single client ingress (A3)

`api-gateway` on `:8080` is the **only** HTTP/WS endpoint that clients
(browsers, scripts, the frontend) connect to. It MUST proxy:

- `GET /ws` → `ws-hub` `:8081` `/ws` (signal events), and
- `GET /ws/audio` → the recorder's internal audio WS.

`ws-hub` and the recorder MUST be reachable only from within the Docker
network. The former in-process `/ws` hub inside `api-gateway` (no
producers, dead code) was removed in Phase 1 (§13.2.4); `GET /ws`
now transparently relays to `ws-hub` `:8081/ws` (`[implemented]`,
A3): the hub is dialed before the client handshake, so an unreachable
hub answers **502 JSON** (`{"error":"ws-hub unreachable"}`), and the
same origin allowlist as ws-hub applies (foreign origins get **403**).
The `/ws/audio` relay awaits the recorder (D1b, `[planned]`).

The frontend's nginx `location /ws/ → ws-hub:8081` rule MUST be replaced
by a single `location / → api-gateway:8080` proxy after the relay lands,
so all traffic, including both WebSocket paths, enters through the
gateway.

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
  `:9090` (tune/gain/status). This path is `[planned]`; today
  `PUT /api/sdrs/{id}` only updates the database row and does not
  retune hardware (see §13).

## 3. Deployment & Ports

**Status: `[implemented]` for current compose files; the A3
published-port reduction is partially done — `db:5432` and
`ws-hub:8081` went internal-only in Phase 2; `9000/udp` (ingest),
`8080` (gateway) and `3000` (frontend) remain the exposed surface
(§17.2).**

### 3.1 Deployment modes

- **Dev (macOS host):** `sdr-capture` runs as a native binary
  (`make build-capture`) so the SDR is reachable; all other services run
  via `docker compose` (profile `dev`). The SvelteKit dev server runs
  with `npm run dev` on `:5173`.
- **Prod (Linux):** everything, including `sdr-capture`, runs in Docker
  (`--profile prod`); the SDR device is passed through into the
  `sdr-capture` container.
- The `classifier` and `location-service` compose entries are legacy
  stubs (D2); target deployments remove them.

### 3.2 Port map (target)

| Port | Transport | Service | Exposure | Status |
| ------ | ----------- | --------- | ---------- | -------- |
| 8080 | TCP | api-gateway | **published — only client ingress** | `[implemented]` |
| 8082 | TCP | tiles | published | `[implemented]` |
| 3000 | TCP | frontend (nginx) | published | `[implemented]` |
| 9000 | UDP | capture → ingest (S1) | published (host-side dev) | `[implemented]` |
| 9001 | UDP | capture → ingest (S2) | published (host-side dev) | `[implemented]` |
| 9010 | UDP | processor IQ input | internal only | `[implemented]` |
| 9011 | UDP | recorder IQ input | internal only | `[planned]` |
| 8081 | TCP | ws-hub (events) | internal only | `[implemented]` — via gateway `/ws` relay |
| 9090 | TCP | capture control API | internal only | `[implemented]` — loopback bind in dev, unpublished in compose |
| 5432 | TCP | db (PostGIS) | internal only | `[implemented]` — compose publish removed (`make db-migrate` / `exec psql` for host access) |

The target surface exposed on the host is exactly: `8080`, `8082`,
`3000`, and UDP `9000`/`9001` (needed only while capture runs on the
host in dev mode).

### 3.3 Environment

The Postgres container itself is configured with
`POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`; Go services
connect with `DB_URL` (inside the compose network the host is `db`).
Frontend build/runtime reads `VITE_API_URL`,
`VITE_TILE_URL`, `VITE_WS_URL` (the target `VITE_WS_URL` is the gateway,
`http(s)://<host>:8080`, not the hub).

## 4. IQ Wire Protocol

**Status: `[implemented]` — normative byte layout below is the exact
current behavior (`internal/sdr/protocol.go`).**

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

## 5. DSP Pipeline

**Status: `[implemented]` except §5.6 (calibration, `[planned]`),
§5.7 (FFT config, `[gap]`).**

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

### 5.6 Power values — calibration

**Gap:** all power numbers in the pipeline are **uncalibrated
relative dB** (10·log10 of squared FFT magnitude). There is no
gain chain calibration, no noise-floor subtraction, and no dBm
conversion. The API field `powerDbm` is therefore a misnomer until
calibration exists; consumers MUST treat it as a relative level, not
an absolute RF power. The roadmap (§17.4, Phase 3) defines a
calibration contract; until then the UI MUST NOT display "dBm" as an
absolute measurement.

### 5.7 Noise floor & frame-level extras

- `DetectNoiseFloor`: median of the **lower 50 %** of bins
  (selection sort; n is small).
- AGC (used for live audio level, §10.6): target peak **0.95**,
  attack **1 sample**, release **50 samples**, hard-clip at ±1.0.
- **Gap:** `config/signal-processor.yaml` documents `fft.size: 4096`
  and `fft.window: hann`, but the pipeline uses the frame size as-is
  (≤ 1024 pairs) and **no windowing**. Until wired, that YAML block is
  aspirational; the normative values are "frame length, rectangular
  window".

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

## 7. Scanning

**Status: `[planned]` (D3). Config keys exist
(`scan.step_hz`, `scan.dwell_ms`); no scan loop is implemented in
`sdr-capture` today — it holds one tuning until commanded.**

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
- With `mode: monitor` (or `both` when a monitor command is active)
  the device instead holds a fixed frequency until retuned.

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
| `GET /api/v1/status` | — | per-device: id, active, freq_hz, gain_db, sample_rate |
| `POST /api/v1/frequency` | `{"id":"S1","freq_mhz":145.5}` | retune device (pauses its scan loop) |
| `POST /api/v1/gain` | `{"id":"S1","gain_db":40}` | set LNA gain |

A manual `frequency` command **pauses** that device's scan loop until
the service restarts (v1 has no "resume scan" command; this is
documented operator behavior).

## 8. Dual-SDR Verification

**Status: verifier logic `[implemented]` (`internal/location/verify.go`);
its integration into `signal-processor` is `[planned]` (D2/D3 —
`location-service` was never wired; a `verified` flag already exists
in the schema and UI).**

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
(A1); tracking is `[planned]`; TDOA multilateration is Phase 4.**

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

### 9.4 Tracking (planned)

`Track` (implemented in `internal/location`, not yet driven):
haversine distance between consecutive fixes,
`speed = km/h`, `IsMoving` at **> 1 km/h**, heading from
`atan2(dLon, dLat)`. The `tracks` table exists; population is Phase 4
(§17.4).

## 10. Audio

**Status: demodulators (incl. SSB) + WAV encoder `[implemented]`
(library only — `cmd/recorder` is a stub); in-band capture, dedicated
monitor mode, and live Opus streaming are `[planned]` (D1/D1b).**

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

1. reads frames (§4 protocol) and inspects the processor's signal
   events (via `signal.new`/`signal.update` on `/ws`) to learn which
   signals are demodulable,
2. for each active signal, carves the in-band IQ from the shared
   stream around the detected peak (baseband shift by the peak
   offset),
3. demodulates with the registry (§10.1),
4. feeds the live Opus stream (§10.4) and the WAV writer (§10.5).

No SDR retuning ever happens for audio — the capture/scan behavior
is completely unaffected.

### 10.3 Dedicated-tune monitor mode (D1b, optional)

A device may be pinned to a frequency
(`mode: monitor`, or `POST /api/v1/frequency` for `both` devices) to
get a known-capture-frequency live audio channel — e.g. a fixed
aviation or marine frequency. The in-band path works identically;
the peak sits at DC and the baseband shift is trivial. At most the
pinned channel streams audio; this mode is an operator convenience,
not a pipeline change.

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
     `"modulation":"<FM|AM|SSB>","subType":"<WFM|NFM|USB|LSB|>","sampleRate":48000,"channels":1,"bitrate":24000}`
  2. Then **binary messages only**: each binary WebSocket message is
     **exactly one Opus packet** (TOC byte + payload, per RFC 6716).
     No length prefix, no timestamp, no extra header.
  3. Playback order = message order. Missing messages = audio gaps
     (UDP-like tolerance; no retransmission).
  4. Stream end: recorder closes the stream with close code 1000.
- The gateway relay is transparent (frames pass through untouched);
  it performs no decoding.
- One live stream per signal per client; the recorder multiplexes
  per-signal encoder state.

### 10.5 Recorded files (D1)

Per recording event the recorder writes, in parallel:

- **WAV** (always): 16-bit PCM mono, 48 kHz —
  `EncodeWAV` (RIFF, `fmt` chunk PCM/1ch/16-bit, `data` chunk,
  all little-endian).
- **Raw IQ** (optional, `recording.raw_iq: true`):
  `int16[2n]` interleaved I/Q, headerless, at the capture sample
  rate.

**FLAC is not supported and MUST NOT be claimed** (the README's
former "raw IQ + decoded audio (WAV/FLAC)" claim was removed in the
README realignment). File names:
`<signalId>-<unix-ts>.{wav,iq}` under
`recordings/<sdrId>/<YYYY-MM-DD>/`.

### 10.6 Levels

Live level metering uses the §5.7 AGC constants (target 0.95,
attack 1, release 50) on the demodulated float32 stream; the
`audio.level` WS event type is reserved in the hub allowlist for a
coarse (≤ 10 Hz) level feed and is not emitted in v1.

## 11. Recordings & Retention

**Status: sweep/TTL `[implemented]` (inactive-flag model, §11.2);
archive purge and file retention are `[planned]` (D6). Recording
trigger/writer is `[planned]` (recorder stub).**

### 11.1 Recording trigger

A recording starts when a **demodulable** signal appears
(modulation ∈ {FM, AM, SSB} with a matching registry entry) and ends
when the signal disappears (TTL, §11.2) or `recording.max_duration_s`
(default **300 s**) is reached. Each recording writes a `recordings`
row plus the files of §10.5. Multiple simultaneous signals record
independently.

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
| power_dbm | REAL | **relative dB until §5.6 calibration** |
| location | GEOGRAPHY(POINT, 4326) | NULL allowed (→ §9.3) |
| accuracy_m | REAL | 0 = exactly at SDR (§9.2) |
| first_seen / last_seen | TIMESTAMPTZ | upsert semantics: first_seen kept, last_seen refreshed |
| sdr_id | TEXT → sdrs(id) | |
| verified | BOOLEAN | default false, §8 |
| active | BOOLEAN | `[implemented]` — D6 lifecycle; partial index `idx_signals_active` (`last_seen DESC WHERE active`) |
| method | TEXT | `[implemented]` — `rules`/`onnx`; refreshed on every upsert |

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

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**; `path`
GEOGRAPHY(LINESTRING, 4326); `speed_kmh`; `heading`; `updated_at`.
GIST on `path`. Populated in Phase 4 (§9.4).

### 12.5 `annotations`

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**;
`user_note` TEXT; `created_at`. No REST surface in v1
(`[gap]` — planned endpoint, §13.1).

### 12.6 `verifications`

`id` UUID PK; `signal_id` → signals **ON DELETE CASCADE**;
`sdr1_id`, `sdr2_id` TEXT; `verified` BOOLEAN; `confidence` REAL;
`created_at`. Written by §8.

### 12.7 JSON mapping

REST and WS payloads use the **camelCase** keys of
`internal/db/models.go` (`freqHz`, `bandwidthHz`, `sdrId`,
`firstSeen`, `powerDbm`, `signalId`, …). `location` is flattened to
`lat`/`lon` (NULL when unlocated). No snake_case in any client
payload.

## 13. REST API

**Status: endpoints in §13.1 `[implemented]` including the `GET /ws`
relay (A3, Phase 2); `/ws/audio` and the control-API proxies are
`[planned]`; §13.2 lists the `[gap]` fixes to existing endpoints —
3 of 4 closed (items 1, 2, 4; item 1's `active` filter landed with
§11.2 in Phase 2; item 3 awaits the control-API proxy).**

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
| `PUT /api/sdrs/{id}` | `[implemented]` → target `[planned]` | Partial update (`model`, `serial`, `lat`, `lon`, `gainDb`, `freqHz`, `active`); **today DB-only** — target: also proxy to `sdr-capture` control API (§7.4) so `freqHz`/`gainDb` actually retune hardware |
| `GET /ws` | `[implemented]` | Transparent bidirectional relay to `ws-hub:8081/ws` (§2.2, Phase 2); the hub is dialed before the client upgrade — hub down ⇒ `502` JSON `{"error":"ws-hub unreachable"}`; foreign origins ⇒ `403` (`ALLOWED_ORIGINS`, §17.2) |
| `GET /ws/audio` | `[planned]` | Relay to recorder `:9012/ws/audio` (§10.4) |
| `GET /api/sdrs/{id}/status` | `[planned]` | Proxy of capture control `GET /api/v1/status` (per-device live state) |
| `GET/POST /api/signals/{id}/annotations` | `[planned]` | List / add notes (§12.5) |

### 13.2 Gap fixes to existing endpoints

1. **`GET /api/signals`** MUST add the `active = true` filter when
   the D6 column lands, and MUST include unlocated signals
   (`location IS NULL`) — those rows are simply without `lat`/`lon`
   (A1, §9.3) — **done** (unlocated inclusion: Phase 1; `active`
   filter: Phase 2, §11.2).
2. **`GET /api/recordings/{id}/audio`**: the `flac` content-type
   branch was removed (Phase 1 — FLAC unsupported, §10.5); `iq`
   files serve as `application/octet-stream`.
3. **`PUT /api/sdrs/{id}`**: after the control-API proxy lands, a
   `409` (or `502`) response with a clear error body is required
   when the capture service is unreachable — never silently
   DB-only. **Open** — awaits the control-API proxy (§13.1).
4. **`GET /ws`**: the in-process hub implementation was deleted from
   `api-gateway` entirely (Phase 1); the `ws.Hub` dependency moved
   out of the gateway — the hub lives only in the `ws-hub` service.

## 14. WebSocket Events

**Status: `ws-hub` + producer POST path + `GET /ws` client relay
`[implemented]` (A3, §14.4.1); `sdr.status` producer
`[implemented]` (§14.4.3); frontend data wiring `[implemented]`
(§14.4.2); the `/ws/audio` relay is `[planned]` (awaits the
recorder, D1b).**

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
| `audio.level` | `{"signalId":"<uuid>","level":<0..1>}` | reserved, not emitted in v1 |

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
   `[implemented]` (§2.2, A3) — the `/ws/audio` relay awaits the
   recorder (D1b).
2. Frontend wiring — `[implemented]`: `+page.svelte` bootstraps via
   `$lib/api/client.ts` (`fetchSignals`, `fetchSDRs`), opens the
   event socket through the gateway relay, dispatches
   `signal.new`/`signal.update`/`signal.removed` + `sdr.status`
   into the stores, and re-bootstraps with exponential backoff after
   every reconnect; `SignalList`/`MapView`/`SDRControl` render from
   the stores.
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
`[implemented]` (build tag; §15.3 defect fixes compile-validated,
on-hardware validation `[planned]`); HackRF `[planned]` (H1/H2).**

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
| RTL-SDR (RTL2832U) | `rtlsdr` (cgo, librtlsdr) | 24 MHz – 1.7 GHz | 3.2 MHz | no | `[implemented]` — §15.3 fixes compile-validated, hardware-unverified |
| HackRF One | `hackrf` (cgo, libhackrf) | **1 MHz – 7250 MHz** (H1) | ≤ **56 MSPS** (H1; practical cap ≈ 20 MSPS) | hardware has TX; **prohibited** | `[planned]` |

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

### 15.3 RTL-SDR defect fixes (`[implemented]`, hardware-unverified)

All three defects are fixed and compile-validated with
`go build -tags rtlsdr ./cmd/sdr-capture` (no hardware in CI):

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

The cgo bindings were rewritten against the real librtlsdr ABI
(`rtlsdr_dev_t **` out-param from `rtlsdr_open`, argument-less
`rtlsdr_get_device_count`, `rtlsdr_get_device_usb_strings`, 4-arg
`rtlsdr_read_sync`) — the previous file referenced functions that do
not exist in the library. On-hardware validation remains a Phase 3
gate.

### 15.4 HackRF (H1/H2) — `[planned]` contract

- RX-only in this project. The driver MUST report
  `hasTX = false` and MUST NOT call `hackrf_start_tx` — **TX is
  prohibited by policy (H2), regardless of hardware capability**.
- Range cap: 1 MHz – 7250 MHz; sample rate requests > maxBW are
  clamped (same behavior as the RTL driver's `SetSampleRate`).
- Config: `driver: hackrf` with `serial` selection; everything
  else (scan loop, frame protocol, downstream) is unchanged.
- Multi-host deployments and PTP/NTP clock sync are **out of v1
  scope** (Phase 4); single-host, one or two local SDRs is the only
  supported topology.

## 16. Configuration Reference

**Status: the YAML reference is normative and loaded — iq-ingest and
signal-processor read their files with flag/env overrides (§16.1);
the recorder (stub) and `fft.*` wiring (§5.7) remain `[planned]`.**

### 16.1 Config surface — `[implemented]` (recorder + `fft.*` remain)

Runtime configuration is a **layered surface** (flag > env > YAML >
built-in default):

| Service | Reads |
| --------- | ------- |
| sdr-capture | `config/sdr-capture.yaml` (via `-config`), flags `-sim/-freq/-gain/-listen` |
| iq-ingest | `config/iq-ingest.yaml` (`-config`/`CONFIG`); env `LISTEN_PORT`, `CONSUMERS` and flags `-port/-consumers` override |
| signal-processor | `config/signal-processor.yaml` + `config/classifier.yaml` (`-processor-config`/`-classifier-config`); flags `-port/-threshold/-max-peaks/-model` and env `SIGNAL_TTL`, `SDR_CONFIG`, `MODEL_PATH`, `WS_HUB_URL` override |
| recorder | (stub — nothing) `config/recorder.yaml` `[planned]` |
| api-gateway | env `RECORDINGS_DIR`, `ALLOWED_ORIGINS` (CORS allowlist, §17.2), `WS_HUB_ADDR` (`/ws` relay, §2.2) |

**Target:** the YAML files in `config/` are the **single source of
truth**; each service loads its own file (flags/env remain as
overrides for dev use). The defaults in §16.2–16.5 remain normative
and the YAML documents them.

Known YAML-vs-behavior conflicts:

- `classifier.yaml` `onnx.min_confidence: 0.5` — **enforced**
  (`[implemented]`): an ONNX result below the threshold falls back to
  the rules classification (whose `method` stays `rules`).
- `recorder.yaml` `audio.format` — resolved (Phase 1): `flac`
  removed; only `wav` is documented (§10.5).
- `signal-processor.yaml` `fft.*` — see §5.7 (not wired,
  `[planned]`).

### 16.2 `sdr-capture.yaml` (loaded)

```textsdrs[]:
  id            string   required, unique; frame sdr_id + DB row
  driver        string   rtlsdr | hackrf | simulator(-sim flag)
  usb_index     int      (rtlsdr)
  serial        string   (hackrf)
  default_freq  uint64   Hz
  default_gain  float    dB
  default_bw    uint32   Hz (clamped to driver maxBW)
  mode          string   scanner | monitor | both
  stream_host   string   default localhost
  stream_port   int      default 9000 + index
  lat, lon      float    optional — single-receiver location (§9.2)
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
| `LISTEN_PORT` | 9000 | UDP receive port (one per SDR; multi-port `[planned]` — today S1/S2 need two ingest instances or a port-list) |
| `CONSUMERS` | `signal-processor:9010` | csv of fan-out targets; **recorder target MUST be added** when it ships |

Stats log interval: 5 s. Buffer: 256 frames.

### 16.5 `recorder.yaml` (reference until wired)

```textrecordings_dir        string  /recordings
audio.sample_rate     int     48000
audio.format          string  wav          (wav only; FLAC unsupported, §10.5)
audio.channels        int     1
iq.enabled            bool    true         (raw int16 interleaved)
iq.max_duration_s     int     300
retention.max_age_days int    30
retention.max_size_gb  int    50
```

### 16.6 Environment (`docker-compose` / `.env`)

| Var | Used by | Meaning |
| ----- | --------- | --------- |
| `POSTGRES_USER/PASSWORD/DB` | db | credentials (container) |
| `DB_URL` | processor, gateway, (recorder) | `postgres://sdr:sdr@db:5432/sdr?sslmode=disable` (compose default); host-side tooling uses `@localhost:5432` |
| `SDR_CAPTURE_UDP_PORT_0/1` | sdr-capture (host bind) | 9000/9001 |
| `IQ_INGEST_UDP_PORT` | iq-ingest | 9000 |
| `SIGNAL_PROCESSOR_PORT` | signal-processor | 9010 |
| `RECORDER_PORT` | recorder | 9012 (WS; UDP in 9011) |
| `API_GATEWAY_PORT` / `WS_HUB_PORT` | gateway / hub | 8080 / 8081 |
| `TILE_SERVER_PORT` | tiles | 8082 |
| `VITE_API_URL` / `VITE_WS_URL` / `VITE_TILE_URL` | frontend build | gateway `:8080` (target), tiles `:8082` |
| `LOG_LEVEL` | all | zerolog level |

Legacy vars `CLASSIFIER_PORT` (9011) and
`LOCATION_SERVICE_PORT` (9013) belong to the retired stubs and are
removed from compose with the stubs (D2).

## 17. Non-Functional Requirements, Security & Quality

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
- **Exposed surface (target, A3):** only `8080`, `8082`, `3000`
  (TCP) and UDP `9000`/`9001`. DB, hub, capture control, and
  recorder are network-internal (§3.2).
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
  `hasTX = false` metadata and code review (HackRF driver MUST NOT
  link `hackrf_start_tx`).
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
| `internal/db` | migrations + upsert idempotency (Postgres via testcontainer/local); `TEST_DATABASE_URL`-gated integration suite (TTL deactivate/reactivate, `GetSignals(active)` filtering, SDR activation, verifications) |
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

**DB integration suite (live database, gated) — pending first run:**
`internal/db/queries_integration_test.go` is skipped unless
`TEST_DATABASE_URL` is set, and it truncates its tables — point it
at a disposable database only. It landed in Phase 2 with the Docker
daemon unavailable, so its live run (plus the full `docker compose
up` smoke test) is still outstanding. When Docker is back: `make
db-migrate` (runs psql via `docker compose exec`, no published port
needed), temporarily publish `db:5432` (compose keeps it
internal-only per §3.2/A3) for the host-run suite:

```text
TEST_DATABASE_URL='postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable' \
go test ./internal/db/ -run TestIntegration -v
```

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
  for mono 20 ms) on the recorder's WS.
- D6 (§11.2): TTL test asserting `active=false` + row survives;
  purge test for the 30-day/50 GB caps.

### 17.4 Roadmap (phased)

| Phase | Scope | Exit criteria |
| ------- | ------- | --------------- |
| **0 — Core pipeline** (current) | capture → ingest → DSP → classify → persist → events; dashboard shell; CI | smoke-onnx green; this spec written |
| **1 — Correctness** | D4 negative offsets; A1 unlocated signals; §6.5 class enum; dead `/ws` hub removal; FLAC-claim cleanup (code + README); CORS/origin tightening — all **done** | new tests per §17.3 green; docs match behavior |
| **2 — Features** | **Delivered in Phase 2:** §15.3 RTL-SDR defect fixes; §10.1 real SSB + pair-aware registry; §11.2 active/TTL lifecycle; `sdr.status` producer (§14.4.3); `GET /ws` gateway relay (§2.2, A3); frontend data wiring (§14.4.2); YAML config loading + `min_confidence` enforcement (§16.1). **Remaining `[planned]`:** D3 scan loop; §8 verification in processor; recorder (D1 in-band + D6 retention); Opus live streaming (D1b); `/ws/audio` relay; control-API proxy | §17.3 obligations green for delivered scope; dashboard live end-to-end |
| **3 — Hardware & fidelity** | RTL-SDR on-hardware validation (§15.3 defect fixes delivered in Phase 2); HackRF driver (H1/H2); power calibration contract (§5.6); `min_confidence` enforcement (§16.1) | 2 real SDRs verified end-to-end; calibration documented |
| **4 — Deferred** | multi-host + NTP/PTP; TDOA multilateration; tracking (`tracks`, §9.4); annotations UI; `audio.level` feed | scoped separately |

## Appendix A — Decision Register

Locked design decisions and the section carrying their normative
text. (Decision points not listed here were folded into the
relevant section during spec consolidation or were drafting
defaults — they have no separate normative surface.)

| ID | Decision | Carried in |
| ---- | ---------- | ----------- |
| D1 | Audio: hybrid — in-band capture by default (recorder demodulates peaks from the shared IQ stream, no retuning) | §10.2 |
| D1b | Optional dedicated-tune monitor per SDR; live audio = Opus binary over WS | §10.3, §10.4 |
| D2 | Topology: classifier + location-service merged into signal-processor; dead in-process WS hub removed ⇒ 6 services | §2.1, §2.2 |
| D3 | Normative scanner loop in sdr-capture (step/dwell); 2-SDR verification asynchronous | §7, §8 |
| D4 | FFT bins above fs/2 wrapped to negative offsets (below-center blind spot) | §5.3 |
| D6 | Retention: TTL → `active=false` (no hard delete); 30-day archive purge; recordings 30 days / 50 GB | §11 |
| H1 | Hardware: RTL-SDR + simulator now; HackRF 1–7250 MHz, ≤ 56 MSPS, planned | §15.1, §15.3, §15.4 |
| H2 | TX prohibited by policy; drivers report `hasTX=false` | §1.2, §15.4, §17.2 |
| A1 | Unlocated signals are listed & verified, never silently dropped (map omits them) | §9.3 |
| A3 | Single client ingress: api-gateway proxies `/ws` and `/ws/audio`; hub + recorder internal-only | §2.2, §3.2 |

## Appendix B — Glossary

| Term | Meaning |
| ------ | --------- |
| IQ frame | one UDP datagram per §4 (40-byte header + int16 I/Q) |
| Baseband offset | frequency relative to the tuning center (may be negative after D4) |
| Band | §5.5 table row for an absolute frequency |
| Bucket | 10 kHz frequency bucket used for signal IDs and throttling |
| Verification | §8 dual-SDR cross-check result |
| Throttle window | 2 s per (SDR, kHz bucket) event cap (§5.8) |
