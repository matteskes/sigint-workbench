package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeWAV_Empty(t *testing.T) {
	if _, err := EncodeWAV(nil, 48000); err == nil {
		t.Fatal("expected error for empty samples, got nil")
	}
}

func TestEncodeWAV_Header(t *testing.T) {
	samples := []float32{0.0, 0.5, -0.5}
	const sr = 44100
	buf, err := EncodeWAV(samples, sr)
	if err != nil {
		t.Fatalf("EncodeWAV: %v", err)
	}
	r := bytes.NewReader(buf)

	readTag := func(want string) {
		t.Helper()
		var b [4]byte
		if err := binary.Read(r, binary.LittleEndian, &b); err != nil {
			t.Fatalf("read tag: %v", err)
		}
		if string(b[:]) != want {
			t.Fatalf("tag = %q, want %q", b, want)
		}
	}
	readU32 := func(want uint32) {
		t.Helper()
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			t.Fatalf("read u32: %v", err)
		}
		if v != want {
			t.Fatalf("u32 = %d, want %d", v, want)
		}
	}
	readU16 := func(want uint16) {
		t.Helper()
		var v uint16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			t.Fatalf("read u16: %v", err)
		}
		if v != want {
			t.Fatalf("u16 = %d, want %d", v, want)
		}
	}

	readTag("RIFF")
	readU32(36 + uint32(len(samples)*2)) // RIFF chunk size
	readTag("WAVE")
	readTag("fmt ")
	readU32(16) // fmt chunk size
	readU16(1)  // PCM
	readU16(1)  // mono
	readU32(sr) // sample rate
	readU32(sr * 2) // byte rate (1 ch * 16 bit)
	readU16(2)  // block align
	readU16(16) // bits per sample
	readTag("data")
	readU32(uint32(len(samples) * 2))

	var s0, s1, s2 int16
	if err := binary.Read(r, binary.LittleEndian, &s0); err != nil {
		t.Fatalf("read sample 0: %v", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &s1); err != nil {
		t.Fatalf("read sample 1: %v", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &s2); err != nil {
		t.Fatalf("read sample 2: %v", err)
	}
	if s0 != 0 || s1 != 16383 || s2 != -16383 {
		t.Fatalf("samples = [%d %d %d], want [0 16383 -16383]", s0, s1, s2)
	}
	if r.Len() != 0 {
		t.Fatalf("expected no trailing bytes, got %d", r.Len())
	}
}

func TestFloat32ToPCM16(t *testing.T) {
	cases := []struct {
		in   float32
		want int16
	}{
		{0, 0},
		{0.5, 16383},
		{-0.5, -16383},
		{1.0, 32767},
		{-1.0, -32767},
		{2.0, 32767},  // clipped to max
		{-2.0, -32768}, // clipped to min
	}
	out := Float32ToPCM16(make([]float32, len(cases)))
	if out := Float32ToPCM16(nil); len(out) != 0 {
		t.Fatalf("expected empty output for nil input, got %d samples", len(out))
	}
	samples := make([]float32, len(cases))
	for i, c := range cases {
		samples[i] = c.in
	}
	out = Float32ToPCM16(samples)
	for i, c := range cases {
		if out[i] != c.want {
			t.Errorf("Float32ToPCM16(%v) = %d, want %d", c.in, out[i], c.want)
		}
	}
}