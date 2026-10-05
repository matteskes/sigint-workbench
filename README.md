# SIGINT Workbench

Real-time multi-SDR signal monitoring, classification, recording,
and geospatial visualization.

## Features

- **Dual-SDR wideband scanning** with cross-verification
- **Real-time signal detection** and classification (rules + ONNX)
- **Movement tracking** — speed, heading, and persisted track paths
  per signal (§9.4)
- **Multilateration (TDOA)** — geolocate a craft by passively
  receiving its radio emissions (e.g., ADS-B/Mode S or ACARS)
  and comparing arrival times across the SDR network; engine and
  processor wiring delivered, on-air validation pending a third
  receiver (§9.5–§9.6)
- **FM/AM/SSB voice demodulation** with live audio streaming (Opus
  over WebSocket) and in-browser playback (§10.4)
- **Signal recording** — decoded audio (WAV) + raw IQ, with a
  max-duration cap (§10.5, §11.1)
- **PostGIS-backed geospatial database** for signal locations and
  movement tracks
- **Interactive map** (MapLibre GL + self-hosted OSM tiles) with
  live signal overlay
- **Dashboard control** — retune receivers from the UI via the
  capture control API (§7.4); per-signal user notes
- **Spectrum analyzer** and **waterfall display** — live per-SDR
  spectrum trace + waterfall on canvas (§18)
- **Time-frequency analysis** — on-demand STFT/reassigned/SPWVD/
  Morlet renders per recording, inspect action + waterfall
  drag-select entry points (§19)
- **First-run setup screen** — configure receivers, scan,
  detection, recorder and classifier options from a browser
  wizard; validated, comment-preserving YAML saves with a plain
  restart to apply (§20)
- **Extensible demodulator and classifier framework** — add new modes
  by implementing one interface

## Architecture

```text
┌──────────────────────────────────────────────────────────────┐
│  macOS (dev) / Linux (prod)                                  │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  sdr-capture (Go binary)                               │  │
│  │  Reads SDRs via USB, streams IQ over UDP               │  │
│  │  macOS: native binary  Linux: Docker w/ USB passthru  │  │
│  └────────────────────────┬───────────────────────────────┘  │
│                           │ UDP (IQ frames)                  │
│  ┌────────────────────────▼──────────────────────────────┐   │
│  │  Docker Compose Network                                │   │
│  │                                                        │   │
│  │   iq-ingest ──┬─→ signal-processor ─┬─→ db (PostGIS)   │   │
│  │               │                     └─→ ws-hub         │   │
│  │               └─→ recorder (audio demod, Opus live)     │   │
│  │                                                        │   │
│  │   api-gateway :8080 — single client ingress           │   │
│  │     REST • /ws (→ ws-hub) • /ws/audio (→ recorder)     │   │
│  │          │                                             │   │
│  │          ▼                                             │   │
│  │   Frontend (SvelteKit) ←── tileserver-gl (OSM tiles)  │   │
│  └────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────┘
```

### Services

| Service | Language | Description |
| ------- | -------- | ----------- |
| `sdr-capture` | Go (cgo) | Reads SDR hardware, streams IQ over UDP; scan loop and control API |
| `iq-ingest` | Go | Receives IQ streams, validates frames, fans out to consumers |
| `signal-processor` | Go | DSP, classification (rules + ONNX), per-SDR location, persistence, event publishing; 2-SDR verification, tracking, and the TDOA engine + wiring delivered (on-air TDOA validation pending a third receiver) |
| `recorder` | Go | Audio demodulation (FM/AM/SSB), WAV + raw-IQ recording, live Opus audio, on-demand §19 time-frequency renders |
| `api-gateway` | Go | Single client ingress: REST API, recording file serving, `/ws` + `/ws/audio` relays, capture control proxy, §19 TFR proxy |
| `ws-hub` | Go | Internal WebSocket event fan-out (not client-facing) |
| `db` | PostGIS | Spatial database (PostgreSQL 16): signals, recordings, tracks |
| `tiles` | tileserver-gl | Self-hosted OSM vector tiles |
| `frontend` | SvelteKit | Interactive map + signal list; live data, recording playback, live Opus listening, and retune controls delivered; the UI lives at `http://localhost:5173` (dev) / `http://localhost:3000` (prod) |

