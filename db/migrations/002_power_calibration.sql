-- 002: signals.power_calibrated (power-calibration honesty flag — §5.6)
--
-- TRUE means power_dbm is a calibrated absolute level (the detecting
-- SDR has a calibration_offset_db in the sdr-capture config);
-- FALSE (default) means power_dbm is uncalibrated relative dB and the
-- UI/API must label it "dB (rel.)". Older rows migrate as FALSE.
--
-- Fresh deployments get this schema from db/init.sql on first start.
-- Apply manually to an existing database (idempotent):
--
--   make db-migrate
--   # or:
--   docker compose exec -T db psql -U sdr -d sdr < db/migrations/002_power_calibration.sql

ALTER TABLE signals ADD COLUMN IF NOT EXISTS power_calibrated BOOLEAN NOT NULL DEFAULT FALSE;
