import { describe, it, expect } from 'vitest';
import { freqHz, bandwidthHz, powerDb, rateSps, relTime } from './format';
import { describeStatus } from './describeStatus';
import { classHex, classText, SIGNAL_CLASSES } from './classColor';

describe('format helpers (§4)', () => {
	it('formats frequencies with the right unit', () => {
		expect(freqHz(145_500_000)).toBe('145.500 MHz');
		expect(freqHz(1_250_000_000)).toBe('1.250 GHz');
		expect(freqHz(162_400)).toBe('162.4 kHz');
	});

	it('formats bandwidths', () => {
		expect(bandwidthHz(12_500)).toBe('12.5 kHz');
		expect(bandwidthHz(2_000_000)).toBe('2.0 MHz');
		expect(bandwidthHz(400)).toBe('400 Hz');
	});

	it('keeps the §5.6 power unit honest', () => {
		expect(powerDb(-62.14, true)).toEqual({ value: '-62.1', unit: 'dBm' });
		expect(powerDb(-62.14, false).unit).toBe('dB (rel.)');
	});

	it('formats sample rates', () => {
		expect(rateSps(48_000)).toBe('48 kS/s');
		expect(rateSps(1_024_000)).toBe('1.0 MS/s');
	});

	it('renders relative ages', () => {
		const now = Date.parse('2026-10-05T12:00:00Z');
		expect(relTime('2026-10-05T11:59:58Z', now)).toBe('2s ago');
		expect(relTime('2026-10-05T11:55:00Z', now)).toBe('5m ago');
		expect(relTime('2026-10-05T09:00:00Z', now)).toBe('3h ago');
		expect(relTime('not-a-date', now)).toBe('');
	});
});

describe('describeStatus (§2.2 fail-loud phrases)', () => {
	it('maps control-plane status codes to phrases', () => {
		expect(describeStatus(new Error('API error: 502'))).toBe('capture unreachable');
		expect(describeStatus(new Error('API error: 404'))).toBe('unknown device at capture');
		expect(describeStatus(new Error('API error: 409'))).toBe('device has no scan loop');
		expect(describeStatus(new Error('API error: 503'))).toBe('database unavailable');
	});

	it('keeps unknown errors visible instead of swallowing them', () => {
		expect(describeStatus(new Error('API error: 418'))).toBe('error 418');
		expect(describeStatus('weird failure')).toBe('weird failure');
	});
});

describe('classColor (§4 single source)', () => {
	it('covers every classification class with both maps', () => {
		for (const cls of SIGNAL_CLASSES) {
			expect(classHex(cls)).toMatch(/^#[0-9a-f]{6}$/);
			expect(classText(cls)).toMatch(/^text-/);
		}
	});

	it('defaults unknown classes to the unknown color', () => {
		expect(classHex('mystery')).toBe(classHex('unknown'));
		expect(classText('mystery')).toBe(classText('unknown'));
	});
});
