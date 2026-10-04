// Package db — live-Postgres integration tests for the signal
// lifecycle queries (§11.2). Skipped unless TEST_DATABASE_URL is set,
// so default CI (no database) is unaffected:
//
//	docker compose up -d db
//	TEST_DATABASE_URL='postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable' \
//	  go test ./internal/db/ -run TestIntegration -v
//
// WARNING: the test truncates signals/sdrs in the target database —
// point TEST_DATABASE_URL at a disposable database, not production.
package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// integrationPool connects to the TEST_DATABASE_URL database and
// resets the signal/sdr tables for an isolated run.
func integrationPool(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live-database integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)
	// Isolate: clear previous state (CASCADE covers referencing tables).
	if _, err := d.Pool.Exec(ctx, `TRUNCATE signals, sdrs CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Fixture SDR — signals.sdr_id references sdrs(id).
	sdr := &SDRDevice{ID: "test-sdr", Model: "Test SDR", Serial: "0001", GainDB: 40, FreqHz: 146520000, Active: true}
	if err := d.UpsertSDR(ctx, sdr); err != nil {
		t.Fatalf("upsert sdr fixture: %v", err)
	}
	return d
}

func integrationSignal(id string, method string) *Signal {
	now := time.Now()
	return &Signal{
		ID:          id,
		FreqHz:      146_520_000,
		BandwidthHz: 12_500,
		Modulation:  "FM",
		SubType:     "NFM",
		Class:       "land_mobile",
		Method:      method,
		Confidence:  0.9,
		PowerDBM:    -50,
		FirstSeen:   now,
		LastSeen:    now,
		SDRID:       "test-sdr",
		Active:      true,
	}
}

// TestIntegrationAnnotations covers the §12.5 annotation queries:
// round-trip, newest-first ordering, unknown-signal ErrNotFound, and
// the empty-list case.
func TestIntegrationAnnotations(t *testing.T) {
	d := integrationPool(t)
	ctx := context.Background()
	id := "22222222-2222-2222-2222-222222222222"

	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	a1, err := d.AddAnnotation(ctx, id, "first note")
	if err != nil {
		t.Fatalf("add annotation 1: %v", err)
	}
	if a1.ID == "" || a1.SignalID != id || a1.UserNote != "first note" {
		t.Fatalf("returned annotation = %+v", a1)
	}

	// Ensure created_at differs so the DESC ordering is meaningful.
	time.Sleep(10 * time.Millisecond)
	if _, err := d.AddAnnotation(ctx, id, "second note"); err != nil {
		t.Fatalf("add annotation 2: %v", err)
	}

	notes, err := d.GetAnnotations(ctx, id)
	if err != nil {
		t.Fatalf("get annotations: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("want 2 annotations, got %d", len(notes))
	}
	if notes[0].UserNote != "second note" || notes[1].UserNote != "first note" {
		t.Fatalf("newest-first ordering violated: %+v", notes)
	}

	// Unknown signal ⇒ ErrNotFound (⇒ 404 at the API).
	if _, err := d.AddAnnotation(ctx, "99999999-9999-9999-9999-999999999999", "ghost"); err != ErrNotFound {
		t.Fatalf("unknown signal: err = %v, want ErrNotFound", err)
	}

	// A known-shaped but note-less signal yields an empty list.
	empty, err := d.GetAnnotations(ctx, "88888888-8888-8888-8888-888888888888")
	if err != nil {
		t.Fatalf("get annotations (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want 0 annotations, got %d", len(empty))
	}
}

func TestIntegrationSignalActiveLifecycle(t *testing.T) {
	d := integrationPool(t)
	ctx := context.Background()
	id := "11111111-1111-1111-1111-111111111111"

	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sigs, err := d.GetSignals(ctx, -90, -180, 90, 180)
	if err != nil {
		t.Fatalf("get signals: %v", err)
	}
	if len(sigs) != 1 || !sigs[0].Active {
		t.Fatalf("expected 1 active signal after upsert, got %+v", sigs)
	}

	// Retire: hidden from the live view, preserved in the table.
	if err := d.DeactivateSignal(ctx, id); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if sigs, _ = d.GetSignals(ctx, -90, -180, 90, 180); len(sigs) != 0 {
		t.Fatalf("expected 0 active signals after deactivate, got %+v", sigs)
	}
	got, err := d.GetSignal(ctx, id)
	if err != nil {
		t.Fatalf("retired row must remain queryable: %v", err)
	}
	if got.Active {
		t.Error("retired row still flagged active")
	}

	// A re-observation re-activates the row (§11.2).
	if err := d.UpsertSignal(ctx, integrationSignal(id, "onnx")); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if sigs, _ = d.GetSignals(ctx, -90, -180, 90, 180); len(sigs) != 1 {
		t.Fatalf("expected signal re-activated by upsert, got %d", len(sigs))
	}
}

func TestIntegrationMethodPersisted(t *testing.T) {
	d := integrationPool(t)
	ctx := context.Background()
	id := "22222222-2222-2222-2222-222222222222"

	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := d.GetSignal(ctx, id)
	if err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if got.Method != "rules" {
		t.Errorf("method = %q, want rules", got.Method)
	}

	// A later observation with a different classifier refreshes method.
	if err := d.UpsertSignal(ctx, integrationSignal(id, "onnx")); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err = d.GetSignal(ctx, id)
	if err != nil {
		t.Fatalf("re-get signal: %v", err)
	}
	if got.Method != "onnx" {
		t.Errorf("method = %q, want onnx after re-upsert", got.Method)
	}
}

func TestIntegrationDeactivateAll(t *testing.T) {
	d := integrationPool(t)
	ctx := context.Background()
	ids := []string{
		"33333333-3333-3333-3333-333333333331",
		"33333333-3333-3333-3333-333333333332",
	}
	for _, id := range ids {
		if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	n, err := d.DeactivateAllSignals(ctx)
	if err != nil {
		t.Fatalf("deactivate all: %v", err)
	}
	if n != int64(len(ids)) {
		t.Errorf("deactivated %d rows, want %d", n, len(ids))
	}
	if sigs, _ := d.GetSignals(ctx, -90, -180, 90, 180); len(sigs) != 0 {
		t.Errorf("expected 0 active signals after DeactivateAllSignals, got %d", len(sigs))
	}
	for _, id := range ids {
		if _, err := d.GetSignal(ctx, id); err != nil {
			t.Errorf("row %s must be preserved after reconciliation: %v", id, err)
		}
	}
}

func TestIntegrationActivePartialIndex(t *testing.T) {
	// SQL assertion: the partial index from init.sql / migration 001
	// exists (live views and sweep depend on it).
	d := integrationPool(t)
	ctx := context.Background()
	var n int
	err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'signals' AND indexname = 'idx_signals_active'`,
	).Scan(&n)
	if err != nil {
		t.Fatalf("index lookup: %v", err)
	}
	if n != 1 {
		t.Errorf("idx_signals_active exists = %v, want true (apply db/migrations/001_signals_active.sql)", n == 1)
	}
}

