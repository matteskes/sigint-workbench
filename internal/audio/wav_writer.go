// Package audio — streaming WAV file writer (§10.2).
package audio

import (
	"encoding/binary"
	"io"
)

// wavHeaderSize is the canonical 44-byte PCM WAV header length.
const wavHeaderSize = 44

// WAVWriter streams float32 samples into a 16-bit PCM WAV file. The
// output is byte-identical to EncodeWAV over the same sample
// sequence: the 44-byte header is written up front with zeroed size
// fields and Close patches them once the data size is final, so a
// recording stays structurally valid even if the process dies before
// finalization (the file then reads as empty rather than corrupt).
type WAVWriter struct {
	w          io.WriteSeeker
	sampleRate uint32
	dataBytes  uint64
	closed     bool
}

// NewWAVWriter writes the WAV header (with placeholder sizes) to w.
func NewWAVWriter(w io.WriteSeeker, sampleRate uint32) (*WAVWriter, error) {
	ww := &WAVWriter{w: w, sampleRate: sampleRate}
	if err := ww.writeHeader(0); err != nil {
		return nil, err
	}
	return ww, nil
}

// writeHeader emits the 44-byte header at offset 0 with the given
// data size, then seeks back to the end of the stream.
func (ww *WAVWriter) writeHeader(dataSize uint32) error {
	numChannels := uint16(1)
	bitsPerSample := uint16(16)
	byteRate := ww.sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := numChannels * bitsPerSample / 8

	header := make([]byte, 0, wavHeaderSize)
	appendStr := func(s string) { header = append(header, s...) }
	appendU32 := func(v uint32) { header = binary.LittleEndian.AppendUint32(header, v) }
	appendU16 := func(v uint16) { header = binary.LittleEndian.AppendUint16(header, v) }

	// RIFF header
	appendStr("RIFF")
	appendU32(36 + dataSize)
	appendStr("WAVE")

	// fmt chunk
	appendStr("fmt ")
	appendU32(16) // chunk size
	appendU16(1)  // PCM
	appendU16(numChannels)
	appendU32(ww.sampleRate)
	appendU32(byteRate)
	appendU16(blockAlign)
	appendU16(bitsPerSample)

	// data chunk
	appendStr("data")
	appendU32(dataSize)

	if _, err := ww.w.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := ww.w.Write(header); err != nil {
		return err
	}
	_, err := ww.w.Seek(int64(len(header)), io.SeekStart)
	return err
}

// Write converts samples to 16-bit PCM (same conversion as EncodeWAV)
// and appends them to the data chunk.
func (ww *WAVWriter) Write(samples []float32) error {
	if ww.closed {
		return io.ErrClosedPipe
	}
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		v := int16(s * 32767) // identical to EncodeWAV
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	n, err := ww.w.Write(pcm)
	ww.dataBytes += uint64(n)
	return err
}

// Close patches the RIFF and data chunk sizes to the number of bytes
// written and flushes the header. Safe to call more than once.
func (ww *WAVWriter) Close() error {
	if ww.closed {
		return nil
	}
	ww.closed = true
	return ww.writeHeader(uint32(ww.dataBytes))
}

// DataBytes reports how many PCM bytes have been written so far.
func (ww *WAVWriter) DataBytes() uint64 {
	return ww.dataBytes
}
