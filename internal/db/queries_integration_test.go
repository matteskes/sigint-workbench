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
