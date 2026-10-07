package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// newTestServer builds an API server without a database.
func newTestServer() *httptest.Server {
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	ts := httptest.NewServer(srv.Handler())
	return ts
}

// newTestServerWithHub builds an API server wired to the given
// ws-hub address.
func newTestServerWithHub(hubAddr string) *httptest.Server {
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.wsHubAddr = hubAddr
	return httptest.NewServer(srv.Handler())
}

// newTestServerWithUpstreams builds an API server wired to explicit
// ws-hub, recorder and capture-control addresses.
func newTestServerWithUpstreams(hub, recorder, capture string) *httptest.Server {
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.wsHubAddr = hub
	srv.recorderWSAddr = recorder
	srv.captureCtrl = capture
	return httptest.NewServer(srv.Handler())
}

func TestWSRelayForwardsHubEvents(t *testing.T) {
	// Fake ws-hub: pushes one event after the client (relay) connects.
	hubTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"signal.new","payload":{"id":"abc"}}`)); err != nil {
			return
		}
		// Keep the connection open until the relay tears it down.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer hubTS.Close()

	hubURL, err := url.Parse(hubTS.URL)
	if err != nil {
		t.Fatalf("parse hub url: %v", err)
	}
	ts := newTestServerWithHub(hubURL.Host)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer client.Close()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read via relay: %v", err)
	}
	if !strings.Contains(string(msg), "signal.new") {
		t.Fatalf("relayed message = %q, want signal.new event", msg)
	}
}

func TestWSRelayHubUnreachable(t *testing.T) {
	// Closed port: the relay must answer 502 JSON, not a handshake.
	ts := newTestServerWithHub("127.0.0.1:1")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body := make([]byte, 128)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "ws-hub unreachable") {
		t.Fatalf("body = %q, want ws-hub unreachable JSON", body[:n])
	}
}

func TestAnnotationsGetRequiresDatabase(t *testing.T) {
	// DB-down gateway: every /api/* route answers 503 (§13 contract).
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/signals/00000000-0000-0000-0000-000000000000/annotations")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAnnotationsPostValidation(t *testing.T) {
	// The body is validated before the database is consulted, so a
	// nil-DB server can exercise the 400 paths.
	ts := newTestServer()
	defer ts.Close()
	url := ts.URL + "/api/signals/00000000-0000-0000-0000-000000000000/annotations"

	for _, body := range []string{`{}`, `{"userNote":""}`, `{"userNote":"   "}`} {
		resp, err := http.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post %q: %v", body, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("post %q: status = %d, want 400", body, resp.StatusCode)
		}
	}

	// Malformed JSON → 400.
	resp, err := http.Post(url, "application/json", strings.NewReader(`{not json`))
	if err != nil {
		t.Fatalf("post malformed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400", resp.StatusCode)
	}

	// A well-formed body passes validation and reaches the (absent)
	// database → 503.
	resp, err = http.Post(url, "application/json", strings.NewReader(`{"userNote":"bench note"}`))
	if err != nil {
		t.Fatalf("post valid: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("valid body: status = %d, want 503 (validation passed, DB down)", resp.StatusCode)
	}
}

func TestWSRelayRejectsForeignOrigin(t *testing.T) {
	hubTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer hubTS.Close()
	hubURL, _ := url.Parse(hubTS.URL)

	ts := newTestServerWithHub(hubURL.Host)
	defer ts.Close()

	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", header)
	if err == nil {
		t.Fatal("expected dial failure for foreign origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestHealth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSignalsWithoutDB(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	for _, path := range []string{"/api/signals", "/api/signals/abc", "/api/recordings", "/api/sdrs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, resp.StatusCode)
		}
	}
}

func TestUpdateSDRWithoutDB(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/sdrs/rtlsdr-0", strings.NewReader(`{"gainDb":40}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestCORSAllowlistedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want http://localhost:5173", got)
	}
}

func TestCORSRejectsForeignOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", got)
	}

	// Cross-origin preflights get no CORS grant: go-chi/cors aborts
	// them without CORS headers (browsers treat that as a failure).
	pre, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/signals", nil)
	pre.Header.Set("Origin", "http://evil.example")
	pre.Header.Set("Access-Control-Request-Method", http.MethodGet)
	presp, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	presp.Body.Close()
	if got := presp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want none", got)
	}
	if got := presp.Header.Get("Access-Control-Allow-Methods"); got != "" {
		t.Fatalf("preflight Access-Control-Allow-Methods = %q, want none", got)
	}
}

func TestCORSDenyAllWhenEnvEmpty(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none (deny-all)", got)
	}
}

