package record

import (
	"fmt"
	"sync"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/db"
)

// StreamConfig configures the live Opus stream (§10.4).
type StreamConfig struct {
	SampleRate int // 48000 (§10.4)
	Channels   int // 1 (§10.4)
	BitrateBps int // 24000 CBR (§10.4)
}

// Packetizer turns exactly one frame of PCM into exactly one packet.
// audio.OpusEncoder is the production implementation (§10.4); tests
// inject fakes so the multiplexer logic is testable without cgo.
type Packetizer interface {
	EncodeFrame(pcm []float32) ([]byte, error)
}

// newPacketizer is the encoder factory; swappable in tests.
var newPacketizer = func(cfg StreamConfig) (Packetizer, int, error) {
	enc, err := audio.NewOpusEncoder(cfg.SampleRate, cfg.Channels, cfg.BitrateBps)
	return enc, audio.FrameSamples, err
}

// StreamMeta is the one-time JSON hello sent on every /ws/audio
// connection (§10.4: type audio.meta, then binary Opus packets only).
type StreamMeta struct {
	Type       string `json:"type"`
	SignalID   string `json:"signalId"`
	CenterHz   uint64 `json:"centerHz"`
	Modulation string `json:"modulation"`
	SubType    string `json:"subType"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Bitrate    int    `json:"bitrate"`
}

// ErrNoStream is returned by Subscribe when the signal has no open
// live stream (no active in-band session — yet or anymore).
var ErrNoStream = fmt.Errorf("record: no live stream for signal")

// sink is one connected /ws/audio client. Packets are delivered on C;
// a receive from closed signals the end of the stream (§10.4: close
// code 1000).
type sink struct {
	C chan []byte
}

// signalStream is the per-signal encoder state plus every subscribed
// client (§10.4: one live stream per signal per client, multiplexed).
type signalStream struct {
	meta      StreamMeta
	pkt       Packetizer
	pcm       []float32 // buffered samples below one frame
	frameSize int
	sinks     map[*sink]struct{}
}

// Streamer multiplexes demodulated audio into per-signal live Opus
// streams (D1b, §10.4). The Recorder feeds it from the session path;
// the /ws/audio server subscribes clients to it. Encoding happens
// only while at least one client listens.
type Streamer struct {
	cfg StreamConfig

	mu      sync.Mutex
	streams map[string]*signalStream
}

// NewStreamer creates a streamer. Sensible §10.4 defaults apply for
// unset fields.
func NewStreamer(cfg StreamConfig) *Streamer {
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 48000
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.BitrateBps <= 0 {
		cfg.BitrateBps = 24000
	}
	return &Streamer{cfg: cfg, streams: make(map[string]*signalStream)}
}

// Feed appends demodulated audio for sig, encoding and fanning out
// complete frames to subscribed clients. Called on the recorder's
// frame path — it must stay cheap: with no listeners it only buffers
// (and trims) PCM.
func (s *Streamer) Feed(sig db.Signal, pcm []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.streams[sig.ID]
	if !ok {
		if len(pcm) == 0 {
			return
		}
		pkt, frameSize, err := newPacketizer(s.cfg)
		if err != nil {
			return // no encoder in this build / init failure — no live stream
		}
		st = &signalStream{
			meta: StreamMeta{
				Type:       "audio.meta",
				SignalID:   sig.ID,
				CenterHz:   sig.FreqHz,
				Modulation: sig.Modulation,
				SubType:    sig.SubType,
				SampleRate: s.cfg.SampleRate,
				Channels:   s.cfg.Channels,
				Bitrate:    s.cfg.BitrateBps,
			},
			pkt:       pkt,
			frameSize: frameSize,
			sinks:     make(map[*sink]struct{}),
		}
		s.streams[sig.ID] = st
	}

	// Live semantics (§10.4.3): audio nobody listens to is dropped,
	// not buffered — a subscriber hears what happens from now on,
	// never a stale or ever-growing backlog.
	if len(st.sinks) == 0 {
		return
	}
	st.pcm = append(st.pcm, pcm...)
	s.pumpLocked(st)
}

// pumpLocked encodes complete buffered frames and fans the packets
// out to every subscriber. Called with s.mu held. A leftover partial
// frame stays buffered for the next feed; a failing encoder drops its
// frame (§10.4.3, UDP-like tolerance).
func (s *Streamer) pumpLocked(st *signalStream) {
	for len(st.sinks) > 0 && len(st.pcm) >= st.frameSize {
		frame := st.pcm[:st.frameSize]
		pkt, err := st.pkt.EncodeFrame(frame)
		st.pcm = st.pcm[st.frameSize:]
		if err != nil {
			continue
		}
		for sn := range st.sinks {
			deliver(sn, pkt)
		}
	}
}

// Subscribe attaches a client to sig's live stream. The returned
// channel carries one Opus packet per value; it is closed when the
// stream ends (session finalized), after which the server sends the
// 1000 close.
func (s *Streamer) Subscribe(signalID string) (<-chan []byte, StreamMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.streams[signalID]
	if !ok {
		return nil, StreamMeta{}, ErrNoStream
	}
	sn := &sink{C: make(chan []byte, 32)} // ~640 ms of 20 ms packets
	st.sinks[sn] = struct{}{}
	s.pumpLocked(st) // flush frames buffered before this subscriber joined
	return sn.C, st.meta, nil
}

// Unsubscribe detaches a client (client disconnect).
func (s *Streamer) Unsubscribe(signalID string, C <-chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.streams[signalID]
	if !ok {
		return
	}
	for sn := range st.sinks {
		if sn.C == C {
			delete(st.sinks, sn)
		}
	}
}

// CloseStream ends sig's live stream: every client is cut off (the
// server responds with close 1000) and the encoder state is dropped.
// Called when the WAV session finalizes.
func (s *Streamer) CloseStream(signalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.streams[signalID]
	if !ok {
		return
	}
	delete(s.streams, signalID)
	for sn := range st.sinks {
		close(sn.C)
	}
}

// deliver pushes one packet to a client, dropping the oldest queued
// packet on overflow (§10.4.3: missing messages are audio gaps —
// UDP-like tolerance, no retransmission). Never blocks the feed path.
func deliver(sn *sink, pkt []byte) {
	for {
		select {
		case sn.C <- pkt:
			return
		default:
		}
		select {
		case <-sn.C: // shed the oldest, retry the new one
		default:
			return // raced empty again — never spin
		}
	}
}
