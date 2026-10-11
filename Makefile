.PHONY: help dev build-capture build-capture-hw build-capture-linux build-ingest build-processor build-recorder build-hw-tools build-prod deploy stop test frontend-test e2e e2e-ui e2e-api smoke-onnx ort-lib db-init db-migrate db-shell tidy setup clean

help: ## Show available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ─── Development (macOS) ───

# N1 (docs/UI-BUGCHECK.md): `make dev` runs Vite in the foreground and
# Vite reads stdin — backgrounding this target from a terminal without
# detaching stdin SIGTTIN-stops the whole bench. Use:
#   nohup make dev < /dev/null > /tmp/sigint-dev.log 2>&1 &
dev: ## Start full dev environment (macOS: native SDR capture + Docker services)
	./scripts/dev-macos.sh

build-capture: ## Build native macOS sdr-capture binary
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o bin/sdr-capture ./cmd/sdr-capture

build-capture-hw: ## Build native sdr-capture with hardware drivers (rtlsdr + hackrf; needs librtlsdr + libhackrf)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags "rtlsdr,hackrf" -o bin/sdr-capture ./cmd/sdr-capture

build-hw-tools: ## Build RTL-SDR bench tools (rtl-list + rtl-calibrate; needs librtlsdr)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags rtlsdr -o bin/rtl-list ./cmd/rtl-list
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags rtlsdr -o bin/rtl-calibrate ./cmd/rtl-calibrate

build-capture-linux: ## Cross-compile sdr-capture for Linux (requires C cross-compiler)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc go build -o bin/sdr-capture-linux ./cmd/sdr-capture

build-ingest: ## Build native iq-ingest (macOS UDP bench — HARDWARE.md §4)
	go build -o bin/iq-ingest ./cmd/iq-ingest

build-processor: ## Build native signal-processor (macOS UDP bench; rules classifier unless -tags onnx)
	go build -o bin/signal-processor ./cmd/signal-processor

build-recorder: ## Build native recorder with live Opus audio (-tags opus; needs libopus — macOS bench, HARDWARE.md §4)
	go build -tags opus -o bin/recorder ./cmd/recorder

# ─── Production (Linux) ───

build-prod: ## Build all Docker images for Linux production
	docker compose build

deploy: ## Start full production stack (all services in Docker)
	docker compose --profile prod up -d

stop: ## Stop all services
	docker compose down
	@-pkill -f 'bin/sdr-capture' 2>/dev/null || true

# ─── Database ───

db-init: ## Initialize PostGIS schema
	docker compose exec db psql -U sdr -d sdr -f /docker-entrypoint-initdb.d/init.sql

db-migrate: ## Apply pending schema migrations (db/migrations, idempotent)
	cat db/migrations/*.sql | docker compose exec -T db psql -U sdr -d sdr

db-shell: ## Open psql shell
	docker compose exec db psql -U sdr -d sdr

# ─── Testing ───

test: ## Run all Go tests
	go test ./... -v -count=1

frontend-test: ## Run frontend unit tests
	cd frontend && npm test

e2e: ## Run Playwright E2E tests (starts the dev web server; backend suites skip when `make dev` is down)
	cd frontend && npx playwright test

e2e-ui: ## Run Playwright E2E tests with UI reporter (requires `npm run dev`)
	cd frontend && npx playwright test --ui

e2e-api: ## Run Go-based API E2E tests (requires `make deploy` or `docker compose up -d`)
	cd tests/e2e && go test ./api/... -v -count=1

smoke-onnx: ## E2E: real binary detects CW+WFM over UDP (auto-downloads ORT; needs git-lfs model)
	./scripts/smoke-test.sh

ort-lib: ## Fetch ONNX Runtime library for native -tags onnx builds (prints the export line)
	./scripts/fetch-ort.sh
# ─── Utilities ───

tidy: ## Tidy Go modules
	go mod tidy

setup: ## First-time setup (install deps, init git lfs)
	@echo "=== Installing git-lfs ==="
	@command -v git-lfs >/dev/null 2>&1 || git lfs install
	git lfs pull
	@echo "=== Installing Go dependencies ==="
	go mod download
	@echo "=== Installing frontend dependencies ==="
	cd frontend && npm install
	@echo "=== Setting up tiles (run with region arg) ==="
	@echo "  Usage: ./tiles/setup-tiles.sh <region>"
	@echo "=== Done. Create .env from .env.example ==="
	@test -f .env || cp .env.example .env && echo "Created .env from template"

clean: ## Remove build artifacts
	rm -rf bin/ frontend/build/ frontend/.svelte-kit/