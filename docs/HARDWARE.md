# Hardware Runbook — RTL-SDR (SPEC §15.3)

Bench procedure for running SIGINT Workbench on real RTL-SDR hardware:
identifying dongles, pinning them to config slots, validating the
on-hardware path end to end, and measuring each device's §5.6 power
calibration offset with the on-air method (no signal generator
required). The HackRF half of the Phase 3 exit criteria is deferred —
its checklist lives in §7 below.

Status (Phase 3, slice 3 — SPEC §17.4):

| Item | Status |
| --- | --- |
| RTL-SDR driver, build tag `rtlsdr` (§15.3) | `[implemented]`, hardware-validated |
| On-hardware validation (this runbook, V1–V8) | `[done]` 2026-10-04 (§8) |
| §5.6 per-device calibration measurement | `[done]` — offsets in config/sdr-capture.yaml (§6, §8) |
| HackRF on-hardware validation (§15.4) | deferred — no hardware |

Build the tools once (§1 lists the libraries they need):

```bash
make build-capture-hw   # bin/sdr-capture with rtlsdr (+ hackrf) drivers
make build-hw-tools     # bin/rtl-list + bin/rtl-calibrate
```

## 1. Prerequisites

The supported bench host is macOS (`make build-capture-hw` and
`build-hw-tools` are native darwin/arm64 targets); on Linux use the
same commands with local `GOOS` (the production image already compiles
the drivers in).

```bash
brew install librtlsdr   # Debian/Ubuntu: apt install librtlsdr-dev
```

Hardware:

- two RTL-SDR dongles (RTL2832U; R860/R820T2 tuner) with antennas
  suited to the bands under test — an FM-band whip covers §5 and §6;
- direct USB ports, not a hub — RTL-SDRs glitch on under-powered hubs;
- `jq` for eyeballing control-API JSON (optional).

## 2. Identify the dongles

`cmd/rtl-list` enumerates attached dongles without opening them:

```bash
./bin/rtl-list
```

```text
INDEX MODEL                        SERIAL
0     RTL2838UHIDIR                00000001
1     Blog V4                      00000001
```

Two gotchas:

- **USB indices are enumeration order, not identity.** A replug or
  reboot can swap index 0 and index 1. Re-run `rtl-list` after every
  hardware change and before trusting a config.
- **Serials can collide.** Cheap dongles (both of ours, above) often
  ship with the same serial `00000001`. When serials match, the
  index↔model mapping from `rtl-list` plus a physical label on each
  dongle is the only pin available — `librtlsdr` cannot select by
  serial here, and the driver keeps `usb_index` (§15.3).

(`sdr-capture -devices` prints a similar list as a startup convenience;
`rtl-list` is the lighter tool for config pinning.)

## 3. Pin devices in config

Edit `config/sdr-capture.yaml` and give each `sdrs[]` entry its own
`usb_index` (the stock config uses 0 and 1). Keep the configured
roles: `rtlsdr-0` scanner, `rtlsdr-1` monitor. Record in §8 which
serial (or, with colliding serials, which model + USB port) got which
role.

## 4. Bring up the stack

Build the hardware binaries first — note that `make dev` builds the
**untagged** `sdr-capture` (simulator-only; it aborts on
`driver: rtlsdr`), so the bench always builds and runs its own:

```bash
make build-capture-hw build-hw-tools
```

On macOS, Docker Desktop's UDP port forwarder silently drops at the
full dual-dongle rate (~3.5k pkt/s): frames never reach a
containerized iq-ingest (found 2026-10-04 — capture logged zero send
errors while the ingest container counted zero arrivals), so the UDP
pair runs natively and only TCP services stay in Docker. On Linux
(`--profile prod`) the all-Docker topology is unaffected.

