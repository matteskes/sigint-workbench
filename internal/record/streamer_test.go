package record

import (
	"fmt"
	"testing"
	"time"

	"sigint-workbench/internal/db"
)

// fakePacketizer stands in for audio.OpusEncoder so the multiplexer
// logic is testable without libopus/cgo.
type fakePacketizer struct{ n int }

func (f *fakePacketizer) EncodeFrame(pcm []float32) ([]byte, error) {
	f.n++
	return fmt.Appendf(nil, "pkt-%03d(%d)", f.n, len(pcm)), nil
}

// failPacketizer models a build without opus (or a broken encoder):
// frames must be dropped, not panic.
type failPacketizer struct{}

func (failPacketizer) EncodeFrame([]float32) ([]byte, error) {
	return nil, fmt.Errorf("no encoder")
}

func streamSig(id string) db.Signal {
	return db.Signal{ID: id, FreqHz: 145_500_000, BandwidthHz: 12_500, Modulation: "FM", SubType: "NFM"}
}

func withFakePacketizer(t *testing.T) {
	t.Helper()
	old := newPacketizer
	newPacketizer = func(StreamConfig) (Packetizer, int, error) { return &fakePacketizer{}, 960, nil }
	t.Cleanup(func() { newPacketizer = old })
}

// TestStreamerSubscribesAndMultiplexes pins §10.4: frames accumulate
// into 960-sample packets, identical packets fan out to every
// subscriber, and the hello meta carries the signal identity.
func TestStreamerSubscribesAndMultiplexes(t *testing.T) {
	withFakePacketizer(t)
	st := NewStreamer(StreamConfig{})
	const id = "11111111-1111-1111-1111-111111111111"

	// Feeding before any subscriber must be dropped (live semantics),
	// not queued for later playback.
	st.Feed(streamSig(id), make([]float32, 500))

	c1, meta, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	c2, meta2, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe 2: %v", err)
	}
	if meta.SignalID != id || meta.Type != "audio.meta" || meta.Modulation != "FM" ||
		meta.SubType != "NFM" || meta.CenterHz != 145_500_000 ||
		meta.SampleRate != 48000 || meta.Channels != 1 || meta.Bitrate != 24000 {
		t.Fatalf("hello meta wrong: %+v", meta)
	}
	if meta2.SignalID != meta.SignalID {
		t.Fatalf("subscribers see different meta: %+v vs %+v", meta, meta2)
	}

	// 600 + 900 = 1500 samples → one 960-sample frame, 540 held.
	st.Feed(streamSig(id), make([]float32, 600))
	st.Feed(streamSig(id), make([]float32, 900))

	p1 := recvPacket(t, c1, time.Second)
	p2 := recvPacket(t, c2, time.Second)
	if string(p1) != string(p2) {
		t.Fatalf("subscribers got different packets: %q vs %q", p1, p2)
	}
	if got, want := string(p1), "pkt-001(960)"; got != want {
		t.Fatalf("packet = %q, want %q", got, want)
	}
	// The 540-sample remainder is not a packet.
	select {
	case p := <-c1:
		t.Fatalf("unexpected extra packet %q", p)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestStreamerCloseStream ends the stream for every subscriber
// (§10.4.4: the server then responds with close 1000).
func TestStreamerCloseStream(t *testing.T) {
	withFakePacketizer(t)
	st := NewStreamer(StreamConfig{})
	const id = "22222222-2222-2222-2222-222222222222"

	if _, _, err := st.Subscribe(id); err == nil {
		t.Fatal("Subscribe must fail while no stream exists")
	}

	st.Feed(streamSig(id), make([]float32, 960)) // creates the stream lazily
	c, _, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	st.Feed(streamSig(id), make([]float32, 960)) // first live packet
	recvPacket(t, c, time.Second)

	st.CloseStream(id)
	if _, open := <-c; open {
		t.Fatal("channel must be closed by CloseStream")
	}
	if _, _, err := st.Subscribe(id); err == nil {
		t.Fatal("Subscribe after CloseStream must fail")
	}
}

// TestStreamerDropOldest pins §10.4.3: a slow client sheds the oldest
// queued packet instead of blocking the feed path.
func TestStreamerDropOldest(t *testing.T) {
	withFakePacketizer(t)
	st := NewStreamer(StreamConfig{})
	const id = "33333333-3333-3333-3333-333333333333"
	st.Feed(streamSig(id), make([]float32, 960)) // stream comes into being
	c, _, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Overflow the 32-slot buffer; Feed must never block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			st.Feed(streamSig(id), make([]float32, 960))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Feed blocked on a full client buffer")
	}
	if got := len(c); got != 32 {
		t.Fatalf("buffer holds %d packets, want 32", got)
	}
}

// TestStreamerFailingEncoder models a build without opus: streams
// still open (hello carries meta), frames are dropped, nothing panics.
func TestStreamerFailingEncoder(t *testing.T) {
	old := newPacketizer
	newPacketizer = func(StreamConfig) (Packetizer, int, error) { return failPacketizer{}, 960, nil }
	t.Cleanup(func() { newPacketizer = old })

	st := NewStreamer(StreamConfig{})
	const id = "44444444-4444-4444-4444-444444444444"
	st.Feed(streamSig(id), make([]float32, 3000))
	c, _, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case p := <-c:
		t.Fatalf("failing encoder produced a packet %q", p)
	case <-time.After(50 * time.Millisecond):
	}
	st.CloseStream(id)
}

// TestStreamerDropsUnlistenedAudio pins the live-stream semantics:
// audio fed while nobody listens is discarded — a subscriber joins
// the stream live and never receives a stale backlog.
func TestStreamerDropsUnlistenedAudio(t *testing.T) {
	withFakePacketizer(t)
	st := NewStreamer(StreamConfig{})
	const id = "55555555-5555-5555-5555-555555555555"

	st.Feed(streamSig(id), make([]float32, 20*48000)) // 20 s, nobody listens

	st.mu.Lock()
	bufLen := len(st.streams[id].pcm)
	st.mu.Unlock()
	if bufLen != 0 {
		t.Fatalf("backlog = %d samples, want 0 (live streams never buffer for absent listeners)", bufLen)
	}

	// Joining afterwards yields only audio from the next feed on.
	c, _, err := st.Subscribe(id)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	st.Feed(streamSig(id), make([]float32, 960))
	recvPacket(t, c, time.Second)
}

func recvPacket(t *testing.T, c <-chan []byte, d time.Duration) []byte {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(d):
		t.Fatalf("timed out waiting for a packet")
		return nil
	}
}
