// Package api — requireDB liveness (§13: DB down ⇒ 503 on every /api
// route, including a database that dies after startup, which used to
// surface as handler-level 500s). The dead-pool cases are hermetic —
// a lazy pool aimed at a closed port — while the healthy-path probe
// is gated on TEST_DATABASE_URL like the db integration tests.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"sigint-workbench/internal/db"
)

// deadPoolURL points at a port nothing listens on: pgxpool.New is
// lazy, so only requireDB's probe dials it.
const deadPoolURL = "postgres://sigint:sigint@127.0.0.1:1/sigint"

func newDBProbeServer(database *db.DB) (*Server, *httptest.Server) {
	srv := &Server{
		router: chi.NewRouter(),
		log:    zerolog.Nop(),
		db:     database,
	}
	srv.buildRoutes()
	return srv, httptest.NewServer(srv.Handler())
}

func TestRequireDBDeadPoolAnswers503(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), deadPoolURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	srv, ts := newDBProbeServer(&db.DB{Pool: pool})
	defer ts.Close()

	// First hit probes the pool and answers 503 (§13).
	resp, err := http.Get(ts.URL + "/api/signals")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first hit status = %d, want 503", resp.StatusCode)
	}

	// The failed probe is cached for dbProbeTTL: an immediate second
	// hit 503s without re-dialing the dead port.
	resp, err = http.Get(ts.URL + "/api/signals")
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("cached hit status = %d, want 503", resp.StatusCode)
	}

	// Past the TTL the probe is retried — and still 503s while the
	// database stays down, instead of degrading to handler 500s.
	srv.dbProbeMu.Lock()
	srv.dbProbeAt = time.Now().Add(-dbProbeTTL)
	srv.dbProbeMu.Unlock()
	resp, err = http.Get(ts.URL + "/api/signals")
	if err != nil {
		t.Fatalf("post-TTL get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("post-TTL status = %d, want 503", resp.StatusCode)
	}
}

func TestRequireDBNilPoolAnswers503(t *testing.T) {
	_, ts := newDBProbeServer(&db.DB{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/signals")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// Gated live-DB probe: a healthy pool passes requireDB.
func TestRequireDBLiveDBPasses(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live-database integration test")
	}
	d, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer d.Close()
	_, ts := newDBProbeServer(d)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/signals")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
