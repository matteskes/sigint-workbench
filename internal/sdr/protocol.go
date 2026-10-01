// Package sdr — UDP IQ streaming protocol.
//
// Wire format (little-endian), 40-byte header + payload:
//
//	[0:4]    Magic      uint32  = 0x31524453 ("SDR1")
//	[4:20]   SDRID      [16]byte  ASCII, null-padded
//	[20:28]  FreqHz     uint64
//	[28:32]  SampleRate uint32
//	[32:36]  SampleCnt  uint32  (number of IQ pairs)
//	[36:40]  Timestamp  uint32  (seconds since epoch)
//
//	Payload: int16[SampleCnt * 2] — interleaved I[0],Q[0],I[1],Q[1],...
package sdr

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	// IQMagic is the 4-byte magic number at the start of every IQ frame.
	IQMagic uint32 = 0x31524453 // "SDR1"

	// IQHeaderSize is the size of the IQ frame header in bytes.
	IQHeaderSize = 40

	// MaxIQSamplesPerFrame is the max IQ pairs per UDP frame.
	// Kept at 1024 to stay well under macOS's ~9 KB UDP socket buffer
	// (1024 pairs * 4 bytes + 40 header = 4136 bytes).
	MaxIQSamplesPerFrame = 1024
)

// IQFrame represents one UDP packet of IQ data.
type IQFrame struct {
	SDRID      string
	FreqHz     uint64
	SampleRate uint32
	Timestamp  time.Time
	Samples    []int16 // interleaved I/Q
}

// Encode serializes an IQFrame into a byte slice.
func (f *IQFrame) Encode() ([]byte, error) {
	if len(f.Samples)%2 != 0 {
		return nil, fmt.Errorf("sdr: sample count must be even (got %d)", len(f.Samples))
	}
	pairs := len(f.Samples) / 2
	if pairs > MaxIQSamplesPerFrame {
		return nil, fmt.Errorf("sdr: too many samples (%d > %d)", pairs, MaxIQSamplesPerFrame)
	}

	buf := make([]byte, IQHeaderSize+len(f.Samples)*2)
	binary.LittleEndian.PutUint32(buf[0:4], IQMagic)
	copy(buf[4:20], f.SDRID)
	binary.LittleEndian.PutUint64(buf[20:28], f.FreqHz)
	binary.LittleEndian.PutUint32(buf[28:32], f.SampleRate)
	binary.LittleEndian.PutUint32(buf[32:36], uint32(pairs))
	binary.LittleEndian.PutUint32(buf[36:40], uint32(f.Timestamp.Unix()))
	for i, s := range f.Samples {
		binary.LittleEndian.PutUint16(buf[IQHeaderSize+i*2:], uint16(s))
	}
	return buf, nil
}

// DecodeIQFrame parses a byte slice into an IQFrame.
func DecodeIQFrame(buf []byte) (*IQFrame, error) {
	if len(buf) < IQHeaderSize {
		return nil, fmt.Errorf("sdr: frame too short (%d < %d)", len(buf), IQHeaderSize)
	}
	magic := binary.LittleEndian.Uint32(buf[0:4])
	if magic != IQMagic {
		return nil, fmt.Errorf("sdr: bad magic 0x%08X", magic)
	}

	id := ""
	for _, b := range buf[4:20] {
		if b == 0 {
			break
		}
		id += string(b)
	}

	freq := binary.LittleEndian.Uint64(buf[20:28])
	sr := binary.LittleEndian.Uint32(buf[28:32])
	pairs := binary.LittleEndian.Uint32(buf[32:36])
	ts := binary.LittleEndian.Uint32(buf[36:40])

	expected := IQHeaderSize + int(pairs)*4
	if len(buf) < expected {
		return nil, fmt.Errorf("sdr: frame truncated")
	}

	samples := make([]int16, pairs*2)
	for i := 0; i < int(pairs)*2; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(buf[IQHeaderSize+i*2:]))
	}

	return &IQFrame{
		SDRID:      id,
		FreqHz:     freq,
		SampleRate: sr,
		Timestamp:  time.Unix(int64(ts), 0),
		Samples:    samples,
	}, nil
}