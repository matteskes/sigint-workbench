import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import { applyTDOAEvent, clearTDOA, tdoaResults } from './tdoa';
import type { TDOAEvent } from '$lib/api/client';

function ev(id: string, over: Partial<TDOAEvent> = {}): TDOAEvent {
	return {
		signalId: id,
		freqHz: 145_500_000,
		at: '2026-10-05T12:00:00Z',
		accepted: false,
		persisted: false,
		receivers: ['rtlsdr-0', 'rtlsdr-1'],
		...over
	};
}

beforeEach(() => tdoaResults.set({}));

describe('tdoa store (§9.6/§14.2)', () => {
	it('keeps the latest attempt per signal (last event wins, §14.3)', () => {
		applyTDOAEvent(ev('sig-1', { reason: 'insufficient_pairs' }));
		applyTDOAEvent(ev('sig-2', { accepted: true, persisted: true, reference: 'rtlsdr-0' }));
		expect(Object.keys(get(tdoaResults))).toEqual(['sig-1', 'sig-2']);
		expect(get(tdoaResults)['sig-1'].reason).toBe('insufficient_pairs');

		applyTDOAEvent(
			ev('sig-1', {
				accepted: true,
				persisted: true,
				reference: 'rtlsdr-0',
				fix: { lat: 40.7, lng: -74, residualNs: 41, pairsUsed: 3, maxBaselineM: 8000, covPosDef: true }
			})
		);
		expect(get(tdoaResults)['sig-1'].accepted).toBe(true);
		expect(get(tdoaResults)['sig-1'].fix?.pairsUsed).toBe(3);
		expect(get(tdoaResults)['sig-1'].fix?.covPosDef).toBe(true);
	});

	it('carries the locus surface through untouched', () => {
		applyTDOAEvent(ev('sig-3', { locus: { lat1: 40.7, lng1: -74, lat2: 40.8, lng2: -73.9 } }));
		const stored = get(tdoaResults)['sig-3'];
		expect(stored.locus?.lat1).toBeCloseTo(40.7);
		expect(stored.locus?.lng2).toBeCloseTo(-73.9);
		expect(stored.accepted).toBe(false);
	});

	it('drops the entry with the signal (signal.removed)', () => {
		applyTDOAEvent(ev('sig-1'));
		clearTDOA('sig-1');
		expect(get(tdoaResults)['sig-1']).toBeUndefined();
		// Clearing an unknown id must not rewrite the store.
		const before = get(tdoaResults);
		clearTDOA('nope');
		expect(get(tdoaResults)).toBe(before);
	});

	it('ignores malformed payloads', () => {
		applyTDOAEvent(undefined as unknown as TDOAEvent);
		applyTDOAEvent(ev(''));
		expect(get(tdoaResults)).toEqual({});
	});
});