func TestIntegrationVerification(t *testing.T) {
	// §8: verification evidence rows are recorded per signal and the
	// signals.verified flag is sticky across re-observations.
	d := integrationPool(t)
	ctx := context.Background()

	partner := &SDRDevice{ID: "test-sdr-2", Model: "Test SDR 2", Serial: "0002", GainDB: 40, FreqHz: 146_520_000, Active: true}
	if err := d.UpsertSDR(ctx, partner); err != nil {
		t.Fatalf("upsert partner sdr: %v", err)
	}

	id := "22222222-2222-2222-2222-222222222222"
	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// An unverified re-observation must not set the flag.
	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if s, err := d.GetSignal(ctx, id); err != nil {
		t.Fatalf("get: %v", err)
	} else if s.Verified {
		t.Fatalf("verified = true before any verification, want false")
	}

	// The two-SDR verifier flips the flag and records the evidence.
	if err := d.MarkVerified(ctx, id); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	ver := &Verification{SignalID: id, SDR1: "test-sdr", SDR2: "test-sdr-2", Verified: true, Confidence: 0.9}
	if err := d.InsertVerification(ctx, ver); err != nil {
		t.Fatalf("insert verification: %v", err)
	}

	// Latch (§8): a later unverified re-observation cannot clear the
	// flag, while classification fields still refresh.
	if err := d.UpsertSignal(ctx, integrationSignal(id, "onnx")); err != nil {
		t.Fatalf("post-verified upsert: %v", err)
	}
	s, err := d.GetSignal(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !s.Verified {
		t.Error("verified flag cleared by re-observation; want sticky latch")
	}
	if s.Method != "onnx" {
		t.Errorf("method = %q, want onnx (re-observation must still refresh)", s.Method)
	}

	// Exactly one evidence row landed, pointing at the signal.
	var n int
	err = d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM verifications
		 WHERE signal_id = $1 AND verified AND sdr1_id = 'test-sdr' AND sdr2_id = 'test-sdr-2'`,
		id).Scan(&n)
	if err != nil {
		t.Fatalf("verification lookup: %v", err)
	}
	if n != 1 {
		t.Errorf("verification rows = %d, want 1", n)
	}
}

func TestIntegrationRecordingsAndPurge(t *testing.T) {
	// §10.2/§11: recording rows round-trip and the archive purge
	// removes long-inactive signals while recordings survive (their
	// signal_id becomes NULL).
	d := integrationPool(t)
	ctx := context.Background()

	id := "33333333-3333-3333-3333-333333333333"
	if err := d.UpsertSignal(ctx, integrationSignal(id, "rules")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	start := time.Now().Add(-time.Minute)
	rec := &Recording{
		SignalID:    id,
		StartTime:   start,
		EndTime:     start.Add(30 * time.Second),
		DurationS:   30,
		SampleRate:  48000,
		CenterFreq:  146_520_000,
		BandwidthHz: 12_500,
		FilePath:    "/recordings/test-33333333.wav",
		FileFormat:  "wav",
		SizeBytes:   2880044,
	}
	if err := d.InsertRecording(ctx, rec); err != nil {
		t.Fatalf("insert recording: %v", err)
	}

	rows, err := d.ListRecordings(ctx, id, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("recordings = %d, want 1", len(rows))
	}
	if rows[0].FilePath != rec.FilePath || rows[0].SizeBytes != rec.SizeBytes ||
		rows[0].SignalID != id || rows[0].SampleRate != 48000 {
		t.Errorf("recording round-trip mismatch: %+v", rows[0])
	}

	// A long-inactive sibling signal with its own recording.
	stale := "44444444-4444-4444-4444-444444444444"
	if err := d.UpsertSignal(ctx, integrationSignal(stale, "rules")); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`UPDATE signals SET active = FALSE, last_seen = now() - interval '40 days' WHERE id = $1`, stale); err != nil {
		t.Fatalf("age stale signal: %v", err)
	}
	staleRec := &Recording{SignalID: stale, StartTime: start, FilePath: "/recordings/stale.wav",
		FileFormat: "wav", SizeBytes: 1000}
	if err := d.InsertRecording(ctx, staleRec); err != nil {
		t.Fatalf("insert stale recording: %v", err)
	}

	n, err := d.PurgeInactiveSignals(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d signals, want 1", n)
	}
	if _, err := d.GetSignal(ctx, stale); err != ErrNotFound {
		t.Errorf("stale signal survived purge: err=%v, want ErrNotFound", err)
	}
	if _, err := d.GetSignal(ctx, id); err != nil {
		t.Errorf("active signal must survive purge: %v", err)
	}
	// The stale recording survives with a detached signal reference (§11).
	srows, err := d.ListRecordings(ctx, "", 0)
	if err != nil {
		t.Fatalf("list after purge: %v", err)
	}
	if len(srows) != 2 {
		t.Fatalf("recordings after purge = %d, want 2 (rows survive the purge)", len(srows))
	}

	// Delete by ID.
	if err := d.DeleteRecording(ctx, rows[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := d.DeleteRecording(ctx, rows[0].ID); err != ErrNotFound {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestIntegrationSDRNullLatLon(t *testing.T) {
	// §12.1 allows NULL lat/lon on sdrs; reads must COALESCE them to
	// 0 (the UpsertSDR "unlocated" convention) instead of failing the
	// scan — a manual NULL row must not 500 GET /api/sdrs or the
	// PUT /api/sdrs/{id} retune path (§13.1).
	d := integrationPool(t)
	ctx := context.Background()

	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO sdrs (id, model, gain_db, freq_hz, active) VALUES ('null-loc-sdr', 'Simulator', 40, 0, true)`); err != nil {
		t.Fatalf("insert null-lat sdr: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM sdrs WHERE id = 'null-loc-sdr'`)
	})

	got, err := d.GetSDR(ctx, "null-loc-sdr")
	if err != nil {
		t.Fatalf("get sdr with NULL lat/lon: %v", err)
	}
	if got.Lat != 0 || got.Lon != 0 {
		t.Fatalf("lat/lon = %v/%v, want 0/0 (unlocated convention)", got.Lat, got.Lon)
	}

	sdrs, err := d.ListSDRs(ctx)
	if err != nil {
		t.Fatalf("list sdrs with NULL lat/lon row: %v", err)
	}
	found := false
	for _, s := range sdrs {
		if s.ID == "null-loc-sdr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("null-loc-sdr missing from ListSDRs: %+v", sdrs)
	}
}
