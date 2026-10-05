import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import {
	spectrum,
	spectrumSdrIds,
	selectedSdrId,
	selectedSpectrum,
	applySpectrumFrame,
	clearSpectrum,
	isSpectrumFrame,
	WATERFALL_ROWS
} from './spectrum';
import type { SpectrumFrame } from '../api/client';

function frame(over: Partial<SpectrumFrame> = {}): SpectrumFrame {
	return {
		sdrId: 'sim0',
		freqHz: 100_000_000,
		sampleRate: 2_400_000,
		t: '2026-10-04T12:00:00Z',
		bins: 4,
		df: 585.9375,
		db: [-87.3, -84.1, -90, -88.5],
		...over
	};
}

beforeEach(() => {
	spectrum.set({});
	selectedSdrId.set(null);
});

describe('spectrum store (§18.3)', () => {
	it('keeps the latest frame plus a FIFO-capped history per SDR', () => {
		for (let i = 0; i < WATERFALL_ROWS + 5; i++) {
			const t = new Date(Date.UTC(2026, 9, 4, 12, 0, 0, i)).toISOString();
			applySpectrumFrame(frame({ t, db: [i, i, i, i] }));
		}
		const st = get(spectrum)['sim0'];
		expect(st.rows).toHaveLength(WATERFALL_ROWS);
		expect(st.latest.db[0]).toBe(WATERFALL_ROWS + 4); // newest kept
		expect(st.rows[0].db[0]).toBe(5); // oldest five evicted
	});

	it('is idempotent per t: duplicate frames add no rows', () => {
		expect(applySpectrumFrame(frame())).toBe(true);
		expect(applySpectrumFrame(frame())).toBe(false); // same t
		const st = get(spectrum)['sim0'];
		expect(st.rows).toHaveLength(1);
		expect(st.latest.t).toBe('2026-10-04T12:00:00Z');
	});

	it('rejects out-of-order (older) frames without rewinding', () => {
		applySpectrumFrame(frame({ t: '2026-10-04T12:00:01Z' }));
		expect(applySpectrumFrame(frame({ t: '2026-10-04T12:00:00.9Z' }))).toBe(false);
		const st = get(spectrum)['sim0'];
		expect(st.rows).toHaveLength(1);
		expect(st.latest.t).toBe('2026-10-04T12:00:01Z');
	});

	it('tolerates gaps — a frame after silence is just appended', () => {
		applySpectrumFrame(frame({ t: '2026-10-04T12:00:00Z' }));
		// 30 s of retune silence (§18.1): nothing arrives, nothing is
		// synthesized, and the next received frame is fresh data.
		expect(applySpectrumFrame(frame({ t: '2026-10-04T12:00:30Z' }))).toBe(true);
		expect(get(spectrum)['sim0'].rows).toHaveLength(2);
	});

	it('treats malformed frames as no-ops (§14.3)', () => {
		const bad: unknown[] = [
			null,
			42,
			frame({ sdrId: '' }),
			frame({ sampleRate: 0 }),
			frame({ t: 'not-a-time' }),
			frame({ bins: 3 }), // bins ≠ len(db)
			frame({ df: -1 }),
			frame({ db: [-1, Number.NaN, -3, -4] }),
			frame({ db: 'nope' as unknown as number[] })
		];
		for (const b of bad) {
			expect(applySpectrumFrame(b)).toBe(false);
		}
		expect(get(spectrum)).toEqual({});
		expect(isSpectrumFrame(frame())).toBe(true);
	});

	it('keeps per-SDR state isolated and lists ids sorted', () => {
		applySpectrumFrame(frame({ sdrId: 'b', t: '2026-10-04T12:00:00Z' }));
		applySpectrumFrame(frame({ sdrId: 'a', t: '2026-10-04T12:00:01Z' }));
		applySpectrumFrame(frame({ sdrId: 'a', t: '2026-10-04T12:00:02Z' }));
		const $s = get(spectrum);
		expect($s['a'].rows).toHaveLength(2);
		expect($s['b'].rows).toHaveLength(1);
		expect(get(spectrumSdrIds)).toEqual(['a', 'b']);
	});

	it('selectedSpectrum follows the selector and falls back to the first SDR', () => {
		expect(get(selectedSpectrum)).toBeNull();
		applySpectrumFrame(frame({ sdrId: 'b' }));
		applySpectrumFrame(frame({ sdrId: 'a' }));
		// No explicit selection yet → first known SDR.
		expect(get(selectedSpectrum)?.latest.sdrId).toBe('a');
		selectedSdrId.set('b');
		expect(get(selectedSpectrum)?.latest.sdrId).toBe('b');
	});

	it('clearSpectrum drops one SDR, or everything without an id', () => {
		applySpectrumFrame(frame({ sdrId: 'a' }));
		applySpectrumFrame(frame({ sdrId: 'b' }));
		clearSpectrum('a');
		expect(get(spectrumSdrIds)).toEqual(['b']);
		clearSpectrum();
		expect(get(spectrum)).toEqual({});
	});
});