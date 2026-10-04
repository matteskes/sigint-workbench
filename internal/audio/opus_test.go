//go:build opus

package audio

import (
	"math"
	"testing"
)

// TestOpusEncoderFrameContract pins the §10.4 transport contract:
// 960-sample (20 ms) frames in, exactly one CBR packet out, with the
// mono/20 ms CELT TOC byte 0xF8 that §17.3 requires the recorder's WS
// to emit.
func TestOpusEncoderFrameContract(t *testing.T) {
	e, err := NewOpusEncoder(48000, 1, 24000)
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}

	// 1 second of 440 Hz sine at 48 kHz, fed in 20 ms frames.
	sine := make([]float32, 48000)
	for i := range sine {
		sine[i] = 0.4 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	packets := 0
	var toc byte
	var sizes []int
	for off := 0; off+FrameSamples <= len(sine); off += FrameSamples {
		pkt, err := e.EncodeFrame(sine[off : off+FrameSamples])
		if err != nil {
			t.Fatalf("EncodeFrame @%d: %v", off, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("EncodeFrame @%d: empty packet", off)
		}
		if packets == 0 {
			toc = pkt[0]
		} else if pkt[0] != toc {
			t.Fatalf("TOC changed mid-stream: %02x -> %02x", toc, pkt[0])
		}
		sizes = append(sizes, len(pkt))
		packets++
	}
	if packets != 50 {
		t.Fatalf("1 s of 48 kHz audio = %d packets of 20 ms, got %d", 50, packets)
	}
	if toc != 0xF8 {
		t.Fatalf("TOC byte = %02x, want 0xF8 (mono, 20 ms CELT config 31)", toc)
	}
	// CBR: packet sizes for a constant tone must be tightly grouped.
	lo, hi := sizes[0], sizes[0]
	for _, s := range sizes {
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	if hi-lo > 8 {
		t.Fatalf("CBR packet sizes vary by %d bytes (%d..%d)", hi-lo, lo, hi)
	}
	if lo < 40 || hi > 512 {
		t.Fatalf("packet size %d..%d outside plausible 24 kbps range", lo, hi)
	}

	// Frame length enforcement.
	if _, err := e.EncodeFrame(make([]float32, FrameSamples-1)); err == nil {
		t.Fatal("EncodeFrame accepted a short frame")
	}
}
