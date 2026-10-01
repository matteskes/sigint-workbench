// Package db — SQL queries.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpsertSignal inserts or updates a signal.
func (d *DB) UpsertSignal(ctx context.Context, s *Signal) error {
	query := `
		INSERT INTO signals (id, frequency_hz, bandwidth_hz, modulation, sub_type, class,
			confidence, power_dbm, location, accuracy_m, first_seen, last_seen, sdr_id, verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, ST_SetSRID(ST_MakePoint($9, $10), 4326),
			$11, $12, $13, $14, $15)
		ON CONFLICT (id) DO UPDATE SET
			last_seen = EXCLUDED.last_seen,
			confidence = EXCLUDED.confidence,
			power_dbm = EXCLUDED.power_dbm,
			location = EXCLUDED.location,
			verified = EXCLUDED.verified
	`
	_, err := d.Pool.Exec(ctx, query,
		s.ID, s.FreqHz, s.BandwidthHz, s.Modulation, s.SubType, s.Class,
		s.Confidence, s.PowerDBM, s.Lon, s.Lat, s.AccuracyM,
		s.FirstSeen, s.LastSeen, s.SDRID, s.Verified,
	)
	return err
}

// GetSignals returns all active signals, optionally filtered by bounding box.
func (d *DB) GetSignals(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]Signal, error) {
	query := `
		SELECT id, frequency_hz, bandwidth_hz, COALESCE(modulation,''), COALESCE(sub_type,''),
			COALESCE(class,''), confidence, COALESCE(power_dbm,0),
			ST_Y(location), ST_X(location), COALESCE(accuracy_m,0),
			first_seen, last_seen, sdr_id, verified
		FROM signals
		WHERE location && ST_MakeEnvelope($1, $2, $3, $4, 4326)
		ORDER BY last_seen DESC
		LIMIT 500
	`
	rows, err := d.Pool.Query(ctx, query, minLon, minLat, maxLon, maxLat)
	if err != nil {
		return nil, fmt.Errorf("db: get signals: %w", err)
	}
	defer rows.Close()

	var signals []Signal
	for rows.Next() {
		var s Signal
		if err := rows.Scan(&s.ID, &s.FreqHz, &s.BandwidthHz, &s.Modulation, &s.SubType,
			&s.Class, &s.Confidence, &s.PowerDBM, &s.Lat, &s.Lon, &s.AccuracyM,
			&s.FirstSeen, &s.LastSeen, &s.SDRID, &s.Verified); err != nil {
			return nil, err
		}
		signals = append(signals, s)
	}
	return signals, rows.Err()
}

// GetSignal returns a single signal by ID.
func (d *DB) GetSignal(ctx context.Context, id string) (*Signal, error) {
	query := `
		SELECT id, frequency_hz, bandwidth_hz, COALESCE(modulation,''), COALESCE(sub_type,''),
			COALESCE(class,''), confidence, COALESCE(power_dbm,0),
			ST_Y(location), ST_X(location), COALESCE(accuracy_m,0),
			first_seen, last_seen, sdr_id, verified
		FROM signals
		WHERE id = $1
	`
	var s Signal
	err := d.Pool.QueryRow(ctx, query, id).Scan(&s.ID, &s.FreqHz, &s.BandwidthHz, &s.Modulation, &s.SubType,
		&s.Class, &s.Confidence, &s.PowerDBM, &s.Lat, &s.Lon, &s.AccuracyM,
		&s.FirstSeen, &s.LastSeen, &s.SDRID, &s.Verified)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("db: get signal: %w", err)
	}
	return &s, nil
}

// GetRecording returns a single recording by ID.
func (d *DB) GetRecording(ctx context.Context, id string) (*Recording, error) {
	query := `
		SELECT id, signal_id, start_time, end_time, duration_s,
			sample_rate, center_freq, bandwidth_hz, file_path, file_format, size_bytes
		FROM recordings
		WHERE id = $1
	`
	var r Recording
	err := d.Pool.QueryRow(ctx, query, id).Scan(&r.ID, &r.SignalID, &r.StartTime, &r.EndTime, &r.DurationS,
		&r.SampleRate, &r.CenterFreq, &r.BandwidthHz, &r.FilePath, &r.FileFormat, &r.SizeBytes)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("db: get recording: %w", err)
	}
	return &r, nil
}

