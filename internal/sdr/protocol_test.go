package sdr

import (
	"testing"
	"time"
)

func TestIQFrame_EncodeDecode_RoundTrip(t *testing.T) {
	frame := &IQFrame{
		SDRID:      "rtl-sdr-1",
		FreqHz:     146_520_000,
		SampleRate: 2_000_000,
		Timestamp:  time.Unix(1700000000, 0),
		Samples:    []int16{100, -200, 300, -400, 500, -600, 700, -800},
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Verify magic bytes
	if len(encoded) < IQHeaderSize {
		t.Fatalf("encoded length %d < header size %d", len(encoded), IQHeaderSize)
	}
	if encoded[0] != 0x53 || encoded[1] != 0x44 || encoded[2] != 0x52 || encoded[3] != 0x31 {
		t.Errorf("bad magic bytes: % x", encoded[0:4])
	}

	decoded, err := DecodeIQFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeIQFrame failed: %v", err)
	}

	if decoded.SDRID != frame.SDRID {
		t.Errorf("SDRID = %q, expected %q", decoded.SDRID, frame.SDRID)
	}
	if decoded.FreqHz != frame.FreqHz {
		t.Errorf("FreqHz = %d, expected %d", decoded.FreqHz, frame.FreqHz)
	}
	if decoded.SampleRate != frame.SampleRate {
		t.Errorf("SampleRate = %d, expected %d", decoded.SampleRate, frame.SampleRate)
	}
	if !decoded.Timestamp.Equal(frame.Timestamp) {
		t.Errorf("Timestamp = %v, expected %v", decoded.Timestamp, frame.Timestamp)
	}
	if len(decoded.Samples) != len(frame.Samples) {
		t.Fatalf("Samples length = %d, expected %d", len(decoded.Samples), len(frame.Samples))
	}
	for i := range frame.Samples {
		if decoded.Samples[i] != frame.Samples[i] {
			t.Errorf("Samples[%d] = %d, expected %d", i, decoded.Samples[i], frame.Samples[i])
		}
	}
}

func TestIQFrame_Encode_OddSamples(t *testing.T) {
	frame := &IQFrame{
		SDRID:   "test",
		Samples: []int16{1, 2, 3}, // odd number
	}
	_, err := frame.Encode()
	if err == nil {
		t.Error("expected error for odd sample count")
	}
}

func TestIQFrame_Encode_TooManySamples(t *testing.T) {
	// Max is 1024 pairs = 2048 int16 values
	samples := make([]int16, (MaxIQSamplesPerFrame+1)*2)
	frame := &IQFrame{
		SDRID:   "test",
		Samples: samples,
	}
	_, err := frame.Encode()
	if err == nil {
		t.Error("expected error for too many samples")
	}
}

func TestIQFrame_Decode_TooShort(t *testing.T) {
	buf := make([]byte, IQHeaderSize-1) // one byte too short
	_, err := DecodeIQFrame(buf)
	if err == nil {
		t.Error("expected error for too-short buffer")
	}
}

func TestIQFrame_Decode_BadMagic(t *testing.T) {
	buf := make([]byte, IQHeaderSize+8)
	// Set bad magic
	buf[0] = 0xDE
	buf[1] = 0xAD
	buf[2] = 0xBE
	buf[3] = 0xEF
	_, err := DecodeIQFrame(buf)
	if err == nil {
		t.Error("expected error for bad magic")
	}
}

func TestIQFrame_Decode_TruncatedPayload(t *testing.T) {
	buf := make([]byte, IQHeaderSize+4) // only 1 pair worth of payload
	buf[0] = 0x53 // S
	buf[1] = 0x44 // D
	buf[2] = 0x52 // R
	buf[3] = 0x31 // 1
	// Set SampleCnt = 10 but only have room for 1
	buf[32] = 10
	buf[33] = 0

	_, err := DecodeIQFrame(buf)
	if err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestIQFrame_Encode_MaxSize(t *testing.T) {
	// Exactly at the limit should succeed
	samples := make([]int16, MaxIQSamplesPerFrame*2)
	for i := range samples {
		samples[i] = int16(i)
	}
	frame := &IQFrame{
		SDRID:      "full",
		FreqHz:     100_000_000,
		SampleRate: 2_000_000,
		Timestamp:  time.Now().Truncate(time.Second),
		Samples:    samples,
	}
	_, err := frame.Encode()
	if err != nil {
		t.Errorf("Encode at max size should succeed, got: %v", err)
	}
}