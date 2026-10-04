import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import { sdrs, sdrCount, selectSDR, applySDRStatus, type SDRStatus } from './sdrs';

function makeSDR(overrides: Partial<SDRStatus> = {}): SDRStatus {
	return {
		id: 'rtlsdr-0',
		model: 'RTL-SDR v3',
		freqHz: 146_520_000,
		gainDb: 40,
		bwHz: 2_000_000,
		active: true,
		...overrides
	};
}

beforeEach(() => {
	sdrs.set([]);
	sdrCount.set(0);
});

describe('sdrs store', () => {
	it('starts empty with count 0', () => {
		expect(get(sdrs)).toEqual([]);
		expect(get(sdrCount)).toBe(0);
	});

	it('selectSDR returns the matching SDR', () => {
		sdrs.set([makeSDR({ id: 'rtlsdr-0' }), makeSDR({ id: 'rtlsdr-1', freqHz: 433_920_000 })]);
		const found = selectSDR('rtlsdr-1');
		expect(found).toBeDefined();
		expect(found?.id).toBe('rtlsdr-1');
		expect(found?.freqHz).toBe(433_920_000);
	});

	it('selectSDR returns undefined when the SDR is missing', () => {
		sdrs.set([makeSDR({ id: 'rtlsdr-0' })]);
		expect(selectSDR('nope')).toBeUndefined();
	});

	it('selectSDR reflects store updates', () => {
		sdrs.set([makeSDR({ id: 'rtlsdr-0', freqHz: 100_000_000, active: true })]);
		sdrs.update(($s) => $s.map((s) => (s.id === 'rtlsdr-0' ? { ...s, freqHz: 146_000_000, active: false } : s)));
		const updated = selectSDR('rtlsdr-0');
		expect(updated?.freqHz).toBe(146_000_000);
		expect(updated?.active).toBe(false);
	});

	it('applySDRStatus appends unknown SDRs', () => {
		applySDRStatus(makeSDR({ id: 'rtlsdr-0' }));
		applySDRStatus(makeSDR({ id: 'rtlsdr-1', freqHz: 121_500_000 }));
		expect(get(sdrs).map((s) => s.id)).toEqual(['rtlsdr-0', 'rtlsdr-1']);
	});

	it('applySDRStatus replaces the matching SDR in place', () => {
		applySDRStatus(makeSDR({ id: 'rtlsdr-0', freqHz: 146_520_000, active: true }));
		applySDRStatus(makeSDR({ id: 'rtlsdr-0', freqHz: 121_500_000, active: false }));
		const list = get(sdrs);
		expect(list).toHaveLength(1);
		expect(list[0].freqHz).toBe(121_500_000);
		expect(list[0].active).toBe(false);
	});
});