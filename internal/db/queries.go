// Package db — SQL queries.
package db

import (
	"context"
	"fmt"
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