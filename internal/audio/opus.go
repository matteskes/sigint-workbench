//go:build opus

// Opus encoder for the live audio stream (D1b, §10.4).
//
// Built only with `-tags opus` because libopus is a cgo dependency:
// the recorder image (docker/Dockerfile.recorder) is Debian-based for
// this reason, while every other image stays CGO_ENABLED=0. Without
// the tag, opus_stub.go provides the same surface and reports
// ErrOpusUnsupported, so the rest of the tree compiles unchanged.
//
// Transport contract (§10.4, normative): mono, 48 kHz input, 24 kbps
// CBR, 20 ms frames (960 samples per packet). Restricted-lowdelay
// forces the CELT-only mode, so a 20 ms mono frame's TOC byte is
// 0xF8 (config 31), which the §17.3 frame-counter test asserts.
package audio

import (
	"errors"
	"fmt"

	"github.com/hraban/opus"
)

// OpusAvailable reports whether this binary supports Opus encoding.
const OpusAvailable = true

// OpusEncoder wraps a libopus encoder pinned to the §10.4 transport
// contract. EncodeFrame takes exactly FrameSamples input samples and
// returns exactly one Opus packet.
type OpusEncoder struct {
	enc          *opus.Encoder
	frameSamples int
}

// FrameSamples is the number of 48 kHz mono input samples per Opus
// packet (20 ms, §10.4).
const FrameSamples = 960

// NewOpusEncoder creates a 20 ms CBR encoder per the §10.4 contract:
// sampleRate 48000, channels 1, bitrateBps 24000.
func NewOpusEncoder(sampleRate, channels, bitrateBps int) (*OpusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppRestrictedLowdelay)
	if err != nil {
		return nil, fmt.Errorf("audio: opus encoder init: %w", err)
	}
	// CELT-only mode (restricted lowdelay) keeps latency minimal and
	// fixes the TOC at config 31 for 20 ms frames; CBR pins the size
	// so the gateway relay stays a transparent byte pipe.
	if err := enc.SetBitrate(bitrateBps); err != nil {
		return nil, fmt.Errorf("audio: opus set bitrate: %w", err)
	}
	if err := enc.SetVBR(false); err != nil { // false = CBR (§10.4)
		return nil, fmt.Errorf("audio: opus set CBR: %w", err)
	}
	if err := enc.SetComplexity(4); err != nil {
		return nil, fmt.Errorf("audio: opus set complexity: %w", err)
	}
	return &OpusEncoder{enc: enc, frameSamples: sampleRate / (1000 / 20)}, nil
}

// EncodeFrame encodes exactly one frame of PCM into exactly one Opus
// packet (TOC byte + payload, RFC 6716). pcm must hold FrameSamples
// samples — the streamer only ever hands over complete frames.
func (e *OpusEncoder) EncodeFrame(pcm []float32) ([]byte, error) {
	if len(pcm) != e.frameSamples {
		return nil, fmt.Errorf("audio: opus frame needs %d samples, got %d", e.frameSamples, len(pcm))
	}
	buf := make([]byte, 512) // max Opus packet is 1275 bytes; 24 kbps CBR frames are ~60
	n, err := e.enc.EncodeFloat32(pcm, buf)
	if err != nil {
		return nil, fmt.Errorf("audio: opus encode: %w", err)
	}
	if n == 0 {
		return nil, errors.New("audio: opus encode produced an empty packet")
	}
	return buf[:n], nil
}