func TestAudioContentType(t *testing.T) {
	cases := map[string]string{
		"wav":  "audio/wav",
		"iq":   "application/octet-stream",
		"flac": "application/octet-stream", // removed format must not resurface
		"":     "application/octet-stream",
	}
	for format, want := range cases {
		if got := audioContentType(format); got != want {
			t.Fatalf("audioContentType(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestUpdateSDRInvalidJSON(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	// Without a DB this should 503 before touching JSON; verify the
	// handler chain responds deterministically rather than panicking.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/sdrs/rtlsdr-0", strings.NewReader(`{"gainDb":`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// --- /ws/audio relay (§2.2, §10.4, slice 5) ---

func TestWSAudioRelayForwardsRecorderStream(t *testing.T) {
	recTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/audio" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("signal"); got != "sig-1" {
			t.Errorf("recorder got signal=%q, want sig-1", got)
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// §10.4 framing: one text hello, then binary Opus packets.
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"audio.meta","signalId":"sig-1","sampleRate":48000}`)); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0xF8, 0xAA, 0xBB}); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer recTS.Close()

	recURL, err := url.Parse(recTS.URL)
	if err != nil {
		t.Fatalf("parse recorder url: %v", err)
	}
	ts := newTestServerWithUpstreams("127.0.0.1:1", recURL.Host, "127.0.0.1:1")
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/audio?signal=sig-1"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial audio relay: %v", err)
	}
	defer client.Close()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))

	mt, msg, err := client.ReadMessage()
	if err != nil || mt != websocket.TextMessage || !strings.Contains(string(msg), "audio.meta") {
		t.Fatalf("first frame = (%v, %q, %v), want text audio.meta hello", mt, msg, err)
	}
	mt, msg, err = client.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || len(msg) != 3 {
		t.Fatalf("second frame = (%v, %v, %v), want 3-byte binary Opus packet", mt, msg, err)
	}
}

// B17 regression (§10.4.4): the recorder's clean close — the 3 s
// "no live stream" grace — must reach the relayed client with code and
// reason intact. The old pump swallowed upstream close frames, so the
// browser saw an abnormal 1006 and rendered the spec'd "ended" state
// as "Live stream error: websocket error".
func TestWSAudioRelayPropagatesUpstreamCloseCode(t *testing.T) {
	recTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// The recorder's closeWS shape (internal/record/ws_server.go):
		// one close frame, then a brief read for the peer's echo.
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "no live stream"),
			time.Now().Add(time.Second))
		conn.SetReadDeadline(time.Now().Add(time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer recTS.Close()

	recURL, err := url.Parse(recTS.URL)
	if err != nil {
		t.Fatalf("parse recorder url: %v", err)
	}
	ts := newTestServerWithUpstreams("127.0.0.1:1", recURL.Host, "127.0.0.1:1")
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/audio?signal=sig-1"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial audio relay: %v", err)
	}
	defer client.Close()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))

	_, _, err = client.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("read = %v, want *websocket.CloseError (relay swallowed the close frame)", err)
	}
	if ce.Code != websocket.CloseNormalClosure || ce.Text != "no live stream" {
		t.Fatalf("close = (%d, %q), want (1000, %q)", ce.Code, ce.Text, "no live stream")
	}
}

// B17 symmetry: a client-side clean close is forwarded upstream with
// its code and reason, so the recorder's sink releases promptly.
func TestWSAudioRelayPropagatesClientCloseCode(t *testing.T) {
	upClose := make(chan websocket.CloseError, 1)
	recTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if ce, ok := err.(*websocket.CloseError); ok {
					upClose <- *ce
				}
				return
			}
		}
	}))
	defer recTS.Close()

	recURL, err := url.Parse(recTS.URL)
	if err != nil {
		t.Fatalf("parse recorder url: %v", err)
	}
	ts := newTestServerWithUpstreams("127.0.0.1:1", recURL.Host, "127.0.0.1:1")
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/audio?signal=sig-1"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial audio relay: %v", err)
	}
	defer client.Close()
	if err := client.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "client hangup"),
		time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write close: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := client.ReadMessage(); err != nil {
			break // relayed close or hangup — either ends the client side
		}
	}

	select {
	case ce := <-upClose:
		if ce.Code != websocket.CloseGoingAway || ce.Text != "client hangup" {
			t.Fatalf("upstream close = (%d, %q), want (1001, %q)", ce.Code, ce.Text, "client hangup")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream never observed the client's close frame")
	}
}

func TestWSAudioRelayRecorderUnreachable(t *testing.T) {
	// Closed port: the relay must answer 502 JSON, not a handshake.
	ts := newTestServerWithUpstreams("127.0.0.1:1", "127.0.0.1:1", "127.0.0.1:1")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ws/audio?signal=sig-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "recorder unreachable") {
		t.Fatalf("body = %q, want recorder unreachable JSON", body)
	}
}

func TestWSAudioRelayRejectsForeignOrigin(t *testing.T) {
	ts := newTestServerWithUpstreams("127.0.0.1:1", "127.0.0.1:1", "127.0.0.1:1")
	defer ts.Close()

	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/audio?signal=sig-1", header)
	if err == nil {
		t.Fatal("expected dial failure for foreign origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// --- control-API proxy (§7.4, §13.1, §13.2.3) ---

func TestRetuneCaptureForwardsFreqAndGain(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	freq := uint64(145_550_000)
	gain := 24.5
	if fail := srv.retuneCapture(context.Background(), "S1", &freq, &gain); fail != nil {
		t.Fatalf("retuneCapture: %v", fail)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want 2", calls)
	}
	if !strings.Contains(calls[0], "/api/v1/frequency") ||
		!strings.Contains(calls[0], `"id":"S1"`) ||
		!strings.Contains(calls[0], `"freq_mhz":145.55`) {
		t.Fatalf("freq call = %q, want S1 at 145.55 MHz", calls[0])
	}
	if !strings.Contains(calls[1], "/api/v1/gain") ||
		!strings.Contains(calls[1], `"gain_db":24.5`) {
		t.Fatalf("gain call = %q, want 24.5 dB", calls[1])
	}
}

func TestRetuneCaptureSkipsMetadataOnly(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	// No freq/gain in the update: nothing must hit the control API.
	if fail := srv.retuneCapture(context.Background(), "S1", nil, nil); fail != nil {
		t.Fatalf("retuneCapture: %v", fail)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("control API calls = %d, want 0 for metadata-only update", calls)
	}
}

func TestRetuneCaptureUnreachable(t *testing.T) {
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = "127.0.0.1:1"

	freq := uint64(100)
	fail := srv.retuneCapture(context.Background(), "S1", &freq, nil)
	if fail == nil || fail.status != http.StatusBadGateway {
		t.Fatalf("fail = %v, want 502 ctrlFailure", fail)
	}
}

func TestRetuneCaptureUnknownID(t *testing.T) {
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unknown SDR id", http.StatusNotFound)
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	freq := uint64(100)
	fail := srv.retuneCapture(context.Background(), "S9", &freq, nil)
	if fail == nil || fail.status != http.StatusNotFound {
		t.Fatalf("fail = %v, want 404 ctrlFailure", fail)
	}
}

func TestCaptureStatusProxiesAndFilters(t *testing.T) {
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"S1","active":true,"freq_hz":145550000},` +
			`{"id":"S2","active":false,"freq_hz":0}]`))
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	got, fail := srv.captureStatus(context.Background(), "S2")
	if fail != nil {
		t.Fatalf("captureStatus: %v", fail)
	}
	if got["id"] != "S2" || got["active"] != false {
		t.Fatalf("status = %v, want the S2 entry", got)
	}
	if _, fail := srv.captureStatus(context.Background(), "S9"); fail == nil ||
		fail.status != http.StatusNotFound {
		t.Fatalf("fail = %v, want 404 for unknown id", fail)
	}
}

func TestGetSDRStatusRequiresDB(t *testing.T) {
	// §13 contract: every /api/* route answers 503 when the DB is
	// down, the live-state proxy included.
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/sdrs/S1/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestScanProxyRequiresDBAndRoute(t *testing.T) {
	// §13 contract for POST /api/sdrs/{id}/scan: a 503 (not chi's
	// 404) proves the route is registered and honors the DB-down
	// convention like every other /api route.
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/sdrs/S1/scan", "application/json",
		strings.NewReader(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPostCaptureControlScanForwardsAndRelays(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotBody string
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath, gotBody = r.URL.Path, string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// Capture replies with the device's live status (§7.4).
		_, _ = w.Write([]byte(`{"id":"S1","ok":true,"scanning":true,"scan_paused":true}`))
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	body, fail := srv.postCaptureControl(context.Background(), "/api/v1/scan",
		captureScanRequest{ID: "S1", Enabled: false})
	if fail != nil {
		t.Fatalf("postCaptureControl: %v", fail)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/v1/scan" {
		t.Errorf("upstream path = %q, want /api/v1/scan", gotPath)
	}
	if !strings.Contains(gotBody, `"id":"S1"`) || !strings.Contains(gotBody, `"enabled":false`) {
		t.Errorf("upstream body = %q, want scan command for S1", gotBody)
	}
	if !strings.Contains(string(body), `"scan_paused":true`) {
		t.Errorf("relayed body = %q, want capture's status reply", body)
	}
}

func TestPostCaptureControlConflictRelayed(t *testing.T) {
	// A 409 from capture means "no scan loop on this device" — the
	// gateway must surface it as 409 with capture's message, not
	// flatten it into a generic 502.
	capTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "device has no scan loop (mode monitor)", http.StatusConflict)
	}))
	defer capTS.Close()
	capURL, _ := url.Parse(capTS.URL)

	log := zerolog.Nop()
	srv := NewServer(nil, log)
	srv.captureCtrl = capURL.Host

	_, fail := srv.postCaptureControl(context.Background(), "/api/v1/scan",
		captureScanRequest{ID: "S1", Enabled: true})
	if fail == nil || fail.status != http.StatusConflict {
		t.Fatalf("fail = %v, want 409 ctrlFailure", fail)
	}
	if !strings.Contains(fail.message, "no scan loop") {
		t.Fatalf("message = %q, want capture's conflict text", fail.message)
	}
}
