// Package sdr — UDP IQ streaming protocol.
//
// Two wire formats share the same little-endian style and the same
// int16 interleaved payload; the magic distinguishes them on the wire
// (§4.5: a mixed v1/v2 stream is valid, no negotiation).
//
// v1 (§4.1, "SDR1", 40-byte header):
//
//	[0:4]    Magic      uint32  = 0x31524453 ("SDR1")
//	[4:20]   SDRID      [16]byte  ASCII, null-padded
//	[20:28]  FreqHz     uint64
//	[28:32]  SampleRate uint32
//	[32:36]  SampleCnt  uint32  (number of IQ pairs)
//	[36:40]  Timestamp  uint32  (seconds since epoch)
//
// v2 (§4.5, "SDR2", 64-byte header) adds the sample-accurate timing
// TDOA needs — per-sender sequence, nanosecond anchor, sample_index:
//
//	[0:4]    Magic        uint32  = 0x32524453 ("SDR2")
//	[4:20]   SDRID        [16]byte  ASCII, null-padded
//	[20:28]  FreqHz       uint64
//	[28:32]  SampleRate   uint32
//	[32:36]  SampleCnt    uint32  (number of IQ pairs, 1..1024)
//	[36:44]  Seq          uint64  per-sender, starts at 0, +1 per frame
//	[44:48]  TSec         uint32  Unix seconds at the FIRST sample
//	[48:52]  TNsec        uint32  nanosecond part of that instant
//	[52:60]  SampleIndex  uint64  samples since stream start (first sample)
//	[60:64]  Reserved     uint32  MUST be 0 (senders) / ignored (receivers)
//
//	Payload: int16[SampleCnt * 2] — interleaved I[0],Q[0],I[1],Q[1],...
package sdr

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	// IQMagic is the v1 4-byte magic number ("SDR1").
	IQMagic uint32 = 0x31524453

	// IQMagicV2 is the v2 4-byte magic number ("SDR2", §4.5).
	IQMagicV2 uint32 = 0x32524453

	// IQHeaderSize is the v1 frame header size in bytes.
	IQHeaderSize = 40

	// IQHeaderSizeV2 is the v2 frame header size in bytes (§4.5).
	IQHeaderSizeV2 = 64

	// MaxIQSamplesPerFrame is the max IQ pairs per UDP frame.
	// Kept at 1024 to stay well under macOS's ~9 KB UDP socket buffer
	// (v2 max datagram: 64 + 1024*4 = 4160 bytes).
	MaxIQSamplesPerFrame = 1024

	// MaxIQDatagramSize is the largest possible frame on the wire —
	// the v2 bound (§4.5). UDP receivers size their read buffers with
	// this so a max-size v2 datagram is never truncated.
	MaxIQDatagramSize = IQHeaderSizeV2 + MaxIQSamplesPerFrame*4
)

// IQFrame represents one UDP packet of IQ data.
type IQFrame struct {
	SDRID      string
	FreqHz     uint64
	SampleRate uint32
	Timestamp  time.Time
	Samples    []int16 // interleaved I/Q

	// V2 reports the §4.5 wire format: DecodeIQFrame sets it for
	// "SDR2" datagrams and Encode consults it to emit v2.
	V2 bool

	// Seq is the v2 per-sender sequence number: starts at 0, +1 per
	// frame. Always 0 on v1 frames.
	Seq uint64

	// SampleIndex is the v2 stream-wide index of the frame's first
	// sample ("samples produced for this SDR since stream start").
	// Always 0 on v1 frames — v1 carries no sample-accurate timing
	// and its 1-second timestamp MUST NOT be used for TDOA (§4.5).
	SampleIndex uint64
}

// Encode serializes an IQFrame into a byte slice, emitting the §4.5
// v2 format when f.V2 is set and the classic v1 format otherwise.
func (f *IQFrame) Encode() ([]byte, error) {
	if len(f.Samples)%2 != 0 {
		return nil, fmt.Errorf("sdr: sample count must be even (got %d)", len(f.Samples))
	}
	pairs := len(f.Samples) / 2
	if pairs > MaxIQSamplesPerFrame {
		return nil, fmt.Errorf("sdr: too many samples (%d > %d)", pairs, MaxIQSamplesPerFrame)
	}
	if f.V2 {
		return f.encodeV2(pairs)
	}
	return f.encodeV1(pairs)
}

func (f *IQFrame) encodeV1(pairs int) ([]byte, error) {
	buf := make([]byte, IQHeaderSize+len(f.Samples)*2)
	binary.LittleEndian.PutUint32(buf[0:4], IQMagic)
	copy(buf[4:20], f.SDRID)
	binary.LittleEndian.PutUint64(buf[20:28], f.FreqHz)
	binary.LittleEndian.PutUint32(buf[28:32], f.SampleRate)
	binary.LittleEndian.PutUint32(buf[32:36], uint32(pairs))
	binary.LittleEndian.PutUint32(buf[36:40], uint32(f.Timestamp.Unix()))
	encodeSamples(buf[IQHeaderSize:], f.Samples)
	return buf, nil
}

