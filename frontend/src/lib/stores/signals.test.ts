import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import { signals, selectedSignal, signalCount, filteredSignals, type Signal } from './signals';

function makeSignal(overrides: Partial<Signal> = {}): Signal {
	return {
		id: 'sig-1',
		freqHz: 146_520_000,
		bandwidthHz: 12_000,
		modulation: 'FM',
		subType: '',
		class: 'signal',
		confidence: 0.9,
		powerDbm: -70,
		lat: 50,
		lon: 30,
		accuracyM: 100,
		firstSeen: new Date().toISOString(),
		lastSeen: new Date().toISOString(),
		sdrId: 'rtlsdr-0',
		verified: false,
		...overrides
	};
}

beforeEach(() => {
	signals.set([]);
	selectedSignal.set(null);
});

describe('signals store', () => {
	it('starts empty with count 0', () => {
		expect(get(signals)).toEqual([]);
		expect(get(signalCount)).toBe(0);
	});

	it('signalCount tracks the array length', () => {
		signals.set([makeSignal(), makeSignal({ id: 'sig-2' })]);
		expect(get(signalCount)).toBe(2);
		signals.update(($s) => $s.slice(0, 1));
		expect(get(signalCount)).toBe(1);
	});

	it('selectedSignal holds a single signal or null', () => {
		expect(get(selectedSignal)).toBeNull();
		const s = makeSignal();
		selectedSignal.set(s);
		expect(get(selectedSignal)).toEqual(s);
		selectedSignal.set(null);
		expect(get(selectedSignal)).toBeNull();
	});

	it('filteredSignals applies the predicate', () => {
		signals.set([
			makeSignal({ id: 'a', verified: true }),
			makeSignal({ id: 'b', verified: false }),
			makeSignal({ id: 'c', verified: true })
		]);
		const verified = filteredSignals((s) => s.verified);
		expect(get(verified).map((s) => s.id)).toEqual(['a', 'c']);
	});

	it('filteredSignals reacts to store updates', () => {
		const wide = filteredSignals((s) => s.bandwidthHz >= 15_000);
		signals.set([makeSignal({ id: 'a', bandwidthHz: 12_000 })]);
		expect(get(wide)).toEqual([]);
		signals.update(($s) => [...$s, makeSignal({ id: 'b', bandwidthHz: 50_000 })]);
		expect(get(wide).map((s) => s.id)).toEqual(['b']);
	});
});