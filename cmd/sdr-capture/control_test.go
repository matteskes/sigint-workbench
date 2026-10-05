package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestScanLoop_ResumesFromParkedFrequency covers the §7.4 resume
// semantics: while parked, the sweep cursor tracks manual tunes, so
// resuming continues from the currently tuned frequency instead of a
// stale pre-park cursor.
func TestScanLoop_ResumesFromParkedFrequency(t *testing.T) {
	dev := &fakeSDR{}
	slot := newTestSlot(dev) // freqHz 100.0 MHz, sweep 100.0–100.3 MHz
	slot.scanPaused = true   // boot-parked (scan_autostart: false)
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanLoop(slot, exit)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	if got := dev.tuned(); len(got) != 0 {
		t.Fatalf("parked loop tuned %v, want none", got)
	}

	// Manual tune while parked (§7.4): parks idempotently and moves
	// the device to 100.2 MHz.
	if err := slot.setFrequency(100.2); err != nil {
		t.Fatalf("manual tune: %v", err)
	}

	// Resume: the sweep must continue from 100.2 → 100.3 MHz, not
	// step from the stale 100.0 MHz cursor (which would tune 100.1).
	slot.setScanEnabled(true)
	time.Sleep(30 * time.Millisecond)
	close(exit)
	<-done

	// tuned()[0] is the manual setFrequency above; everything after
	// it belongs to the resumed sweep.
	tuned := dev.tuned()
	if len(tuned) < 2 {
		t.Fatalf("resume did not tune (got %v)", tuned)
	}
	if tuned[1] != 100_300_000 {
		t.Errorf("first tune after resume = %d, want 100300000 (next after parked 100.2 MHz)", tuned[1])
	}
}

// postScan POSTs {"id","enabled"} to the control API under test.
func postScan(t *testing.T, url, id, enabled string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/api/v1/scan", "application/json",
		strings.NewReader(`{"id":"`+id+`","enabled":`+enabled+`}`))
	if err != nil {
		t.Fatalf("post scan: %v", err)
	}
	return resp
}

func decodeScanResponse(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestScanEndpoint_ParkResume exercises POST /api/v1/scan against a
// controlMux with a fake device: park is idempotent, resume un-parks,
// and the reply echoes the device status incl. scan_paused (§7.4).
func TestScanEndpoint_ParkResume(t *testing.T) {
	dev := &fakeSDR{}
	slot := newTestSlot(dev)
	slot.cfg.Mode = "both"

	ts := httptest.NewServer(controlMux([]*sdrSlot{slot}))
	defer ts.Close()

	// The endpoint only flips slot state; the sweep itself needs the
	// loop goroutine running (as in the real service).
	exit := make(chan struct{})
	defer close(exit)
	go scanLoop(slot, exit)

	// Park.
	resp := postScan(t, ts.URL, "fake-0", "false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("park status = %d, want 200", resp.StatusCode)
	}
	out := decodeScanResponse(t, resp)
	if out["ok"] != true || out["scanning"] != true || out["scan_paused"] != true {
		t.Fatalf("park reply = %v, want ok/scanning/scan_paused = true", out)
	}

	// Park again: idempotent.
	resp = postScan(t, ts.URL, "fake-0", "false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-park status = %d, want 200", resp.StatusCode)
	}
	decodeScanResponse(t, resp)

	// Resume.
	resp = postScan(t, ts.URL, "fake-0", "true")
	out = decodeScanResponse(t, resp)
	if out["scan_paused"] != false {
		t.Fatalf("resume reply = %v, want scan_paused false", out)
	}

	// The sweep actually restarts: the fake device gets tuned.
	deadline := time.Now().Add(time.Second)
	for len(dev.tuned()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if len(dev.tuned()) == 0 {
		t.Error("resumed scan loop never tuned the device")
	}
}

// TestScanEndpoint_Errors covers the §7.4 error surface: unknown id →
// 404, device without a scan loop → 409, GET → 405, bad JSON → 400.
func TestScanEndpoint_Errors(t *testing.T) {
	dev := &fakeSDR{}
	scanner := newTestSlot(dev)
	scanner.cfg.ID = "scan-0"
	scanner.cfg.Mode = "both"
	monitor := newTestSlot(&fakeSDR{})
	monitor.cfg.ID = "mon-0"
	monitor.cfg.Mode = "monitor"
	monitor.scanning = false

	ts := httptest.NewServer(controlMux([]*sdrSlot{scanner, monitor}))
	defer ts.Close()

	if resp := postScan(t, ts.URL, "nope", "true"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id: status = %d, want 404", resp.StatusCode)
		resp.Body.Close()
	}
	if resp := postScan(t, ts.URL, "mon-0", "true"); resp.StatusCode != http.StatusConflict {
		t.Errorf("non-scanner: status = %d, want 409", resp.StatusCode)
		resp.Body.Close()
	}
	if resp, err := http.Get(ts.URL + "/api/v1/scan"); err != nil {
		t.Errorf("get: %v", err)
	} else if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want 405", resp.StatusCode)
		resp.Body.Close()
	}
	if resp, err := http.Post(ts.URL+"/api/v1/scan", "application/json",
		strings.NewReader(`{`)); err != nil {
		t.Errorf("bad json post: %v", err)
	} else if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", resp.StatusCode)
		resp.Body.Close()
	}
}
