//go:build !opus

// Opus stub (build without `-tags opus`): same surface as opus.go,
// but every encoder creation fails with ErrOpusUnsupported. The
// recorder checks OpusAvailable and skips the live-stream server, so
// the recording pipeline (D1) works in CGO_ENABLED=0 images exactly
// as before.
package audio

import (
	"errors"
	"fmt"
)

// OpusAvailable reports whether this binary supports Opus encoding.
const OpusAvailable = false

// ErrOpusUnsupported is returned by NewOpusEncoder in binaries built
// without the `opus` build tag (no libopus / cgo).
var ErrOpusUnsupported = errors.New("audio: opus support not built in (requires -tags opus and libopus)")

// FrameSamples is the number of 48 kHz mono input samples per Opus
// packet (20 ms, §10.4) — mirrored here so callers can size buffers
// without importing the tagged file.
const FrameSamples = 960

// OpusEncoder mirrors the real encoder's type so callers compile in
// both build modes. It is never usable in this mode.
type OpusEncoder struct{}

// NewOpusEncoder always fails with ErrOpusUnsupported in this build.
func NewOpusEncoder(sampleRate, channels, bitrateBps int) (*OpusEncoder, error) {
	return nil, fmt.Errorf("%w (requested %d Hz, %d ch, %d bps)", ErrOpusUnsupported, sampleRate, channels, bitrateBps)
}

// EncodeFrame always fails in this build.
func (e *OpusEncoder) EncodeFrame(pcm []float32) ([]byte, error) {
	return nil, ErrOpusUnsupported
}
