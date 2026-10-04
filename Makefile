.PHONY: help dev build-capture build-prod test frontend-test db-init clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ─── Development (macOS) ───

dev: ## Start full dev environment (macOS: native SDR capture + Docker services)
	@echo "=== Building sdr-capture (native macOS) ==="
	@$(MAKE) build-capture
	@echo "=== Starting sdr-capture ==="
	@./bin/sdr-capture -config config/sdr-capture.yaml &
	@echo "=== Starting Docker services ==="
	docker compose up -d iq-ingest signal-processor classifier recorder location-service api-gateway ws-hub db tiles
	@echo "=== Starting frontend dev server ==="
	cd frontend && npm run dev

build-capture: ## Build native macOS sdr-capture binary
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o bin/sdr-capture ./cmd/sdr-capture

build-capture-linux: ## Cross-compile sdr-capture for Linux (requires C cross-compiler)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc go build -o bin/sdr-capture-linux ./cmd/sdr-capture

# ─── Production (Linux) ───

build-prod: ## Build all Docker images for Linux production
	docker compose build

deploy: ## Start full production stack (all services in Docker)
	docker compose --profile prod up -d

stop: ## Stop all services
	docker compose down

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

frontend-test: ## Run frontend tests
	cd frontend && npm test

smoke-onnx: ## E2E: real binary detects CW+WFM over UDP (auto-downloads ORT; needs git-lfs model)
	./scripts/smoke-test.sh
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