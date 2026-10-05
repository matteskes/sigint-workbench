// Package api — §20 setup/settings endpoint tests. The DB-backed
// first-run state test is gated on TEST_DATABASE_URL like the db
// integration tests.
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"sigint-workbench/internal/db"
	"sigint-workbench/internal/settings"
)

// newSettingsTestServer builds a server whose §20 store points at a
// temp config dir pre-seeded with the given files.
func newSettingsTestServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.settings = settings.NewStore(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

const gwCaptureYAML = `sdrs:
  - id: rtlsdr-0
    driver: rtlsdr
    default_freq: 162550000
    default_gain: 32.8
    default_bw: 2400000
    mode: monitor
    stream_host: iq-ingest
    stream_port: 9000
scan:
  step_hz: 100000
  dwell_ms: 50
`

const gwProcessingYAML = `listen_port: 9100
peak_detection:
  threshold_db: -60
  min_spacing_bins: 3
  max_peaks: 8
fft:
  size: 4096
  window: rectangular
spectrum:
  bins: 256
  rate_hz: 5
`

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("bad JSON from %s: %v (%s)", url, err, raw)
	}
	return resp.StatusCode, body
}

func putJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("bad JSON from %s: %v (%s)", url, err, raw)
	}
	return resp.StatusCode, parsed
}

func TestSettingsIndex(t *testing.T) {
	ts := newSettingsTestServer(t, map[string]string{
		"sdr-capture.yaml":      gwCaptureYAML,
		"signal-processor.yaml": gwProcessingYAML,
	})
	status, body := getJSON(t, ts.URL+"/api/settings")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	sections, ok := body["sections"].([]any)
	if !ok || len(sections) != 5 {
		t.Fatalf("sections = %#v", body["sections"])
	}
	values, ok := body["values"].(map[string]any)
	if !ok {
		t.Fatal("values missing")
	}
	capture, ok := values["capture"].(map[string]any)
	if !ok {
		t.Fatal("capture values missing")
	}
	devices, ok := capture["sdrs"].([]any)
	if !ok || len(devices) != 1 {
		t.Fatalf("capture.sdrs = %#v", capture["sdrs"])
	}
	if values["recorder"] != nil {
		t.Fatal("absent recorder file must snapshot to nil")
	}
}

func TestSettingsSectionGet(t *testing.T) {
	ts := newSettingsTestServer(t, map[string]string{"signal-processor.yaml": gwProcessingYAML})
	status, body := getJSON(t, ts.URL+"/api/settings/processing")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	sec, ok := body["section"].(map[string]any)
	if !ok || sec["id"] != "processing" {
		t.Fatalf("section = %#v", body["section"])
	}
	status, _ = getJSON(t, ts.URL+"/api/settings/bogus")
	if status != http.StatusNotFound {
		t.Fatalf("unknown section status = %d", status)
	}
}

func TestSettingsSectionPut(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "signal-processor.yaml")
	if err := os.WriteFile(cfgPath, []byte(gwProcessingYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.settings = settings.NewStore(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	status, body := putJSON(t, ts.URL+"/api/settings/processing",
		`{"values":{"fft.window":"hann"}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %#v", status, body)
	}
	res, ok := body["result"].(map[string]any)
	if !ok || res["file"] != "signal-processor.yaml" {
		t.Fatalf("result = %#v", body["result"])
	}
	restart, ok := res["restart"].([]any)
	if !ok || len(restart) != 1 || restart[0] != "signal-processor" {
		t.Fatalf("restart = %#v", res["restart"])
	}
	out, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(out), "window: hann") {
		t.Fatalf("file not updated:\n%s", out)
	}
	if _, err := os.Stat(cfgPath + ".bak"); err != nil {
		t.Fatal("backup missing after save")
	}
}

func TestSettingsSectionPutErrors(t *testing.T) {
	ts := newSettingsTestServer(t, map[string]string{"signal-processor.yaml": gwProcessingYAML})
	status, body := putJSON(t, ts.URL+"/api/settings/processing",
		`{"values":{"spectrum.bins":300}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	if body["field"] != "spectrum.bins" || body["error"] == "" {
		t.Fatalf("body = %#v, want field+error", body)
	}
	status, _ = putJSON(t, ts.URL+"/api/settings/bogus", `{"values":{}}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown section status = %d", status)
	}
	status, _ = putJSON(t, ts.URL+"/api/settings/processing", `{`)
	if status != http.StatusBadRequest {
		t.Fatalf("bad body status = %d", status)
	}
}

func TestSetupStatusEndpoint(t *testing.T) {
	ts := newSettingsTestServer(t, nil)
	status, body := getJSON(t, ts.URL+"/api/setup/status")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if ok, _ := body["all_ok"].(bool); ok {
		t.Fatal("all_ok must be false with no db or upstreams")
	}
	if ok, _ := body["config_dir_writable"].(bool); !ok {
		t.Fatal("temp config dir should be writable")
	}
	components, ok := body["components"].(map[string]any)
	if !ok || len(components) != 4 {
		t.Fatalf("components = %#v", body["components"])
	}
	dbStatus, ok := components["db"].(map[string]any)
	if !ok {
		t.Fatal("db component missing")
	}
	if ok, _ := dbStatus["ok"].(bool); ok {
		t.Fatal("db must report not-ok without a database")
	}
}

func TestSetupStateWithoutDB(t *testing.T) {
	ts := newSettingsTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/setup/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// requireDB answers plain text (§13), not JSON — status only.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// Gated live-DB test for the §20 first-run flag lifecycle.
func TestSetupStateLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live-database integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer database.Close()
	defer database.Pool.Exec(ctx, "DELETE FROM app_settings WHERE key = $1", setupCompletedKey)

	log := zerolog.Nop()
	srv := NewServer(database, log)
	srv.settings = settings.NewStore(t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	status, body := getJSON(t, ts.URL+"/api/setup/state")
	if status != http.StatusOK || body["first_run"] != true {
		t.Fatalf("initial state = %d %#v", status, body)
	}
	status, body = putJSON(t, ts.URL+"/api/setup/state", `{"first_run":false}`)
	if status != http.StatusOK || body["first_run"] != false {
		t.Fatalf("complete = %d %#v", status, body)
	}
	status, body = getJSON(t, ts.URL+"/api/setup/state")
	if status != http.StatusOK || body["first_run"] != false {
		t.Fatalf("after completion = %d %#v", status, body)
	}
	status, body = putJSON(t, ts.URL+"/api/setup/state", `{"first_run":true}`)
	if status != http.StatusOK || body["first_run"] != true {
		t.Fatalf("re-arm = %d %#v", status, body)
	}
	status, body = getJSON(t, ts.URL+"/api/setup/state")
	if status != http.StatusOK || body["first_run"] != true {
		t.Fatalf("after re-arm = %d %#v", status, body)
	}
	status, _ = putJSON(t, ts.URL+"/api/setup/state", `{}`)
	if status != http.StatusBadRequest {
		t.Fatalf("missing first_run status = %d", status)
	}
}
