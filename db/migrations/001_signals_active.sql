-- 001: signals.method + signals.active (D6 — SPEC §11.2, §12.2)
--
-- Fresh deployments get this schema from db/init.sql on first start.
-- Apply manually to an existing database (idempotent):
--
--   make db-migrate
--   # or:
--   docker compose exec -T db psql -U sdr -d sdr < db/migrations/001_signals_active.sql

ALTER TABLE signals ADD COLUMN IF NOT EXISTS method TEXT NOT NULL DEFAULT '';
ALTER TABLE signals ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_signals_active ON signals(last_seen DESC) WHERE active;
