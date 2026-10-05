import { describe, it, expect } from 'vitest';
import {
	TFR_METHODS,
	TFR_WINDOWS,
	TFR_MAX_SPAN_S,
	spectrumSelection,
	spanForRecording,
	freqSpanForRecording,
	dragToSelection,
	type SpectrumSelection
} from './tfr';

const SEL: SpectrumSelection = {
	freqLoHz: 145_990_000,
	freqHiHz: 146_010_000,
	startMs: Date.parse('2026-10-05T12:00:10Z'),
	endMs: Date.parse('2026-10-05T12:00:14Z')
};

describe('method metadata (§19.2/§19.4)', () => {
	it('covers all four methods with artifact notes', () => {
		const methods = Object.keys(TFR_METHODS);
		expect(methods).toHaveLength(4);
		for (const m of methods) {
			expect(TFR_METHODS[m as keyof typeof TFR_METHODS].artifact.length).toBeGreaterThan(10);
		}
	});

	it('marks reassignment as windowed but rectangular-hostile', () => {
		expect(TFR_METHODS.reassigned.windowed).toBe(true);
		expect(TFR_METHODS.reassigned.noRectangular).toBe(true);
		expect(TFR_METHODS.stft.windowed).toBe(true);
		expect(TFR_METHODS.spwvd.windowed).toBe(false);
		expect(TFR_METHODS['cwt-morlet'].windowed).toBe(false);
	});

	it('offers exactly the §19.2 window set', () => {
		expect(TFR_WINDOWS).toEqual(['hamming', 'gaussian', 'rectangular']);
	});
});

describe('spanForRecording', () => {
	const rec = { startTime: '2026-10-05T12:00:05Z', durationS: 60 };

	it('maps an overlapping selection to recording-local seconds', () => {
		const span = spanForRecording(SEL, rec);
		expect(span).not.toBeNull();
		expect(span![0]).toBeCloseTo(5, 3); // 12:00:10 − 12:00:05
		expect(span![1]).toBeCloseTo(9, 3);
	});

	it('normalizes reversed drag order', () => {
		const span = spanForRecording({ ...SEL, startMs: SEL.endMs, endMs: SEL.startMs }, rec);
		expect(span![0]).toBeCloseTo(5, 3);
		expect(span![1]).toBeCloseTo(9, 3);
	});

	it('clamps to the recording window', () => {
		// Selection reaches past both ends; the intersection is the
		// recording's final 15 s.
		const sel = { ...SEL, startMs: Date.parse('2026-10-05T12:00:50Z'), endMs: Date.parse('2026-10-05T13:00:00Z') };
		const span = spanForRecording(sel, rec)!;
		expect(span[0]).toBeCloseTo(45, 3);
		expect(span[1]).toBeCloseTo(60, 3);
	});

	it('caps the span at the §19.5 limit', () => {
		const sel = { ...SEL, startMs: Date.parse('2026-10-05T12:00:00Z'), endMs: Date.parse('2026-10-05T12:40:00Z') };
		const span = spanForRecording(sel, rec)!;
		expect(span[1] - span[0]).toBeCloseTo(TFR_MAX_SPAN_S, 3);
	});

	it('returns null without overlap (caller falls back)', () => {
		const sel = { ...SEL, startMs: Date.parse('2026-10-05T13:00:00Z'), endMs: Date.parse('2026-10-05T13:00:05Z') };
		expect(spanForRecording(sel, rec)).toBeNull();
	});
});

describe('freqSpanForRecording', () => {
	it('is undefined without a selection (full band)', () => {
		expect(freqSpanForRecording(null, 146_000_000, 100_000)).toBeUndefined();
	});

	it('clamps the selection into the recording band', () => {
		const wide: SpectrumSelection = { ...SEL, freqLoHz: 145_900_000, freqHiHz: 146_100_000 };
		const span = freqSpanForRecording(wide, 146_000_000, 100_000)!;
		expect(span[0]).toBe(145_950_000); // center − fs/2
		expect(span[1]).toBe(146_050_000); // center + fs/2
	});

	it('passes a contained span through untouched', () => {
		const span = freqSpanForRecording(SEL, 146_000_000, 1_000_000)!;
		expect(span[0]).toBe(SEL.freqLoHz);
		expect(span[1]).toBe(SEL.freqHiHz);
	});

	it('is undefined for a disjoint band', () => {
		const sel: SpectrumSelection = { ...SEL, freqLoHz: 100, freqHiHz: 200 };
		expect(freqSpanForRecording(sel, 146_000_000, 100_000)).toBeUndefined();
	});
});

describe('dragToSelection', () => {
	const FREQ_LO = 100;
	const FREQ_HI = 200;

	it('maps pixel columns onto the frequency span', () => {
		const sel = dragToSelection(0, 256, 256, FREQ_LO, FREQ_HI, 1000, 2000)!;
		expect(sel.freqLoHz).toBeCloseTo(100, 6);
		expect(sel.freqHiHz).toBeCloseTo(200, 6);
		expect(sel.startMs).toBe(1000);
		expect(sel.endMs).toBe(2000);
	});

	it('rejects degenerate drags (click clears)', () => {
		expect(dragToSelection(10, 11, 256, FREQ_LO, FREQ_HI, 0, 1000)).toBeNull();
	});

	it('orders reversed drags', () => {
		const sel = dragToSelection(200, 50, 256, FREQ_LO, FREQ_HI, 2000, 1000)!;
		expect(sel.freqLoHz).toBeLessThan(sel.freqHiHz);
		expect(sel.startMs).toBe(1000);
	});

	it('clamps out-of-canvas columns', () => {
		const sel = dragToSelection(-20, 9999, 256, FREQ_LO, FREQ_HI, 0, 1)!;
		expect(sel.freqLoHz).toBeCloseTo(FREQ_LO, 6);
		expect(sel.freqHiHz).toBeCloseTo(FREQ_HI, 6);
	});
});

describe('spectrumSelection store', () => {
	it('holds the draft and clears', () => {
		spectrumSelection.set(SEL);
		spectrumSelection.subscribe((v) => {
			if (v) expect(v.freqHiHz).toBe(SEL.freqHiHz);
		})();
		spectrumSelection.set(null);
	});
});