## Technology Stack

| Layer | Technology |
| ----- | ---------- |
| Backend | Go 1.25+ |
| DSP | gonum (FFT, filtering) + custom Go demodulators |
| ML | ONNX Runtime (inference), Python/PyTorch (offline) |
| Database | PostGIS (PostgreSQL 16) |
| Frontend | SvelteKit 2 + Svelte 5 + TypeScript + Vite |
| Map | MapLibre GL JS (svelte-maplibre) |
| Tiles | tileserver-gl (self-hosted OSM MBTiles) |
| Styling | Tailwind CSS 4 |
| Audio | Go demodulation (FM, AM, SSB) → PCM → WAV; Opus live streaming |
| Real-time | WebSocket (Go hub, native browser client) |
| Orchestration | Docker Compose with profiles |
| Config | YAML per service |
| Logging | zerolog (structured JSON) |

## Prerequisites

### All Platforms

- **Go** 1.25+
- **Docker** + **Docker Compose** v2
- **Node.js** 20+ / npm

### macOS (Development)

- `brew install librtlsdr` (+ `brew install hackrf` for HackRF One)
- RTL-SDR dongle(s) connected via USB — or none: run hardware-less
  with `config/sdr-capture.sim.yaml`

### Linux (Production)

- `apt install librtlsdr-dev libhackrf-dev`
- RTL-SDR dongle(s) or HackRF One via USB

## Quick Start

```bash
# 1. Clone & Setup
git clone <repo-url> sigint-workbench
cd sigint-workbench
make setup
# → Installs deps, pulls LFS models, creates .env from .env.example

# 2. Configure SDRs — open the first-run wizard at
#    http://localhost:5173/setup (dev; :3000/setup under
#    production deploy) or edit config/sdr-capture.yaml by hand.
#    No hardware? Copy config/sdr-capture.sim.yaml over
#    config/sdr-capture.yaml first — two simulated devices
#    sharing one iq-ingest port (SPEC §16.4).

# 3. Set Up Map Tiles
./tiles/setup-tiles.sh california 10 16
#    Full-region render (needs `brew install tilemaker`; downloads a
#    Geofabrik PBF). Skipping this leaves tiles/data empty and the
#    tiles container crash-loops — its /data mount is read-only, so
#    it cannot fetch its own sample data. Minimal fallback instead:
#    curl -fsSL -o tiles/data/zurich_switzerland.mbtiles \
#      https://github.com/maptiler/tileserver-gl/releases/download/v1.3.0/zurich_switzerland.mbtiles
#    Custom regions serve under their file stem, so set
#    VITE_TILE_URL=http://localhost:8082/data/<region>/{z}/{x}/{y}.pbf
#    in frontend/.env for those.

# 4a. macOS Development
make dev
# → UI at http://localhost:5173 (Vite dev server)

# 4b. Linux Production
make deploy
# → UI at http://localhost:3000
```

## Project Structure

```text
sigint-workbench/
├── cmd/                    # One main.go per service (6 services + bench tools)
├── internal/
│   ├── sdr/                # SDR interface, drivers, UDP protocol
│   ├── dsp/                # FFT, peak detection, filters, AGC
│   ├── audio/              # FM/AM/SSB demodulators, WAV/Opus encoders, registry
│   ├── classify/           # Feature extraction, rules, ONNX
│   ├── location/           # Location, tracking, 2-SDR verification
│   ├── tdoa/               # TDOA engine: buffers, resampling, correlator, solver
│   ├── record/             # WAV/IQ recording, live Opus WS server, retention
│   ├── db/                 # Postgres/PostGIS models and queries
│   ├── api/                # REST API server, capture control proxy
│   ├── ws/                 # WebSocket hub
│   └── config/             # Shared config loading
├── .github/                # GitHub Actions CI workflows
├── frontend/               # SvelteKit + MapLibre + Tailwind + nginx.conf
├── db/                     # PostGIS schema (init.sql) + SQL migrations
├── config/                 # Per-service YAML configs
├── docker/                 # Dockerfiles (frontend, sdr-capture, services)
├── tiles/                  # tileserver-gl config + setup script
├── models/                 # ONNX models (git-lfs)
├── recordings/             # Host-mounted audio/IQ recordings
├── docs/                   # SPEC (system contract) + HARDWARE.md runbook
├── scripts/                # fetch-ort.sh (ONNX runtime), smoke-test.sh
├── docker-compose.yml
├── Makefile
├── .env.example            # Template copied to .env by make setup
├── go.mod
└── go.sum
```

