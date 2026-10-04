// Package db — SQL queries.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertSignal inserts or updates a signal. A re-observation always
// re-activates the row (§11.2) and refreshes the classification
// (modulation, subType, class, method) alongside the measurement
// fields; first_seen is kept on conflict. verified is latched: once
// TRUE, an unverified re-observation cannot clear it (§8) — only the
// two-SDR verifier sets it, via MarkVerified.
func (d *DB) UpsertSignal(ctx context.Context, s *Signal) error {
	query := `
		INSERT INTO signals (id, frequency_hz, bandwidth_hz, modulation, sub_type, class, method,
			confidence, power_dbm, power_calibrated, location, accuracy_m, first_seen, last_seen, sdr_id, verified, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, ST_SetSRID(ST_MakePoint($11, $12), 4326),
			$13, $14, $15, $16, $17, $18)
		ON CONFLICT (id) DO UPDATE SET
			last_seen = EXCLUDED.last_seen,
			bandwidth_hz = EXCLUDED.bandwidth_hz,
			modulation = EXCLUDED.modulation,
			sub_type = EXCLUDED.sub_type,
			class = EXCLUDED.class,
			method = EXCLUDED.method,
			confidence = EXCLUDED.confidence,
			power_dbm = EXCLUDED.power_dbm,
			power_calibrated = EXCLUDED.power_calibrated,
			location = EXCLUDED.location,
			verified = signals.verified OR EXCLUDED.verified,
			active = TRUE
	`
	_, err := d.Pool.Exec(ctx, query,
		s.ID, s.FreqHz, s.BandwidthHz, s.Modulation, s.SubType, s.Class, s.Method,
		s.Confidence, s.PowerDBM, s.PowerCalibrated, s.Lon, s.Lat, s.AccuracyM,
		s.FirstSeen, s.LastSeen, s.SDRID, s.Verified, s.Active,
	)
	return err
}

// GetSignals returns all active signals, optionally filtered by bounding box.
// Signals with a NULL location (unlocated SDRs, §9.3) are always included;
// only located signals are additionally bounded by the envelope.
// Retired (inactive) rows are hidden (§11.2) but preserved in the table.
func (d *DB) GetSignals(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]Signal, error) {
	query := `
		SELECT id, frequency_hz, bandwidth_hz, COALESCE(modulation,''), COALESCE(sub_type,''),
			COALESCE(class,''), COALESCE(method,''), confidence, COALESCE(power_dbm,0), power_calibrated,
			ST_Y(location::geometry), ST_X(location::geometry), COALESCE(accuracy_m,0),
			first_seen, last_seen, sdr_id, verified
		FROM signals
		WHERE active AND (location IS NULL OR location::geometry && ST_MakeEnvelope($1, $2, $3, $4, 4326))
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
			&s.Class, &s.Method, &s.Confidence, &s.PowerDBM, &s.PowerCalibrated, &s.Lat, &s.Lon, &s.AccuracyM,
			&s.FirstSeen, &s.LastSeen, &s.SDRID, &s.Verified); err != nil {
			return nil, err
		}
		s.Active = true
		signals = append(signals, s)
	}
	return signals, rows.Err()
}

// GetSignal returns a single signal by ID (active or retired).
func (d *DB) GetSignal(ctx context.Context, id string) (*Signal, error) {
	query := `
		SELECT id, frequency_hz, bandwidth_hz, COALESCE(modulation,''), COALESCE(sub_type,''),
			COALESCE(class,''), COALESCE(method,''), confidence, COALESCE(power_dbm,0), power_calibrated,
			ST_Y(location::geometry), ST_X(location::geometry), COALESCE(accuracy_m,0),
			first_seen, last_seen, sdr_id, verified, active
		FROM signals
		WHERE id = $1
	`
	var s Signal
	err := d.Pool.QueryRow(ctx, query, id).Scan(&s.ID, &s.FreqHz, &s.BandwidthHz, &s.Modulation, &s.SubType,
		&s.Class, &s.Method, &s.Confidence, &s.PowerDBM, &s.PowerCalibrated, &s.Lat, &s.Lon, &s.AccuracyM,
		&s.FirstSeen, &s.LastSeen, &s.SDRID, &s.Verified, &s.Active)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("db: get signal: %w", err)
	}
	return &s, nil
}

// GetAnnotations returns a signal's user notes, newest first (§12.5).
func (d *DB) GetAnnotations(ctx context.Context, signalID string) ([]Annotation, error) {
	query := `
		SELECT id, signal_id, COALESCE(user_note, ''), created_at
		FROM annotations
		WHERE signal_id = $1
		ORDER BY created_at DESC, id DESC
	`
	rows, err := d.Pool.Query(ctx, query, signalID)
	if err != nil {
		return nil, fmt.Errorf("db: get annotations: %w", err)
	}
	defer rows.Close()

	var notes []Annotation
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.SignalID, &a.UserNote, &a.CreatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, a)
	}
	return notes, rows.Err()
}

// AddAnnotation appends a user note to a signal (§12.5). The signal
// must exist — unknown IDs yield ErrNotFound (⇒ 404 at the API).
func (d *DB) AddAnnotation(ctx context.Context, signalID, note string) (*Annotation, error) {
	var a Annotation
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO annotations (signal_id, user_note)
		SELECT $1, $2 WHERE EXISTS (SELECT 1 FROM signals WHERE id = $1)
		RETURNING id, signal_id, user_note, created_at
	`, signalID, note).Scan(&a.ID, &a.SignalID, &a.UserNote, &a.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("db: add annotation: %w", err)
	}
	return &a, nil
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
	// lat/lon are NULL-allowed (§12.1); reads COALESCE them to 0 —
	// the same "unlocated" convention the pipeline's UpsertSDR uses —
	// so a manually inserted NULL row cannot 500 the API.
	query := `
		SELECT id, model, COALESCE(serial,''), COALESCE(lat,0), COALESCE(lon,0), gain_db, freq_hz, active
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
	// Same COALESCE convention as GetSDR (§12.1 NULL-allowed lat/lon).
	query := `
		SELECT id, model, COALESCE(serial,''), COALESCE(lat,0), COALESCE(lon,0), gain_db, freq_hz, active
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