// GetRecordings returns the most recent recordings, optionally filtered
// by signal ID. limit defaults to 50 and is capped at 500.
func (d *DB) GetRecordings(ctx context.Context, limit int, signalID string) ([]Recording, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := `
		SELECT id, signal_id, start_time, end_time, duration_s,
			sample_rate, center_freq, bandwidth_hz, file_path, file_format, size_bytes
		FROM recordings
	`
	args := []interface{}{}
	if signalID != "" {
		query += " WHERE signal_id = $1"
		args = append(args, signalID)
	}
	query += fmt.Sprintf(" ORDER BY start_time DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := d.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: get recordings: %w", err)
	}
	defer rows.Close()

	var recs []Recording
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.ID, &r.SignalID, &r.StartTime, &r.EndTime, &r.DurationS,
			&r.SampleRate, &r.CenterFreq, &r.BandwidthHz, &r.FilePath, &r.FileFormat, &r.SizeBytes); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

// GetSDR returns a single SDR device by ID.
func (d *DB) GetSDR(ctx context.Context, id string) (*SDRDevice, error) {
	query := `
		SELECT id, model, COALESCE(serial,''), lat, lon, gain_db, freq_hz, active
		FROM sdrs
		WHERE id = $1
	`
	var s SDRDevice
	err := d.Pool.QueryRow(ctx, query, id).Scan(&s.ID, &s.Model, &s.Serial, &s.Lat, &s.Lon, &s.GainDB, &s.FreqHz, &s.Active)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("db: get sdr: %w", err)
	}
	return &s, nil
}

// ListSDRs returns all registered SDR devices.
func (d *DB) ListSDRs(ctx context.Context) ([]SDRDevice, error) {
	query := `
		SELECT id, model, COALESCE(serial,''), lat, lon, gain_db, freq_hz, active
		FROM sdrs
		ORDER BY id
	`
	rows, err := d.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("db: list sdrs: %w", err)
	}
	defer rows.Close()

	var sdrs []SDRDevice
	for rows.Next() {
		var s SDRDevice
		if err := rows.Scan(&s.ID, &s.Model, &s.Serial, &s.Lat, &s.Lon, &s.GainDB, &s.FreqHz, &s.Active); err != nil {
			return nil, err
		}
		sdrs = append(sdrs, s)
	}
	return sdrs, rows.Err()
}

// UpsertSDR inserts or updates an SDR device.
func (d *DB) UpsertSDR(ctx context.Context, s *SDRDevice) error {
	query := `
		INSERT INTO sdrs (id, model, serial, lat, lon, gain_db, freq_hz, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			model = EXCLUDED.model,
			serial = EXCLUDED.serial,
			lat = EXCLUDED.lat,
			lon = EXCLUDED.lon,
			gain_db = EXCLUDED.gain_db,
			freq_hz = EXCLUDED.freq_hz,
			active = EXCLUDED.active
	`
	_, err := d.Pool.Exec(ctx, query,
		s.ID, s.Model, s.Serial, s.Lat, s.Lon, s.GainDB, s.FreqHz, s.Active,
	)
	if err != nil {
		return fmt.Errorf("db: upsert sdr: %w", err)
	}
	return nil
}

// UpdateSDR updates an SDR device's settings. It returns ErrNotFound if
// the SDR does not exist.
func (d *DB) UpdateSDR(ctx context.Context, s *SDRDevice) error {
	query := `
		UPDATE sdrs SET
			model = $2, serial = $3, lat = $4, lon = $5,
			gain_db = $6, freq_hz = $7, active = $8
		WHERE id = $1
	`
	tag, err := d.Pool.Exec(ctx, query,
		s.ID, s.Model, s.Serial, s.Lat, s.Lon, s.GainDB, s.FreqHz, s.Active,
	)
	if err != nil {
		return fmt.Errorf("db: update sdr: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSignal removes a signal by ID.
func (d *DB) DeleteSignal(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM signals WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: delete signal: %w", err)
	}
	return nil
}

// InsertRecording inserts a recording entry.
func (d *DB) InsertRecording(ctx context.Context, r *Recording) error {
	query := `
		INSERT INTO recordings (id, signal_id, start_time, end_time, duration_s,
			sample_rate, center_freq, bandwidth_hz, file_path, file_format, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := d.Pool.Exec(ctx, query,
		r.ID, r.SignalID, r.StartTime, r.EndTime, r.DurationS,
		r.SampleRate, r.CenterFreq, r.BandwidthHz, r.FilePath, r.FileFormat, r.SizeBytes,
	)
	return err
}