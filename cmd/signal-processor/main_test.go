package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/ws"
)

// fakeHub records events posted to the ingest endpoint.
type fakeHub struct {
	mu     sync.Mutex
	events []ws.Event
}

func (f *fakeHub) serve() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var ev ws.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.events = append(f.events, ev)
		f.mu.Unlock()
		w.Write([]byte(`{"status":"accepted"}`))
	}))
	return srv
}

func (f *fakeHub) waitCount(n int) int {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		c := len(f.events)
		f.mu.Unlock()
		if c >= n {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func testEvent() signalEvent {
	return signalEvent{
		SDRID:     "rtlsdr-0",
		Timestamp: time.Now(),
		CenterHz:  146_000_000,
		PeakHz:    146_520_000,
		PowerDB:   -20,
		Bandwidth: 25_000,
		Class: &classify.Result{
			Modulation: "am",
			Source:     "aviation",
			Confidence: 0.9,
			Bandwidth:  25_000,
		},
	}
}

func TestPublishNewThenUpdate(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	locs := map[string]sdrLocation{"rtlsdr-0": {Lat: 40.7, Lon: -74.0}}
	p := newPublisher(nil, srv.URL, locs, time.Minute)

	now := time.Now()
	p.publish(testEvent(), now)
	p.publish(testEvent(), now.Add(3*time.Second))

	if n := hub.waitCount(2); n < 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.events[0].Type != "signal.new" {
		t.Fatalf("first event type = %q, want signal.new", hub.events[0].Type)
	}
	if hub.events[1].Type != "signal.update" {
		t.Fatalf("second event type = %q, want signal.update", hub.events[1].Type)
	}
	var sig struct {
		ID    string  `json:"id"`
		Freq  uint64  `json:"freqHz"`
		Mod   string  `json:"modulation"`
		Conf  float64 `json:"confidence"`
		Lat   float64 `json:"lat"`
		SDRID string  `json:"sdrId"`
	}
	if err := json.Unmarshal(hub.events[0].Payload, &sig); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if sig.Freq != 146_520_000 || sig.Mod != "am" || sig.SDRID != "rtlsdr-0" {
		t.Fatalf("unexpected signal payload: %+v", sig)
	}
	if sig.Lat != 40.7 {
		t.Fatalf("lat = %v, want 40.7", sig.Lat)
	}
}

func TestPublishIgnoresUnknownSDR(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, time.Minute)
	p.publish(testEvent(), time.Now())

	time.Sleep(50 * time.Millisecond)
	if hub.waitCount(1) != 0 {
		t.Fatal("expected no events for SDR without location, got some")
	}
}

func TestSweepEmitsRemoved(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	locs := map[string]sdrLocation{"rtlsdr-0": {Lat: 40.7, Lon: -74.0}}
	p := newPublisher(nil, srv.URL, locs, 300*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.sweep(ctx)
	p.publish(testEvent(), time.Now())
	if n := hub.waitCount(1); n < 1 {
		t.Fatalf("expected 1 event, got %d", n)
	}

	// Wait for the sweeper to drop the idle signal (5s tick + 300ms ttl).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		n := len(hub.events)
		lastType := ""
		if n > 0 {
			lastType = hub.events[n-1].Type
		}
		hub.mu.Unlock()
		if n >= 2 && lastType == "signal.removed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("expected signal.removed event after TTL expiry")
}

func TestQueueDropWhenFull(t *testing.T) {
	// Silence expected drop noise.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	p := newPublisher(nil, "http://127.0.0.1:1", map[string]sdrLocation{}, time.Minute)
	// The worker drains to an unreachable endpoint (fast connection
	// refused); this only checks the non-blocking drop behavior.
	for i := 0; i < 300; i++ {
		p.queue("signal.update", map[string]string{"i": "x"})
	}
	// Must not block or panic.
}