// SetSDRActive flags one SDR active/inactive (§14.4.3): the
// processor marks configured SDRs inactive after a silence threshold
// and re-activates them when frames return.
func (d *DB) SetSDRActive(ctx context.Context, id string, active bool) error {
	_, err := d.Pool.Exec(ctx, `UPDATE sdrs SET active = $2 WHERE id = $1`, id, active)
	if err != nil {
		return fmt.Errorf("db: set sdr active: %w", err)
	}
	return nil
}

// DeleteSignal removes a signal by ID. The sweep path does NOT use
// this (it deactivates, §11.2); deletion is reserved for the
// §11.2 archive purge.
func (d *DB) DeleteSignal(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM signals WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: delete signal: %w", err)
	}
	return nil
}

// DeactivateSignal flags one signal inactive (§11.2). The row is
// preserved for history; missing rows are not an error.
func (d *DB) DeactivateSignal(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE signals SET active = FALSE WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: deactivate signal: %w", err)
	}
	return nil
}

// DeactivateAllSignals flags every active signal inactive. Called at
// processor startup: rows left active by a previous process would
// otherwise linger as zombies (§11.2); live signals are re-activated
// by their next upsert. Returns the number of rows deactivated.
func (d *DB) DeactivateAllSignals(ctx context.Context) (int64, error) {
	tag, err := d.Pool.Exec(ctx, `UPDATE signals SET active = FALSE WHERE active`)
	if err != nil {
		return 0, fmt.Errorf("db: deactivate all signals: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkVerified latches a signal's verified flag (§8). The flag is
// sticky: once TRUE it survives re-observations (the UpsertSignal
// conflict clause ORs it back in). Idempotent; missing rows are a
// no-op.
func (d *DB) MarkVerified(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE signals SET verified = TRUE WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: mark verified: %w", err)
	}
	return nil
}

// InsertVerification records one two-SDR cross-check result (§8) as
// evidence attached to a signal row. The id is generated by the
// database.
func (d *DB) InsertVerification(ctx context.Context, v *Verification) error {
	query := `
		INSERT INTO verifications (signal_id, sdr1_id, sdr2_id, verified, confidence)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := d.Pool.Exec(ctx, query, v.SignalID, v.SDR1, v.SDR2, v.Verified, v.Confidence)
	if err != nil {
		return fmt.Errorf("db: insert verification: %w", err)
	}
	return nil
}

// InsertRecording inserts a recording entry. An empty ID lets the
// database generate the UUID (gen_random_uuid(), core in PG13+).
func (d *DB) InsertRecording(ctx context.Context, r *Recording) error {
	query := `
		INSERT INTO recordings (id, signal_id, start_time, end_time, duration_s,
			sample_rate, center_freq, bandwidth_hz, file_path, file_format, size_bytes)
		VALUES (COALESCE($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	var idArg any
	if r.ID != "" {
		idArg = r.ID
	}
	_, err := d.Pool.Exec(ctx, query,
		idArg, r.SignalID, r.StartTime, r.EndTime, r.DurationS,
		r.SampleRate, r.CenterFreq, r.BandwidthHz, r.FilePath, r.FileFormat, r.SizeBytes,
	)
	return err
}

// ListRecordings returns recordings, newest first, optionally filtered
// by signal (empty signalID = all), capped at limit (<=0 → 200).
func (d *DB) ListRecordings(ctx context.Context, signalID string, limit int) ([]Recording, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `
		SELECT id, COALESCE(signal_id::TEXT, ''), start_time, end_time,
			COALESCE(duration_s, 0), COALESCE(sample_rate, 0), COALESCE(center_freq, 0),
			COALESCE(bandwidth_hz, 0), file_path, COALESCE(file_format, ''), COALESCE(size_bytes, 0)
		FROM recordings
		WHERE ($1 = '' OR signal_id::TEXT = $1)
		ORDER BY start_time DESC
		LIMIT $2
	`
	rows, err := d.Pool.Query(ctx, query, signalID, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list recordings: %w", err)
	}
	defer rows.Close()

	var out []Recording
	for rows.Next() {
		var r Recording
		var end *time.Time
		if err := rows.Scan(&r.ID, &r.SignalID, &r.StartTime, &end, &r.DurationS,
			&r.SampleRate, &r.CenterFreq, &r.BandwidthHz, &r.FilePath, &r.FileFormat, &r.SizeBytes); err != nil {
			return nil, fmt.Errorf("db: scan recording: %w", err)
		}
		if end != nil {
			r.EndTime = *end
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecording removes a recording row by ID (§11.2 archive
// purge). Returns ErrNotFound when the row does not exist.
func (d *DB) DeleteRecording(ctx context.Context, id string) error {
	tag, err := d.Pool.Exec(ctx, `DELETE FROM recordings WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: delete recording: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeInactiveSignals hard-deletes signals that have been inactive
// for longer than olderThan (§11/D6: the TTL deactivates, the 30-day
// archive purge removes). Verifications and tracks cascade; recordings
// survive with signal_id set NULL. Returns the number of signals
// removed.
func (d *DB) PurgeInactiveSignals(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM signals WHERE NOT active AND last_seen < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("db: purge inactive signals: %w", err)
	}
	return tag.RowsAffected(), nil
}
