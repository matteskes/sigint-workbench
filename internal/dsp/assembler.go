package dsp

// FFTAssembler concatenates consecutive same-source IQ frames until
// fft.size pairs are assembled (§5.7). Frames arrive as ≤1024-pair
// UDP packets from iq-ingest; the wire carries no sequence number
// (§4.3), so a stream is identified by its SDR id and its tuned
// frequency, and loss inside a buffer is tolerated as a spectral
// discontinuity — the samples after a drop are still contiguous,
// just not the samples a lossless assembly would have produced.
//
// Each SDR keeps at most one partial buffer and a retune discards it
// (a device has one tuner), so memory is bounded by the device count
// and a scanning SDR never mixes samples across dwells.
type FFTAssembler struct {
	target  int                   // IQ pairs per buffer (fft.size)
	streams map[string]*asmStream // live partial per SDR id
}

// asmStream is one SDR's partial buffer at its current frequency.
type asmStream struct {
	freq uint64
	buf  []int16
}

// NewFFTAssembler returns an assembler emitting targetPairs IQ pairs
// per buffer. The caller validates the config (power of two in
// 64..16384, §5.7); this constructor stays allocation-light.
func NewFFTAssembler(targetPairs int) *FFTAssembler {
	return &FFTAssembler{
		target:  targetPairs,
		streams: make(map[string]*asmStream),
	}
}

// Offer appends one frame's samples to its SDR's stream, invoking
// emit once for every completed target-size buffer (normally zero or
// one per call; a frame larger than the target emits several).
// Streams are independent: interleaved frames from several devices
// never mix. The slice passed to emit is owned by the assembler and
// only valid until emit returns.
func (a *FFTAssembler) Offer(sdrID string, freqHz uint64, samples []int16,
	emit func(buf []int16)) {
	st := a.streams[sdrID]
	if st == nil {
		st = &asmStream{freq: freqHz}
		a.streams[sdrID] = st
	} else if st.freq != freqHz {
		st.freq = freqHz // retune: discard the partial
		st.buf = st.buf[:0]
	}
	st.buf = append(st.buf, samples...)
	full := 2 * a.target
	for len(st.buf) >= full {
		emit(st.buf[:full])
		// Compact in place: keep any remainder (frames larger
		// than the target) at the front for the next append.
		rest := copy(st.buf, st.buf[full:])
		st.buf = st.buf[:rest]
	}
}

// Reset drops all partial buffers (shutdown/test helper).
func (a *FFTAssembler) Reset() {
	a.streams = make(map[string]*asmStream)
}
