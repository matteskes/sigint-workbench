//go:build opus

package record

import (
	"io"
	"log"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWSAudioOpusFrameCounter is the §17.3 §10.4 obligation: with the
// real libopus encoder, one second of 48 kHz mono fed to a live
// signal yields ~50 binary packets (20 ms cadence), each starting
// with the mono/20 ms CELT TOC byte 0xF8 — end-to-end through the
// recorder's /ws/audio server.
func TestWSAudioOpusFrameCounter(t *testing.T) {
	st := NewStreamer(StreamConfig{})
	srv := httptest.NewServer(&wsAudioServer{st: st, grace: time.Second, log: log.New(io.Discard, "", 0)})
	defer srv.Close()

	const id = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	st.Feed(streamSig(id), make([]float32, 960)) // stream opens

	conn := dialAudio(t, srv, id)
	defer conn.Close()

	// hello (§10.4.1)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if mt, data, err := conn.ReadMessage(); err != nil || mt != websocket.TextMessage {
		t.Fatalf("hello: type=%d err=%v", mt, err)
	} else if got := string(data); len(got) == 0 {
		t.Fatal("empty hello")
	}

	// 1 s of 440 Hz sine at 48 kHz, fed in demodulator-sized chunks
	// (odd sizes on purpose: the streamer must re-frame to 20 ms).
	sine := make([]float32, 48000)
	for i := range sine {
		sine[i] = 0.4 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	for off := 0; off < len(sine); off += 1031 {
		end := off + 1031
		if end > len(sine) {
			end = len(sine)
		}
		st.Feed(streamSig(id), sine[off:end])
	}

	packets, toc := 0, byte(0)
	for {
		mt, pkt, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("packet %d: %v", packets, err)
		}
		if mt != websocket.BinaryMessage {
			t.Fatalf("message %d: not binary", packets)
		}
		if packets == 0 {
			toc = pkt[0]
		} else if pkt[0] != toc {
			t.Fatalf("TOC changed mid-stream: %02x -> %02x", toc, pkt[0])
		}
		packets++
		if packets >= 50 {
			break
		}
	}
	if toc != 0xF8 {
		t.Fatalf("TOC byte = %02x, want 0xF8 (mono, 20 ms, CELT config 31)", toc)
	}
	if packets != 50 {
		t.Fatalf("got %d packets for 1 s of audio, want 50 (20 ms cadence)", packets)
	}

	// Stream end → close 1000 (§10.4.4).
	st.CloseStream(id)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			ce, ok := err.(*websocket.CloseError)
			if !ok || ce.Code != websocket.CloseNormalClosure {
				t.Fatalf("close = %v, want code 1000", err)
			}
			break
		}
	}
}
