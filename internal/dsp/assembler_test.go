package dsp

import "testing"

// testPairs builds n IQ pairs filled with val.
func testPairs(n int, val int16) []int16 {
	s := make([]int16, 2*n)
	for i := range s {
		s[i] = val
	}
	return s
}

func TestAssemblerAccumulatesAcrossFrames(t *testing.T) {
	a := NewFFTAssembler(4096)
	var got [][]int16
	emit := func(b []int16) { got = append(got, append([]int16(nil), b...)) }

	a.Offer("sdr0", 121_500_000, testPairs(1024, 1), emit)
	a.Offer("sdr0", 121_500_000, testPairs(1024, 2), emit)
	a.Offer("sdr0", 121_500_000, testPairs(1024, 2), emit)
	if len(got) != 0 {
		t.Fatalf("emitted after %d of 4 frames", len(got))
	}
	a.Offer("sdr0", 121_500_000, testPairs(1024, 4), emit)

	if len(got) != 1 {
		t.Fatalf("got %d buffers, want 1", len(got))
	}
	buf := got[0]
	if len(buf) != 2*4096 {
		t.Fatalf("buffer = %d samples, want %d", len(buf), 2*4096)
	}
	// Frame order preserved: fills 1, 2, 2, 4 per 1024-pair frame.
	checks := []struct {
		off  int
		want int16
	}{
		{0, 1}, {2047, 1}, {2048, 2}, {4095, 2},
		{4096, 2}, {6143, 2}, {6144, 4}, {8191, 4},
	}
	for _, c := range checks {
		if buf[c.off] != c.want {
			t.Fatalf("buf[%d] = %d, want %d", c.off, buf[c.off], c.want)
		}
	}
}

func TestAssemblerKeyChangeDiscardsPartial(t *testing.T) {
	a := NewFFTAssembler(4096)
	var got int
	emit := func([]int16) { got++ }

	a.Offer("sdr0", 100_000_000, testPairs(1024, 1), emit)
	a.Offer("sdr0", 100_000_000, testPairs(1024, 1), emit)
	// Retune: the partial 2048-pair buffer is discarded, not emitted.
	a.Offer("sdr0", 103_000_000, testPairs(1024, 2), emit)
	if got != 0 {
		t.Fatalf("retune must discard the partial buffer, got %d emits", got)
	}
	for i := 0; i < 3; i++ {
		a.Offer("sdr0", 103_000_000, testPairs(1024, 2), emit)
	}
	if got != 1 {
		t.Fatalf("got %d emits after retune refill, want 1", got)
	}
}

func TestAssemblerInterleavedSDRs(t *testing.T) {
	a := NewFFTAssembler(4096)
	var got [][]int16
	emit := func(b []int16) { got = append(got, append([]int16(nil), b...)) }

	for round := 0; round < 4; round++ {
		a.Offer("sdr0", 100_000_000, testPairs(1024, int16(round+1)), emit)
		a.Offer("sdr1", 100_000_000, testPairs(1024, int16(10*round+1)), emit)
	}
	if len(got) != 2 {
		t.Fatalf("got %d buffers, want 2 (one per SDR)", len(got))
	}
	// Each stream completed inside its own Offer: sdr0 first, then sdr1.
	if got[0][0] != 1 || got[0][8191] != 4 {
		t.Fatalf("sdr0 buffer contents wrong: %d..%d",
			got[0][0], got[0][8191])
	}
	if got[1][0] != 1 || got[1][8191] != 31 {
		t.Fatalf("sdr1 buffer contents wrong: %d..%d",
			got[1][0], got[1][8191])
	}
}

func TestAssemblerOversizedFrameEmitsMultiple(t *testing.T) {
	a := NewFFTAssembler(512)
	var sizes []int
	emit := func(b []int16) { sizes = append(sizes, len(b)) }

	a.Offer("sdr0", 100_000_000, testPairs(1280, 1), emit)
	if len(sizes) != 2 || sizes[0] != 1024 || sizes[1] != 1024 {
		t.Fatalf("sizes = %v, want [1024 1024] with 512 samples held", sizes)
	}
	a.Offer("sdr0", 100_000_000, testPairs(256, 1), emit)
	if len(sizes) != 3 || sizes[2] != 1024 {
		t.Fatalf("remainder flush wrong: %v", sizes)
	}
}

func TestAssemblerEmitSeesFreshData(t *testing.T) {
	// The emitted slice aliases the assembler's internal buffer; each
	// emit must already contain that window's data (ownership contract:
	// valid until emit returns).
	a := NewFFTAssembler(1024)
	var sums []int64
	emit := func(b []int16) {
		var s int64
		for _, v := range b {
			s += int64(v)
		}
		sums = append(sums, s)
	}
	a.Offer("sdr0", 100_000_000, testPairs(1024, 3), emit)
	a.Offer("sdr0", 100_000_000, testPairs(1024, 5), emit)
	if len(sums) != 2 || sums[0] != 3*2048 || sums[1] != 5*2048 {
		t.Fatalf("sums = %v, want [%d %d]", sums, 3*2048, 5*2048)
	}
}
