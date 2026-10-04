# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project overview

Real-time multi-SDR signal monitoring, classification, recording, and
geospatial visualization.

- `docs/SPEC.md` is the source of truth for behavior. Sections are
  cited as `§N.N`; locked decisions as `D1`–`D8`, `H1`–`H2`, `A1`–`A6`.
- Every SPEC section carries a status tag: `[implemented]`,
  `[planned]`, `[gap]`. Change code and docs together.
- Services live in `cmd/` (one main.go each): `sdr-capture`,
  `iq-ingest`, `signal-processor`, `recorder`, `api-gateway`,
  `ws-hub`. Also in `cmd/`: bench tools `rtl-list`, `rtl-calibrate`
  (need librtlsdr) and the `smoke-frames` ONNX smoke fixture.
- Shared logic lives in `internal/`: `sdr`, `dsp`, `audio`,
  `classify`, `location`, `db`, `api`, `ws`, `config`, `record`.
- Frontend: SvelteKit 2 + Svelte 5 + TypeScript + Tailwind 4 +
  MapLibre GL, in `frontend/`.
- Database: PostGIS (PostgreSQL 16); schema in `db/`.

## Setup commands

- First time: `make setup` (installs Go/npm deps, pulls git-lfs
  model in `models/`, creates `.env` from `.env.example`).
- macOS dev stack: `make dev`; stop: `make stop`.
- Production: `make build-prod` then `make deploy` (Docker).
- Use `make help` to list all targets.

## Testing

- Go (no hardware needed): `make test` (`go test ./... -v -count=1`).
- Tagged tests: `-tags onnx` needs `ORT_LIBRARY_PATH` +
  `CLASSIFIER_ONNX_PATH` (model is git-lfs); `-tags opus` needs
  libopus/libopusfile. CI shows the exact recipe:
  `.github/workflows/ci.yml`.
- DB integration suite: gated on `TEST_DATABASE_URL`; setup recipe
  (socat bridge) in SPEC §17.3.
- Frontend: `make frontend-test` (vitest); types via
  `cd frontend && npm run check` (svelte-check).
- E2E: `make smoke-onnx`.
- Add tests for new behavior; SPEC §17.3 lists per-feature
  obligations that are normative.

## Lint / checks (CI gates)

- `go vet ./...` — also under `-tags onnx` / `-tags opus` when
  touching `internal/classify` or `internal/audio`/`internal/record`.
- Markdown: `npx markdownlint-cli2 "**/*.md"` — prose ≤ 80 columns
  (MD013; tables/code blocks exempt), compact table style (MD060).

## Code style

- Go 1.25, gofmt-clean; keep dependencies minimal (see `go.mod`:
  chi, gorilla/websocket, pgx/v5, zerolog, gonum, onnxruntime_go).
- Logging: zerolog, structured JSON.
- Config: per-service YAML in `config/`; secrets only via `.env`
  (never committed).
- RX-only by design (H2): no driver may transmit; the HackRF driver
  must not link `hackrf_start_tx`.
- Extending demodulators/classifiers: implement the interface in
  `internal/audio` / `internal/classify` and register in the
  default registry; see README "Adding a New Demodulator".

## Commit / PR guidelines

- Conventional Commits with a scope: `feat(recorder):`, `fix(lint):`,
  `docs(spec):`, `chore:`. Cite SPEC refs, e.g.
  `feat(recorder): ... (D1, §10.3-§10.4)`.
- Scopes follow service/dir names: `sdr-capture`, `signal-processor`,
  `recorder`, `gateway`, `frontend`, `config`, `spec`.
- Before finishing a task: `make test` green; add `make
  frontend-test` + svelte-check when frontend changed; markdownlint
  when docs changed.
