-- 004: signals TDOA quality columns (§9.6 — SPEC §12.2)
--
-- "Fixes are signal_locations rows" (§9.5) resolves to four nullable
-- quality columns on the signals row itself: residual_ns (RMS solve
-- residual), pairs_used (eligible receiver pairs), max_baseline_m
-- (longest inter-receiver baseline) and reference (the receiver whose
-- row carries the accepted fix). There is no separate locations
-- table; the columns are sticky across plain re-observations
-- (COALESCE upsert, §9.6).
--
-- Fresh deployments get this schema from db/init.sql on first start.
-- Apply manually to an existing database (idempotent):
--
--   make db-migrate
--   # or:
--   docker compose exec -T db psql -U sdr -d sdr < db/migrations/004_tdoa_quality.sql

ALTER TABLE signals ADD COLUMN IF NOT EXISTS residual_ns DOUBLE PRECISION;
ALTER TABLE signals ADD COLUMN IF NOT EXISTS pairs_used INTEGER;
ALTER TABLE signals ADD COLUMN IF NOT EXISTS max_baseline_m DOUBLE PRECISION;
ALTER TABLE signals ADD COLUMN IF NOT EXISTS reference TEXT;
