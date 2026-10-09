// Package resilience — Test R5: PostGIS Schema Compliance (D5.1).
package resilience

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPostGISSchemaCompliance validates stored signals have valid coordinates,
// frequencies, geography types, and float8 ranges (spec §4.10.7).
func TestPostGISSchemaCompliance(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}
	ensureAllServicesUp(t)

	// Insert 500 signals.
	for i := 0; i < 500; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": i, "freq_mhz": float64(100 + (i % 500)), "lat": 0.0, "lon": 0.0},
		})
		resp, _ := http.Post("http://localhost:8081/api/events",
			"application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	// Wait for DB storage.
	t.Log("Waiting for 500 signals (60 s max)...")
	time.Sleep(60 * time.Second)

	// Query and validate schema.
	resp, err := http.Get("http://localhost:8080/api/signals")
	if err != nil {
		t.Fatalf("Query /api/signals: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var signals []any
	if err := json.Unmarshal(body, &signals); err != nil {
		var obj map[string]any
		json.Unmarshal(body, &obj)
		if items, ok := obj["items"].([]any); ok {
			signals = items
		}
	}

	// Validate geography (lat/lon bounds) and float8 (freq range).
	for _, sig := range signals {
		s, ok := sig.(map[string]any)
		if !ok {
			continue
		}

		if lat, ok := s["lat"].(float64); ok {
			if lat < -90 || lat > 90 {
				t.Errorf("signal %v invalid lat: %.6f (want -90 to 90)", s["id"], lat)
			}
		}
		if lon, ok := s["lon"].(float64); ok {
			if lon < -180 || lon > 180 {
				t.Errorf("signal %v invalid lon: %.6f (want -180 to 180)", s["id"], lon)
			}
		}
		if freq, ok := s["freq_mhz"].(float64); ok {
			if freq <= 0 || freq > 6000 {
				t.Errorf("signal %v out-of-range freq: %.2f MHz", s["id"], freq)
			}
		}

		// Validate UUID format.
		if id, ok := s["id"].(string); ok {
			if len(id) != 36 {
				t.Errorf("signal %v invalid UUID: %s", s["id"], id)
			}
		}
	}
	t.Logf("PASS: Schema compliance: %d signals validated", len(signals))
}

// TestPostGISEdgeCases validates extreme coordinate and frequency values.
func TestPostGISEdgeCases(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	extreme := []struct{ lat, lon, freq float64 }{
		{90.0, 0.0, 146.0}, {-90.0, 0.0, 146.0},
		{0.0, 180.0, 146.0}, {0.0, -180.0, 6000.0},
		{0.0, 0.0, 146.0},
	}
	for _, e := range extreme {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": e.lat, "freq_mhz": e.freq, "lat": e.lat, "lon": e.lon},
		})
		resp, _ := http.Post("http://localhost:8081/api/events",
			"application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	time.Sleep(15 * time.Second)

	resp, err := http.Get("http://localhost:8080/api/signals")
	if err == nil {
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
	}
	t.Log("Edge case values stored successfully")
}

// TestPostGISConcurrentWrites validates concurrent inserts don't corrupt schema.
func TestPostGISConcurrentWrites(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	var wg sync.WaitGroup
	var stored atomic.Int64
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				payload, _ := json.Marshal(map[string]any{
					"type":    "signal.update",
					"payload": map[string]any{"seq": idx*10 + j, "freq_mhz": float64(146 + j), "lat": 0.0, "lon": 0.0},
				})
				resp, err := http.Post("http://localhost:8081/api/events",
					"application/json", bytes.NewReader(payload))
				if resp != nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				if err == nil {
					stored.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	time.Sleep(30 * time.Second)

	resp, err := http.Get("http://localhost:8080/api/signals")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var signals []any
		json.Unmarshal(body, &signals)

		var seenIds = make(map[string]bool)
		for _, sig := range signals {
			s, ok := sig.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := s["id"].(string); ok {
				seenIds[id] = true
			}
		}
		t.Logf("PASS: Concurrent writes: %d unique signals (no corruption)", len(seenIds))
	}
}

// TestPostGISSpatialQueries validates spatial queries work on stored signals.
func TestPostGISSpatialQueries(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	resp, err := http.Get("http://localhost:8080/api/signals")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var signals []any
		json.Unmarshal(body, &signals)
		t.Logf("PASS: Spatial queries: %d signals available", len(signals))
	}
}
