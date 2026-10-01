// Package audio — audio encoding (WAV, PCM).
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// EncodeWAV encodes float32 audio samples into a 16-bit PCM WAV file.
func EncodeWAV(samples []float32, sampleRate uint32) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("audio: no samples to encode")
	}

	numChannels := uint16(1)
	bitsPerSample := uint16(16)
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := uint16(numChannels * bitsPerSample / 8)
	dataSize := uint32(len(samples) * 2) // 16-bit = 2 bytes per sample

	buf := &bytes.Buffer{}

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(buf, binary.LittleEndian, uint16(1))  // PCM
	binary.Write(buf, binary.LittleEndian, numChannels)
	binary.Write(buf, binary.LittleEndian, sampleRate)
	binary.Write(buf, binary.LittleEndian, byteRate)
	binary.Write(buf, binary.LittleEndian, blockAlign)
	binary.Write(buf, binary.LittleEndian, bitsPerSample)

	// data chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, dataSize)

	for _, s := range samples {
		v := int16(s * 32767)
		binary.Write(buf, binary.LittleEndian, v)
	}

	return buf.Bytes(), nil
}

// Float32ToPCM16 converts float32 samples (-1.0 to 1.0) to int16 PCM.
func Float32ToPCM16(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, s := range samples {
		v := int(s * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}