## Development

| Make Target | Description |
| ----------- | ----------- |
| `make dev` | Full dev environment (macOS) |
| `make build-capture` | Build native macOS sdr-capture |
| `make build-capture-hw` | Build sdr-capture with rtlsdr + hackrf drivers |
| `make build-capture-linux` | Cross-compile sdr-capture for Linux |
| `make build-hw-tools` | Build RTL-SDR bench tools (rtl-list, rtl-calibrate) |
| `make build-prod` | Build all Docker images |
| `make deploy` | Start production stack (Linux) |
| `make stop` | Stop all Docker services |
| `make test` | Run Go tests |
| `make frontend-test` | Run frontend tests (vitest) |
| `make smoke-onnx` | E2E: real binary detects CW + WFM over UDP |
| `make db-init` | Initialize PostGIS schema |
| `make db-shell` | Open psql shell |
| `make setup` | First-time setup (deps, LFS, .env) |
| `make tidy` | Tidy Go modules |
| `make clean` | Remove build artifacts |
| `make help` | Show all targets |

The Go test suite covers the DSP core (FFT, peak detection, AGC,
filters), the SDR package (UDP IQ protocol, simulator, capture-config
loading), classification, location/verification, audio demodulation,
recording, API handlers, and DB models. Run it with `make test`; the
frontend suite runs via `make frontend-test`.

### Adding a New Demodulator

Implement the `Demodulator` interface in `internal/audio/`, register it in
`DefaultRegistry()`, and the recorder and API will automatically use it.

### Hardware Drivers (build tags)

sdr-capture compiles hardware drivers in via build tags (cgo); the
simulator is always available:

```bash
# RTL-SDR only (SPEC §15.3)
go build -tags rtlsdr -o bin/sdr-capture ./cmd/sdr-capture
# RTL-SDR + HackRF One (SPEC §15.4) — or: make build-capture-hw
go build -tags "rtlsdr,hackrf" -o bin/sdr-capture ./cmd/sdr-capture
```

- RTL-SDR: `usb_index` selects the dongle.
- HackRF: `serial` selects the device (omit to use the first found);
  RX-only by policy (SPEC H2 — the driver never links the transmit
  API, and a guard test enforces it).

For device enumeration, on-hardware validation, and power calibration
(§5.6), see [docs/HARDWARE.md](docs/HARDWARE.md). `cmd/rtl-list`
prints the USB index, product and serial of each dongle;
`cmd/rtl-calibrate` measures `calibration_offset_db` against a known
on-air carrier. Build both with `make build-hw-tools`.

Without the tag, selecting that driver in YAML aborts startup with a
clear "built without … support" error. Install the libraries first:
`brew install librtlsdr hackrf` (macOS) or `apt install librtlsdr-dev
libhackrf-dev` (Debian/Ubuntu). The Linux production image already
compiles both drivers in.

### Training Classification Models

Train in Python (PyTorch), export to ONNX, place in `models/`.
See `models/README.md` for the input/output format.

### Continuous Integration

GitHub Actions (`.github/workflows/ci.yml`) runs four parallel jobs on
every push and pull request to `main`:

- **Go** — `go vet` + `go test`
- **Frontend** — dependency install + `svelte-check` type checking
- **Markdown** — `markdownlint-cli2` on all `.md` files
- **Docker** — builds the frontend, sdr-capture, and services images and
  scans them with Trivy (frontend: CRITICAL+HIGH; Go images: CRITICAL;
  unfixed vulnerabilities ignored)

## Legal Notice

**This software is for authorized use only.** You are responsible for
complying with all applicable laws in your jurisdiction. Receiving
unencrypted signals is generally legal; recording or acting upon
intercepted communications may be restricted. Consult a lawyer.

## License

[MIT License](LICENSE) — Copyright (c) 2026 Matt Eskes
