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
    confidence      REAL DEFAULT 0,
    power_dbm       REAL,
    location        GEOGRAPHY(POINT, 4326),
    accuracy_m      REAL,
    first_seen      TIMESTAMPTZ DEFAULT now(),
    last_seen       TIMESTAMPTZ DEFAULT now(),
    sdr_id          TEXT REFERENCES sdrs(id),
    verified        BOOLEAN DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_signals_location ON signals USING GIST(location);
CREATE INDEX IF NOT EXISTS idx_signals_freq ON signals(frequency_hz);
CREATE INDEX IF NOT EXISTS idx_signals_class ON signals(class);
CREATE INDEX IF NOT EXISTS idx_signals_last_seen ON signals(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_signals_sdr ON signals(sdr_id);

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
    signal_id   UUID REFERENCES signals(id) ON DELETE CASCADE,
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