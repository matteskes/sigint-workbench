-- 005: app_settings key/value table (§20 setup screen)
--
-- Small store for gateway-managed app state, currently the §20
-- first-run flag (setup.completed_at). Not service configuration:
-- the YAML files in config/ remain the single source of truth (§16).
--
-- Fresh deployments get this schema from db/init.sql on first start.
-- Apply manually to an existing database (idempotent):
--
--   make db-migrate
--   # or:
--   docker compose exec -T db psql -U sdr -d sdr < db/migrations/005_app_settings.sql

CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT now()
);
