package record

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestAudioServer spins up an httptest server serving the §10.4
// handler with a short subscribe grace and a fake packetizer.
func newTestAudioServer(t *testing.T) (*Streamer, *httptest.Server) {
	t.Helper()
	withFakePacketizer(t)
	st := NewStreamer(StreamConfig{})
	srv := httptest.NewServer(&wsAudioServer{st: st, grace: 300 * time.Millisecond, log: log.New(io.Discard, "", 0)})
	t.Cleanup(srv.Close)
	return st, srv
}

func dialAudio(t *testing.T, srv *httptest.Server, signalID string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/audio?signal=" + signalID
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial /ws/audio: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readMeta reads the mandatory one-time hello (§10.4.1: text, JSON).
func readMeta(t *testing.T, conn *websocket.Conn) StreamMeta {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("first message type = %d, want text", mt)
	}
	var meta StreamMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("hello is not JSON: %v (%q)", err, data)
	}
	if meta.Type != "audio.meta" {
		t.Fatalf("hello type = %q, want audio.meta", meta.Type)
	}
	return meta
}

// readClose reads until the server closes, asserting close code 1000
// (§10.4.4). Any binary packet seen first is ignored (dropped frame).
func readClose(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue // packets before the close are fine
		}
		ce, ok := err.(*websocket.CloseError)
		if !ok {
			t.Fatalf("expected close frame, got %v", err)
		}
		if ce.Code != websocket.CloseNormalClosure {
			t.Fatalf("close code = %d, want 1000", ce.Code)
		}
		return
	}
}

// TestWSAudioBadRequests: the handler answers plain HTTP errors
// before any upgrade for malformed requests.
func TestWSAudioBadRequests(t *testing.T) {
	_, srv := newTestAudioServer(t)

	resp, err := http.Get(srv.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong path → %d, want 404", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/ws/audio")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing signal → %d, want 400", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/ws/audio?signal=not-a-uuid")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-uuid signal → %d, want 400", resp.StatusCode)
	}
}

// TestWSAudioNoStream pins the no-stream path: hello (or close) is
// followed by a 1000 close — never a hang.
func TestWSAudioNoStream(t *testing.T) {
	_, srv := newTestAudioServer(t)
	conn := dialAudio(t, srv, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	readClose(t, conn)
}

// TestWSAudioPacketsThenStreamEnd pins the normative framing: one
// text hello, then binary-only Opus packets in order, then close 1000
// when the session finalizes.
func TestWSAudioPacketsThenStreamEnd(t *testing.T) {
	st, srv := newTestAudioServer(t)
	const id = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	st.Feed(streamSig(id), make([]float32, 960)) // opens the stream (dropped: no listener yet)
	conn := dialAudio(t, srv, id)
	if meta := readMeta(t, conn); meta.SignalID != id || meta.SampleRate != 48000 ||
		meta.Channels != 1 || meta.Bitrate != 24000 {
		t.Fatalf("hello meta wrong: %+v", meta)
	}

	// Live packets arrive in order.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 1; i <= 2; i++ {
		st.Feed(streamSig(id), make([]float32, 960))
		mt, pkt, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if mt != websocket.BinaryMessage {
			t.Fatalf("message %d type = %d, want binary (§10.4.2)", i, mt)
		}
		if want := fmt.Sprintf("pkt-%03d(960)", i); string(pkt) != want {
			t.Fatalf("packet %d = %q, want %q", i, pkt, want)
		}
	}

	// Session finalize → stream end → close 1000.
	st.CloseStream(id)
	readClose(t, conn)
}

// TestWSAudioTwoClients: §10.4 — one live stream per signal per
// client; every subscriber receives identical packets.
func TestWSAudioTwoClients(t *testing.T) {
	st, srv := newTestAudioServer(t)
	const id = "cccccccc-cccc-cccc-cccc-cccccccccccc"

	st.Feed(streamSig(id), make([]float32, 960)) // stream opens
	c1 := dialAudio(t, srv, id)
	c2 := dialAudio(t, srv, id)
	readMeta(t, c1)
	readMeta(t, c2)

	for i := 0; i < 3; i++ {
		st.Feed(streamSig(id), make([]float32, 960))
		_, p1, err := c1.ReadMessage()
		if err != nil {
			t.Fatalf("client1 packet %d: %v", i, err)
		}
		_, p2, err := c2.ReadMessage()
		if err != nil {
			t.Fatalf("client2 packet %d: %v", i, err)
		}
		if string(p1) != string(p2) {
			t.Fatalf("clients diverged: %q vs %q", p1, p2)
		}
	}

	// One client leaving must not disturb the other.
	c1.Close()
	st.Feed(streamSig(id), make([]float32, 960))
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, p, err := c2.ReadMessage(); err != nil || !strings.Contains(string(p), "(960)") {
		t.Fatalf("client2 lost after client1 left: %q %v", p, err)
	}
}
