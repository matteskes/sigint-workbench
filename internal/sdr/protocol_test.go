package sdr

import (
	"encoding/binary"
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

func TestIQFrame_EncodeDecode_RoundTripV2(t *testing.T) {
	frame := &IQFrame{
		SDRID:       "rtlsdr-0",
		FreqHz:      146_520_000,
		SampleRate:  2_400_000,
		Timestamp:   time.Unix(1700000000, 123456789),
		Samples:     []int16{100, -200, 300, -400, 500, -600},
		V2:          true,
		Seq:         42,
		SampleIndex: 987654321,
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(encoded) != IQHeaderSizeV2+len(frame.Samples)*2 {
		t.Fatalf("encoded length = %d, expected %d", len(encoded), IQHeaderSizeV2+len(frame.Samples)*2)
	}
	// §4.5 v2 magic on the wire.
	if encoded[0] != 0x53 || encoded[1] != 0x44 || encoded[2] != 0x52 || encoded[3] != 0x32 {
		t.Errorf("bad v2 magic bytes: % x", encoded[0:4])
	}
	// Reserved word MUST be zero on the wire (§4.5 senders).
	for i := IQHeaderSizeV2 - 4; i < IQHeaderSizeV2; i++ {
		if encoded[i] != 0 {
			t.Errorf("reserved byte %d = %#x, expected 0", i, encoded[i])
		}
	}

	decoded, err := DecodeIQFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeIQFrame failed: %v", err)
	}
	if !decoded.V2 {
		t.Error("decoded.V2 = false, expected true")
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
	// The ns-resolution anchor is the whole point of v2 (§4.5).
	if !decoded.Timestamp.Equal(frame.Timestamp) {
		t.Errorf("Timestamp = %v, expected %v", decoded.Timestamp, frame.Timestamp)
	}
	if decoded.Seq != frame.Seq {
		t.Errorf("Seq = %d, expected %d", decoded.Seq, frame.Seq)
	}
	if decoded.SampleIndex != frame.SampleIndex {
		t.Errorf("SampleIndex = %d, expected %d", decoded.SampleIndex, frame.SampleIndex)
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

// §4.5: a mixed v1/v2 datagram stream is valid — the magic selects
// the header layout per frame, and decoded frames carry the version
// in V2.
func TestIQFrame_EncodeDecode_MixedV1V2Stream(t *testing.T) {
	now := time.Unix(1700000000, 500000000)
	frames := []*IQFrame{
		{SDRID: "mix", FreqHz: 100, SampleRate: 2, Timestamp: now,
			Samples: []int16{1, 2, 3, 4}},
		{SDRID: "mix", FreqHz: 100, SampleRate: 2, Timestamp: now,
			Samples: []int16{5, 6, 7, 8}, V2: true, Seq: 0, SampleIndex: 4},
		{SDRID: "mix", FreqHz: 100, SampleRate: 2, Timestamp: now,
			Samples: []int16{9, 10}, V2: true, Seq: 1, SampleIndex: 6},
		{SDRID: "mix", FreqHz: 100, SampleRate: 2, Timestamp: now.Truncate(time.Second),
			Samples: []int16{11, 12}},
	}

	wantV2 := []bool{false, true, true, false}
	wantSeq := []uint64{0, 0, 1, 0}
	wantIdx := []uint64{0, 4, 6, 0}
	for i, f := range frames {
		encoded, err := f.Encode()
		if err != nil {
			t.Fatalf("frame %d: Encode failed: %v", i, err)
		}
		decoded, err := DecodeIQFrame(encoded)
		if err != nil {
			t.Fatalf("frame %d: DecodeIQFrame failed: %v", i, err)
		}
		if decoded.V2 != wantV2[i] {
			t.Errorf("frame %d: V2 = %v, expected %v", i, decoded.V2, wantV2[i])
		}
		if decoded.Seq != wantSeq[i] {
			t.Errorf("frame %d: Seq = %d, expected %d", i, decoded.Seq, wantSeq[i])
		}
		if decoded.SampleIndex != wantIdx[i] {
			t.Errorf("frame %d: SampleIndex = %d, expected %d", i, decoded.SampleIndex, wantIdx[i])
		}
		if got, want := len(decoded.Samples), len(f.Samples); got != want {
			t.Errorf("frame %d: samples = %d, expected %d", i, got, want)
		}
	}
}

// §4.5 length bound: 64 + 1024*4 = 4160 bytes.
func TestIQFrame_EncodeDecode_V2_MaxSize(t *testing.T) {
	samples := make([]int16, MaxIQSamplesPerFrame*2)
	for i := range samples {
		samples[i] = int16(i % 30000)
	}
	frame := &IQFrame{
		SDRID:       "full",
		FreqHz:      100,
		SampleRate:  2,
		Timestamp:   time.Unix(1700000000, 999999999),
		Samples:     samples,
		V2:          true,
		Seq:         7,
		SampleIndex: 1 << 40,
	}
	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(encoded) != MaxIQDatagramSize {
		t.Fatalf("encoded length = %d, expected MaxIQDatagramSize %d", len(encoded), MaxIQDatagramSize)
	}
	decoded, err := DecodeIQFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeIQFrame failed: %v", err)
	}
	if len(decoded.Samples) != len(samples) {
		t.Fatalf("samples = %d, expected %d", len(decoded.Samples), len(samples))
	}
	if decoded.SampleIndex != frame.SampleIndex {
		t.Errorf("SampleIndex = %d, expected %d", decoded.SampleIndex, frame.SampleIndex)
	}
}

func TestIQFrame_Decode_V2_TooShort(t *testing.T) {
	// Carries the v2 magic but stops inside the 64-byte header.
	buf := make([]byte, IQHeaderSizeV2-1)
	binary.LittleEndian.PutUint32(buf[0:4], IQMagicV2)
	if _, err := DecodeIQFrame(buf); err == nil {
		t.Error("expected error for too-short v2 header")
	}
}

func TestIQFrame_Decode_V2_TruncatedPayload(t *testing.T) {
	buf := make([]byte, IQHeaderSizeV2+4) // room for only 1 pair
	binary.LittleEndian.PutUint32(buf[0:4], IQMagicV2)
	binary.LittleEndian.PutUint32(buf[32:36], 10) // claims 10 pairs
	if _, err := DecodeIQFrame(buf); err == nil {
		t.Error("expected error for truncated v2 payload")
	}
}

// §4.3 bounds apply to both versions: sample_cnt is 1..1024.
func TestIQFrame_Decode_V2_BadSampleCount(t *testing.T) {
	for _, cnt := range []uint32{0, MaxIQSamplesPerFrame + 1} {
		buf := make([]byte, IQHeaderSizeV2+64)
		binary.LittleEndian.PutUint32(buf[0:4], IQMagicV2)
		binary.LittleEndian.PutUint32(buf[32:36], cnt)
		if _, err := DecodeIQFrame(buf); err == nil {
			t.Errorf("expected error for sample count %d", cnt)
		}
	}
}

func TestIQFrame_Decode_V1_ZeroPairsRejected(t *testing.T) {
	buf := make([]byte, IQHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], IQMagic)
	binary.LittleEndian.PutUint32(buf[32:36], 0)
	if _, err := DecodeIQFrame(buf); err == nil {
		t.Error("expected error for zero pair count (§4.3)")
	}
}

// §4.5: receivers ignore the reserved word (senders MUST zero it).
func TestIQFrame_Decode_V2_ReservedIgnored(t *testing.T) {
	frame := &IQFrame{
		SDRID: "res", FreqHz: 1, SampleRate: 2,
		Timestamp: time.Unix(5, 6), Samples: []int16{7, 8},
		V2: true, Seq: 1, SampleIndex: 2,
	}
	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	binary.LittleEndian.PutUint32(encoded[IQHeaderSizeV2-4:], 0xDEADBEEF)
	decoded, err := DecodeIQFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeIQFrame with nonzero reserved failed: %v", err)
	}
	if decoded.Seq != 1 || !decoded.V2 {
		t.Errorf("unexpected decode: V2=%v Seq=%d", decoded.V2, decoded.Seq)
	}
}