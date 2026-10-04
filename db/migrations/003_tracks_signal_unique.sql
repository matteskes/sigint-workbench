-- 003: tracks.signal_id UNIQUE (§12.4 — one current track per signal)
--
-- The tracking writer (§9.4, Phase 4) upserts one row per signal;
-- the UNIQUE index backs the ON CONFLICT clause. Existing databases
-- have no track rows yet (the table was never populated), so the
-- index build cannot hit duplicates.
--
-- Fresh deployments get this schema from db/init.sql on first start.
-- Apply manually to an existing database (idempotent):
--
--   make db-migrate

CREATE UNIQUE INDEX IF NOT EXISTS ux_tracks_signal ON tracks(signal_id);
