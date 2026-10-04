package audio

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestWAVWriterByteIdentical pins the streaming writer to EncodeWAV:
// both must produce the same bytes for the same sample sequence,
// including the header size fields and the (lossy) int16 conversion.
func TestWAVWriterByteIdentical(t *testing.T) {
	samples := make([]float32, 0, 1000)
	for i := 0; i < 1000; i++ {
		samples = append(samples, float32(math.Sin(float64(i)*0.01))*0.5)
	}
	// Exercise the conversion edges EncodeWAV hits with direct casts.
	samples[0] = 1.5
	samples[1] = -1.5
	samples[2] = 0
	samples[3] = -0.0001

	want, err := EncodeWAV(samples, 48000)
	if err != nil {
		t.Fatalf("EncodeWAV: %v", err)
	}

	path := filepath.Join(t.TempDir(), "out.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ww, err := NewWAVWriter(f, 48000)
	if err != nil {
		t.Fatalf("NewWAVWriter: %v", err)
	}
	// Write in irregular chunks — the result must not depend on framing.
	if err := ww.Write(samples[:137]); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ww.Write(samples[137:]); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ww.DataBytes() != uint64(len(samples)*2) {
		t.Fatalf("DataBytes = %d, want %d", ww.DataBytes(), len(samples)*2)
	}
	if err := ww.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := ww.Close(); err != nil {
		t.Fatalf("double close must be a no-op, got %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("WAVWriter output differs from EncodeWAV: %d vs %d bytes", len(got), len(want))
	}
}

// TestWAVWriterHeaderOnAbandon checks that an un-finalized file (no
// Close) is still a parseable WAV with a zero-length data chunk.
func TestWAVWriterHeaderOnAbandon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abandoned.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := NewWAVWriter(f, 48000); err != nil {
		t.Fatalf("NewWAVWriter: %v", err)
	}
	// Intentionally no Close — simulate a crash.
	f.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(raw) != wavHeaderSize {
		t.Fatalf("header-only file = %d bytes, want %d", len(raw), wavHeaderSize)
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Error("missing RIFF/WAVE magic in header")
	}
}