// encodeV2 emits the §4.5 header: the same leading fields as v1
// (16-byte id, freq, rate, pair count), then the sequence number,
// the CLOCK_REALTIME anchor of the FIRST sample with nanosecond
// resolution, the stream-wide sample_index, and a zeroed reserved
// word.
func (f *IQFrame) encodeV2(pairs int) ([]byte, error) {
	buf := make([]byte, IQHeaderSizeV2+len(f.Samples)*2)
	binary.LittleEndian.PutUint32(buf[0:4], IQMagicV2)
	copy(buf[4:20], f.SDRID)
	binary.LittleEndian.PutUint64(buf[20:28], f.FreqHz)
	binary.LittleEndian.PutUint32(buf[28:32], f.SampleRate)
	binary.LittleEndian.PutUint32(buf[32:36], uint32(pairs))
	binary.LittleEndian.PutUint64(buf[36:44], f.Seq)
	binary.LittleEndian.PutUint32(buf[44:48], uint32(f.Timestamp.Unix()))
	binary.LittleEndian.PutUint32(buf[48:52], uint32(f.Timestamp.Nanosecond()))
	binary.LittleEndian.PutUint64(buf[52:60], f.SampleIndex)
	// buf[60:64] reserved: the zero value already complies (§4.5).
	encodeSamples(buf[IQHeaderSizeV2:], f.Samples)
	return buf, nil
}

func encodeSamples(payload []byte, samples []int16) {
	for i, s := range samples {
		binary.LittleEndian.PutUint16(payload[i*2:], uint16(s))
	}
}

// DecodeIQFrame parses a byte slice into an IQFrame, accepting both
// wire formats (§4.5): the magic selects the header layout, so a
// mixed v1/v2 datagram stream decodes frame by frame.
func DecodeIQFrame(buf []byte) (*IQFrame, error) {
	if len(buf) < IQHeaderSize {
		return nil, fmt.Errorf("sdr: frame too short (%d < %d)", len(buf), IQHeaderSize)
	}
	switch magic := binary.LittleEndian.Uint32(buf[0:4]); magic {
	case IQMagic:
		return decodeV1(buf)
	case IQMagicV2:
		if len(buf) < IQHeaderSizeV2 {
			return nil, fmt.Errorf("sdr: v2 frame too short (%d < %d)", len(buf), IQHeaderSizeV2)
		}
		return decodeV2(buf)
	default:
		return nil, fmt.Errorf("sdr: bad magic 0x%08X", magic)
	}
}

// decodeCommon reads the header fields v1 and v2 share (§4.2/§4.3):
// the null-padded ASCII id, freq, rate, and the 1..1024 pair count.
func decodeCommon(buf []byte, pairs uint32) (id string, freq uint64, sr uint32, err error) {
	for _, b := range buf[4:20] {
		if b == 0 {
			break
		}
		id += string(b)
	}
	freq = binary.LittleEndian.Uint64(buf[20:28])
	sr = binary.LittleEndian.Uint32(buf[28:32])
	if pairs == 0 || pairs > MaxIQSamplesPerFrame {
		return "", 0, 0, fmt.Errorf("sdr: invalid sample count %d (want 1..%d)", pairs, MaxIQSamplesPerFrame)
	}
	return id, freq, sr, nil
}

func decodeV1(buf []byte) (*IQFrame, error) {
	pairs := binary.LittleEndian.Uint32(buf[32:36])
	id, freq, sr, err := decodeCommon(buf, pairs)
	if err != nil {
		return nil, err
	}
	if len(buf) < IQHeaderSize+int(pairs)*4 {
		return nil, fmt.Errorf("sdr: frame truncated")
	}
	ts := binary.LittleEndian.Uint32(buf[36:40])
	return &IQFrame{
		SDRID:      id,
		FreqHz:     freq,
		SampleRate: sr,
		Timestamp:  time.Unix(int64(ts), 0),
		Samples:    decodeSamples(buf[IQHeaderSize:], int(pairs)),
	}, nil
}

// decodeV2 parses the §4.5 header. The reserved word is ignored
// (§4.5: senders MUST zero it, receivers don't check).
func decodeV2(buf []byte) (*IQFrame, error) {
	pairs := binary.LittleEndian.Uint32(buf[32:36])
	id, freq, sr, err := decodeCommon(buf, pairs)
	if err != nil {
		return nil, err
	}
	if len(buf) < IQHeaderSizeV2+int(pairs)*4 {
		return nil, fmt.Errorf("sdr: v2 frame truncated")
	}
	sec := binary.LittleEndian.Uint32(buf[44:48])
	nsec := binary.LittleEndian.Uint32(buf[48:52])
	return &IQFrame{
		SDRID:       id,
		FreqHz:      freq,
		SampleRate:  sr,
		Timestamp:   time.Unix(int64(sec), int64(nsec)),
		Samples:     decodeSamples(buf[IQHeaderSizeV2:], int(pairs)),
		V2:          true,
		Seq:         binary.LittleEndian.Uint64(buf[36:44]),
		SampleIndex: binary.LittleEndian.Uint64(buf[52:60]),
	}, nil
}

func decodeSamples(payload []byte, pairs int) []int16 {
	samples := make([]int16, pairs*2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(payload[i*2:]))
	}
	return samples
}