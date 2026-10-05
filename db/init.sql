-- SIGINT Workbench — PostGIS Schema
-- Run automatically on first `docker compose up` via docker-entrypoint-initdb.d

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ─── SDR Devices ───
CREATE TABLE IF NOT EXISTS sdrs (
    id          TEXT PRIMARY KEY,
    model       TEXT NOT NULL,
    serial      TEXT,
    lat         DOUBLE PRECISION,
    lon         DOUBLE PRECISION,
    gain_db     DOUBLE PRECISION DEFAULT 40,
    freq_hz     BIGINT DEFAULT 0,
    active      BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT now()
);

-- ─── Detected Signals ───
CREATE TABLE IF NOT EXISTS signals (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    frequency_hz    BIGINT NOT NULL,
    bandwidth_hz    INTEGER,
    modulation      TEXT,
    sub_type        TEXT,
    class           TEXT,
    method          TEXT NOT NULL DEFAULT '',
    confidence      REAL DEFAULT 0,
    power_dbm       REAL,
    power_calibrated BOOLEAN NOT NULL DEFAULT FALSE,
    location        GEOGRAPHY(POINT, 4326),
    accuracy_m      REAL,
    first_seen      TIMESTAMPTZ DEFAULT now(),
    last_seen       TIMESTAMPTZ DEFAULT now(),
    sdr_id          TEXT REFERENCES sdrs(id),
    verified        BOOLEAN DEFAULT FALSE,
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    -- §9.6 TDOA quality (migration 004): NULL until a §9.5 fix is
    -- accepted; sticky across plain re-observations (COALESCE upsert).
    residual_ns     DOUBLE PRECISION,
    pairs_used      INTEGER,
    max_baseline_m  DOUBLE PRECISION,
    reference       TEXT
);

CREATE INDEX IF NOT EXISTS idx_signals_location ON signals USING GIST(location);
CREATE INDEX IF NOT EXISTS idx_signals_freq ON signals(frequency_hz);
CREATE INDEX IF NOT EXISTS idx_signals_class ON signals(class);
CREATE INDEX IF NOT EXISTS idx_signals_last_seen ON signals(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_signals_sdr ON signals(sdr_id);
-- Partial index backing the live-signal views (§11.2): only active rows.
CREATE INDEX IF NOT EXISTS idx_signals_active ON signals(last_seen DESC) WHERE active;

-- ─── Signal Recordings ───
CREATE TABLE IF NOT EXISTS recordings (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    signal_id       UUID REFERENCES signals(id) ON DELETE SET NULL,
    start_time      TIMESTAMPTZ NOT NULL,
    end_time        TIMESTAMPTZ,
    duration_s      REAL,
    sample_rate     INTEGER,
    center_freq     BIGINT,
    bandwidth_hz    INTEGER,
    file_path       TEXT NOT NULL,
    file_format     TEXT,
    size_bytes      BIGINT
);

CREATE INDEX IF NOT EXISTS idx_recordings_signal ON recordings(signal_id);
CREATE INDEX IF NOT EXISTS idx_recordings_start ON recordings(start_time DESC);

-- ─── Signal Tracks (movement over time) ───
CREATE TABLE IF NOT EXISTS tracks (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    signal_id   UUID UNIQUE REFERENCES signals(id) ON DELETE CASCADE,
    path        GEOGRAPHY(LINESTRING, 4326),
    speed_kmh   REAL,
    heading     REAL,
    updated_at  TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tracks_path ON tracks USING GIST(path);

-- ─── User Annotations ───
CREATE TABLE IF NOT EXISTS annotations (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    signal_id   UUID REFERENCES signals(id) ON DELETE CASCADE,
    user_note   TEXT,
    created_at  TIMESTAMPTZ DEFAULT now()
);

-- ─── Verification Records ───
CREATE TABLE IF NOT EXISTS verifications (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    signal_id       UUID REFERENCES signals(id) ON DELETE CASCADE,
    sdr1_id         TEXT,
    sdr2_id         TEXT,
    verified        BOOLEAN,
    confidence      REAL,
    created_at      TIMESTAMPTZ DEFAULT now()
);
-- ─── App Settings (§20 setup screen) ───
-- Gateway-managed app state (the first-run flag). Service
-- configuration stays in config/*.yaml (§16).
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT now()
);
