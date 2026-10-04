import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import { tracks, applyTrackUpdate, setTrack, clearTrack } from './tracks';

beforeEach(() => {
	tracks.set({});
});

describe('tracks store (§9.4)', () => {
	it('applies track.update summaries, latest wins', () => {
		applyTrackUpdate({ signalId: 'sig-1', speedKmh: 5, headingDeg: 90, isMoving: true, lat: 40, lon: -74 });
		applyTrackUpdate({ signalId: 'sig-1', speedKmh: 7, headingDeg: 45, isMoving: true, lat: 40.001, lon: -74 });
		const t = get(tracks)['sig-1'];
		expect(t.speedKmh).toBe(7);
		expect(t.headingDeg).toBe(45);
		expect(t.lat).toBeCloseTo(40.001);
	});

	it('keeps the fetched path across summary updates', () => {
		setTrack({
			signalId: 'sig-1',
			path: [{ lat: 40, lon: -74 }, { lat: 40.001, lon: -74 }],
			speedKmh: 11,
			headingDeg: 0,
			updatedAt: '2026-10-04T00:00:00Z'
		});
		applyTrackUpdate({ signalId: 'sig-1', speedKmh: 12, headingDeg: 10, isMoving: true });
		const t = get(tracks)['sig-1'];
		expect(t.path).toHaveLength(2);
		expect(t.speedKmh).toBe(12);
	});

	it('derives isMoving from the 1 km/h threshold on REST tracks', () => {
		setTrack({ signalId: 'sig-2', path: [], speedKmh: 0.5, headingDeg: 0, updatedAt: 'x' });
		expect(get(tracks)['sig-2'].isMoving).toBe(false);
	});

	it('clears one signal without touching the rest', () => {
		applyTrackUpdate({ signalId: 'sig-1' });
		applyTrackUpdate({ signalId: 'sig-2' });
		clearTrack('sig-1');
		const $t = get(tracks);
		expect($t['sig-1']).toBeUndefined();
		expect($t['sig-2']).toBeDefined();
	});
});