```bash
# TCP services in Docker (db publishes 127.0.0.1:5432 for the native
# signal-processor; dev/bench only — §3.2 still governs production):
docker compose up -d db api-gateway ws-hub tiles
cat db/migrations/*.sql | docker compose exec -T db psql -U sdr -d sdr

# native UDP pair (loopback carries the full rate):
CONFIG=config/iq-ingest.yaml LISTEN_PORT=9000 \
  CONSUMERS=localhost:9010 ./bin/iq-ingest &
LISTEN_PORT=9010 SIGNAL_TTL=30 \
  DB_URL='postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable' \
  SDR_CONFIG=config/sdr-capture.yaml \
  PROCESSOR_CONFIG=config/signal-processor.yaml \
  CLASSIFIER_CONFIG=config/classifier.yaml ./bin/signal-processor &

# capture last, with the hardware build:
./bin/sdr-capture -config config/sdr-capture.yaml
```

**ML classification (optional):** the untagged build above falls back
to the rules classifier. To run the real ONNX model on the bench,
fetch the runtime library (dlopen'd — nothing else changes), rebuild
tagged, and export the library path before the launch line:

```bash
git lfs pull --include models/classifier.onnx   # if not local yet
make ort-lib                                    # downloads into .ort/
CGO_ENABLED=1 go build -tags onnx \
  -o bin/signal-processor ./cmd/signal-processor
eval "$(make ort-lib)"   # sets ORT_LIBRARY_PATH in this shell

ORT_LIBRARY_PATH="$ORT_LIBRARY_PATH" LISTEN_PORT=9010 SIGNAL_TTL=30 \
  DB_URL='postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable' \
  SDR_CONFIG=config/sdr-capture.yaml \
  PROCESSOR_CONFIG=config/signal-processor.yaml \
  CLASSIFIER_CONFIG=config/classifier.yaml ./bin/signal-processor &
```

The model path itself comes from `classifier.yaml`
(`models/classifier.onnx`; `-model` or `MODEL_PATH` override it).
Pass looks like a `loaded ONNX classifier …` startup line; without
the tag or library the log says the ONNX classifier is unavailable
and rules are used — that is what the 2026-10-04 V4 run did (§8).

On startup, check:

- the sdr-capture log shows one open line per device (model, freq,
  gain, BW) and no tuner errors — the §15.3 fixes surface
  `rtlsdr_set_*` failures as a startup abort instead of leaving a
  silently misconfigured device;
- both devices answer the control API (§7.4):

```bash
curl -s localhost:9090/api/v1/status | jq
```

Each entry should report `active: true` with the configured freq,
gain, and BW.

## 5. On-hardware validation checklist

Work top to bottom and record every row in §8. V5 is the headline
Phase 3 exit gate (§8: both `signals` rows flip `verified = true` and
a `verifications` row is written).

| ID | Check | Pass looks like |
| --- | --- | --- |
| V1 | enumeration | `rtl-list` shows both dongles with distinct models (or distinct ports for colliding serials) |
| V2 | startup | one open line per device in the sdr-capture log; `active: true` for both in `/api/v1/status` |
| V3 | scan sweep | `rtlsdr-0` `freq_hz` advances through its range (a full 24–1766 MHz pass at 100 kHz/50 ms takes ~15 min; spot-check ~10 retunes over a few minutes) |
| V4 | detection E2E | park `rtlsdr-1` on a strong carrier (§6.1); a `signal.update` with class and band appears on the dashboard |
| V5 | dual-SDR verification | both devices on the same carrier → within ~2 s both `signals` rows show `verified: true`; the UI badge flips to "Verified (2 SDRs)" |
| V6 | control plane | retune + gain change via the APIs below; the §5.6 gain poll follows within 5 s |
| V7 | throughput | both devices at 2.4 MSPS for 10 min: no sustained queue growth, zero drops (§17.1) |
| V8 | §15.3 regressions | the two failure injections below abort startup with clear errors |

Notes per row:

- V4/V5 carrier: an FM broadcast station works well (§6.1). For V5
  both devices must hear it — split one antenna with a 2-way coupler,
  or give each dongle its own antenna; the §8 pair tracker does not
  require identical power. The §8 latch is in-memory (first verified
  pair wins); DB rows persist across restarts.
- V5 evidence:

```bash
curl -s localhost:8080/api/signals | jq '.[] | select(.verified)'
make db-shell
```

```sql
select freq_hz, verified, sdr_id from signals order by last_seen desc;
select * from verifications order by created_at desc limit 5;
```

- V6 evidence:

```bash
# retune via the gateway (Hz; pauses that device's scan loop, §7.4)
curl -s -X PUT localhost:8080/api/sdrs/rtlsdr-1 \
  -H 'Content-Type: application/json' -d '{"freqHz": 121500000}'
# or straight to the capture control API (MHz):
curl -s -X POST localhost:9090/api/v1/frequency \
  -H 'Content-Type: application/json' \
  -d '{"id": "rtlsdr-1", "freq_mhz": 121.5}'
# change gain:
curl -s -X POST localhost:9090/api/v1/gain \
  -H 'Content-Type: application/json' \
  -d '{"id": "rtlsdr-1", "gain_db": 30}'
```

Then watch the signal-processor log: the §5.6 gain poll refreshes
`applied_gain_db` every 5 s so calibrated dBm stays honest.

- V7 evidence: watch the iq-ingest and signal-processor 5 s stats
  logs for 10 minutes. Required USB bandwidth is only
  2 × 2.4 MSPS × 2 B = 9.6 MB/s; keep both dongles on the same USB
  controller for a representative test.
- V8 failure injections (each must abort startup with a clear error,
  never continue silently):

```bash
# unsupported gain → aborts: "tuner gain 99.0 dB not supported
#   (nearest supported step 49.6 dB)" (§15.3 defect 4)
sed 's/default_gain: 40/default_gain: 99/' config/sdr-capture.yaml \
  > /tmp/badgain.yaml
./bin/sdr-capture -config /tmp/badgain.yaml
# out-of-range index → "out of range (found N devices)"
sed 's/usb_index: 0/usb_index: 7/' config/sdr-capture.yaml \
  > /tmp/oob.yaml
./bin/sdr-capture -config /tmp/oob.yaml
```

## 6. Power calibration — on-air method (§5.6)

§5.6 contract: `power_dbm = power_db − applied_gain_db +
calibration_offset_db`. The offset is per device; putting the key in
the config flips the honesty flag (`powerCalibrated`) and the UI and
logs start reporting dBm instead of relative dB.

No signal generator is used: the reference is a strong, stable,
continuous on-air carrier whose level is estimated from its licensed
ERP and free-space distance. The result is honest but not lab-grade:
**±6–10 dB**. Treat the dBm numbers as relative-accurate and
re-measure per band if cross-band comparisons matter. When a generator
becomes available, re-run §6.2 against it — the offset is a config
value, not code, so upgrading the method costs nothing.

### 6.1 Pick and estimate a reference carrier

Best reference: a local FM broadcast station (88–108 MHz) —
continuous, stable, with a published licensed ERP. Estimate the level
at your antenna:

```text
FSPL(dB) = 20·log10(d_km) + 20·log10(f_MHz) + 32.44
Pr(dBm)  ≈ ERP(dBm) − FSPL(dB) + G_ant(dBi) − L_cable(dB)
```

Worked example: a 4 kW ERP station (= 66 dBm) at 98.7 MHz, 12 km out,
indoor whip antenna (≈ −5 dBi including feed and building loss):

```text
FSPL = 21.6 + 39.9 + 32.4 = 93.9 dB
Pr   ≈ 66 − 93.9 − 5      = −32.9 dBm
```

The estimate is rough — building penetration alone is 5–20 dB — and
that uncertainty is exactly the ±6–10 dB honesty envelope above. Aim
for a carrier whose measured `power_db` sits comfortably below full
scale (no clipping) yet above the −60 dB peak threshold (§5.4), so the
live pipeline actually reports it.

### 6.2 Measure with rtl-calibrate

One run per dongle (example for index 0):

```bash
./bin/rtl-calibrate -index 0 -freq 98700000 \
  -expected-dbm -40 -gain 40 -duration 30s -fft-size 4096
```

The tool tunes 250 kHz below the carrier (`-tune-offset`) so the
carrier lands away from the RTL2832U DC spike, excludes ±20 kHz around
baseband 0 regardless, assembles reads at the pipeline's own FFT
geometry (`-fft-size`, default 4096 pairs — keep it equal to the
`fft.size` the signal-processor config sets, §5.7), and computes the
strongest-bin `power_db` exactly like signal-processor (§5.2: the same
/32768 normalization, window, and `ComputeIQFFT`), so the offset it
prints is the offset the pipeline needs with no scale conversion. It
averages per-buffer peak `power_db` over the measurement window and
solves §5.6 for the offset:

```text
calibration_offset_db = expected_dbm − mean_power_db + gain_db
```

Act on the warnings it prints:

- stddev > 3 dB — the reference moved or the front end is overloaded;
  re-run with more distance or less gain;
- mean `power_db` near full scale — clipping; reduce `-gain`;
- mean `power_db` ≤ −60 dB — below the peak threshold; the live
  pipeline would not emit this signal.

### 6.3 Record, apply, verify

1. Put the printed value in that device's `sdrs[]` entry:

   ```yaml
    calibration_offset_db: -12.5
   ```

2. Keep `default_gain` at the calibrated value. The tuner applies the
   nearest supported gain step and the sub-dB delta is absorbed into
   the offset, so the number is only valid at this gain; re-run §6.2
   if the operating gain changes. The offset is likewise valid only
   at the FFT geometry it was measured with — unnormalized spectra
   scale with `fft.size` (§5.7) — so re-run §6.2 after any `fft.*`
   change on either side.
3. Restart the capture process (§4 topology — native
   `./bin/sdr-capture`; Docker Desktop macOS UDP note applies to the
   rest of the chain) and verify end to end: the signal-processor
   log labels that SDR's power in dBm, and the API payload carries
   `"powerCalibrated": true` (§12.7).
4. Repeat for the other dongle. Record both offsets, gains, carriers
   and dates in §8, then update the SPEC (§15 status block, §15.2
   matrix row, §17.4 Phase 3 row).

## 7. HackRF checklist (deferred)

No HackRF hardware has been available; §15.4 stays compile-validated,
hardware-unverified, and on-hardware validation remains a Phase 3 exit
gate. When hardware arrives, work the V1–V8 shape above plus:

- pin by `serial` (config `serial:` field; omit to use the first
  found) — HackRF serials are unique, unlike our RTL-SDR clones;
- RX-only guard (H2): the binary must never link the transmit API
  (a guard test already enforces the link set);
- sweep + §8 verification across the wider 1 MHz–7.25 GHz range;
- throughput sustained near the 20 MSPS practical cap for ≥ 5 min
  (§15.4; the 56 MSPS absolute cap is unreachable in practice);
- calibration: same §6 procedure — the HackRF gain chain is
  deterministic (IF ≈ LNA + VGA, amp off, §15.4).

## 8. Run log

Append one block per bench session; failures feed the §15.3 defect
notes in SPEC, successes flip the §15 and §17.4 statuses.

```text
### 2026-10-04 — Mac Studio (M5 Max), macOS 26 (Darwin 27.0)

- hardware: index 0 = RTL2838UHIDIR/00000001, index 1 = Blog V4/
  00000001; serials collide as §2 predicts — pinning is usb_index
  plus physical label
- V1 pass (both enumerated); V2 pass (open lines report the snapped
  40.2 dB, both active:true, control API up); V3 pass (scanner
  384.92→404.92 MHz in 10 s = 2 MHz/s); V4 pass (classified
  signals via gateway /api/signals — rules classifier; ONNX
  runtime lib absent in the dev image, pre-existing, see notes);
  V5 pass (both dongles parked on 96.5 MHz → the §8 pair tracker
  latched verified=true from BOTH sdr_ids within ~30 s); V6 pass
  (gateway PUT → capture applies → §5.6 poll reflects the snapped
  29.7 dB applied within 5 s); V7 pass (4685–4687 pkt/s in and
  out, 0 dropped for >10 min at 2×2.4 MSPS over native loopback);
  V8 pass (gain 99 → startup abort "not supported (nearest
  supported step 49.6 dB)"; usb_index 7 → "out of range (found
  2 devices)"; both exit 1)
- defects found on the bench, all fixed this session:
  1. §15.3 defect 4 — silent tuner gain snap (driver validates
     against the gain table, reports AppliedGainDB);
  2. rtl-calibrate clipping advisory used the wrong scale
     (mean > −3 vs full scale ≈ +54 dB) — it fired on every
     healthy run; now powerAdvisories keyed to fullScaleDB;
  3. rtlsdr-1 streamed to UDP 9001 where nothing listened
     (iq-ingest binds one port) — devices share 9000, frames
     carry sdr_id;
  4. §5.6 gain poll hardcoded localhost inside the container —
     CAPTURE_API_HOST (compose default sdr-capture, macOS .env
     override), poll verified live;
  5. §4 topology: Docker Desktop's UDP forwarder drops at full
     rate (0 arrivals at the containerized ingest) — UDP pair
     runs natively, §4 rewritten with the bench commands;
  6. api-gateway kept a stale CAPTURE_CTRL_ADDR because it was
     started before .env existed — recreate with
     `docker compose up -d --no-deps api-gateway` after env
     changes.
- calibration: index 0 = −48.00 dB @ 40.2 applied (96.5 MHz FM,
  est. −35 dBm per operator, stddev 1.97 over 7031 FFT frames,
  0 short reads); index 1 = −47.66 dB @ 40.2 applied (same
  carrier, stddev 2.29 over 7027 frames, 0 short reads); the two
  front ends agree within 0.34 dB; offsets applied to
  config/sdr-capture.yaml, powerCalibrated:true verified end to
  end (§6.3)
- SPEC updates: §15 status block, §15.2 matrix row, §17.4 Phase 3
  row — pending the Stage 3 commit
```

```text
### 2026-10-04 (session 2) — Mac Studio (M5 Max), macOS 26 (Darwin 27.0)

- §5.7 fft.* wired end to end: signal-processor assembles 4096-pair
  records (rectangular window) from the 1024-pair wire frames;
  rtl-calibrate gained -fft-size (default 4096); ONNX became reachable
  on the native bench via `make ort-lib` + a -tags onnx build
  (a35aea39, b40bef96)
- recalibration at the 4096 geometry (mandatory after an fft.* change,
  §6.3): 96.5 MHz FM, est. −35 dBm, gain 40 → 40.2 applied, 30 s
  windows, 0 short reads, ~17.5k assembled FFTs per run (reads/4
  exactly — assembly working): index 0 = −55.61 dB (stddev 2.56; the
  first run's stddev 4.16 tripped the §6.2 advisory — FM program
  content, clean on re-run); index 1 = −55.68 dB (stddev 2.62); the
  front ends agree within 0.07 dB
- shift vs. the morning's 1024-pair offsets (−48.00/−47.66): −7.6 dB
  on BOTH devices — the strongest-bin power of the wideband carrier
  rises with record size, exactly the geometry coupling §5.7 documents
  (an un-recalibrated pipeline would have reported every carrier
  ~7.6 dB hot in dBm)
- offsets applied to config/sdr-capture.yaml; capture + the native
  signal-processor restarted (-tags onnx, ORT_LIBRARY_PATH from
  make ort-lib): startup shows "loaded ONNX classifier …" and
  "fft: 4096-pair buffers, rectangular window (§5.7)"
- V4 pass (gateway /api/signals: method onnx, powerCalibrated true,
  plausible dBm — closes this morning's rules-only note); V5 pass
  (both dongles parked on 96.5 via POST /api/v1/frequency →
  verified=true latched from both sdr_ids); V6 pass (gain 30 →
  29.7 applied, §5.6 poll reflected it, restored 40 → 40.2);
  V7 pass (10 min at the 4096 geometry: 120 x 5 s stats samples,
  4666–4688 pkt/s in and out, 0 dropped)
- environment note (pre-existing, not a V-check): the tiles container
  crash-loops — ./tiles/data is mounted :ro and empty, so
  tileserver-gl cannot write zurich_switzerland.mbtiles (EROFS);
  fetch the mbtiles per setup or mount rw before using the map view
```

```text
### YYYY-MM-DD — <host>, <OS>

- hardware: index 0 = <model>/<serial>, index 1 = <model>/<serial>
- V1–V8: pass/fail + notes (failures → SPEC §15.3)
- calibration: index 0 = <offset> dB @ <gain> dB (carrier <station>,
  est. <dBm>, stddev <dB>); index 1 = ...
- SPEC updates: §15 status, §15.2 matrix row, §17.4 Phase 3 row
```
