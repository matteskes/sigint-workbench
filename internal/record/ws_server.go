package record

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// WSAudioHandler serves the recorder side of the live audio transport
// (§10.4, normative): GET /ws/audio?signal=<uuid>. On connect it
// sends exactly one text JSON hello (audio.meta), then binary
// messages only — exactly one Opus packet per message — and closes
// with code 1000 when the signal's live stream ends. The gateway
// relay (§3) passes frames through untouched.
//
// The handler is internal-only (compose §3.2): the recorder is never
// published; the api-gateway relays /ws/audio to ws://recorder:9012.
func WSAudioHandler(st *Streamer) http.Handler {
	return &wsAudioServer{st: st, grace: 3 * time.Second, log: log.Default()}
}

type wsAudioServer struct {
	st    *Streamer
	grace time.Duration // how long to wait for a stream to appear
	log   *log.Logger
}

var upgrader = websocket.Upgrader{
	// The gateway relays browser connections transparently; the
	// Origin check belongs to the api-gateway, not this internal hop.
	CheckOrigin: func(*http.Request) bool { return true },
}

// writeTimeout bounds each WebSocket write; a stuck client must never
// wedge the packet pump.
const writeTimeout = 5 * time.Second

func (s *wsAudioServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws/audio" || r.Method != http.MethodGet {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sigID := r.URL.Query().Get("signal")
	if sigID == "" {
		http.Error(w, "missing signal parameter", http.StatusBadRequest)
		return
	}
	if _, err := uuid.Parse(sigID); err != nil {
		http.Error(w, "signal must be a UUID", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	s.serveConn(conn, sigID)
}

func (s *wsAudioServer) serveConn(conn *websocket.Conn, sigID string) {
	defer conn.Close()

	// Subscribe (waiting briefly for the stream to appear — the WAV
	// session opens on the next in-band IQ frame, which may be a few
	// hundred ms away on a fresh signal).
	deadline := time.Now().Add(s.grace)
	pktC, meta, err := s.st.Subscribe(sigID)
	for err != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		pktC, meta, err = s.st.Subscribe(sigID)
	}
	if err != nil {
		// No stream: normal close (§10.4.4 — stream end semantics).
		closeWS(conn, websocket.CloseNormalClosure, "no live stream")
		return
	}

	// §10.4.1: exactly one text hello, then binary only.
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	hello, _ := json.Marshal(meta)
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		s.st.Unsubscribe(sigID, pktC)
		return
	}

	// Read pump: client messages are ignored (audio flows one way);
	// its only job is detecting disconnects and answering pings.
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		conn.SetReadLimit(4096)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Packet pump: binary messages only (§10.4.2), in order.
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	pumping := true
	for pumping {
		select {
		case pkt, open := <-pktC:
			if !open {
				// Stream ended (session finalized): §10.4.4 close 1000.
				closeWS(conn, websocket.CloseNormalClosure, "stream ended")
				pumping = false
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
				pumping = false
			}
		case <-clientGone:
			pumping = false
		case <-ping.C:
			// WriteControl is safe alongside WriteMessage.
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				pumping = false
			}
		}
	}
	s.st.Unsubscribe(sigID, pktC)
	// Drain the read pump so it exits (it unblocks on conn.Close via
	// the deferred Close above).
	<-clientGone
}

// closeWS sends a close frame; failures are ignored (best effort).
func closeWS(conn *websocket.Conn, code int, text string) {
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	msg := websocket.FormatCloseMessage(code, text)
	_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(writeTimeout))
	_ = conn.SetReadDeadline(time.Now().Add(time.Second)) // let the peer echo the close
}
