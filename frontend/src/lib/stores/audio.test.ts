import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import {
	audioState,
	isPlaying,
	signalLevels,
	applyAudioLevel,
	pruneSignalLevels,
	clearSignalLevel,
	type AudioState
} from './audio';

const defaultState: AudioState = {
	playing: false,
	volume: 0.8,
	level: 0,
	signalId: null
};

beforeEach(() => {
	audioState.set(defaultState);
	signalLevels.set({});
});

describe('audio store', () => {
	it('has expected defaults after reset', () => {
		expect(get(audioState)).toEqual(defaultState);
	});

	it('isPlaying derives from audioState.playing', () => {
		expect(get(isPlaying)).toBe(false);
		audioState.update((s) => ({ ...s, playing: true }));
		expect(get(isPlaying)).toBe(true);
		audioState.update((s) => ({ ...s, playing: false }));
		expect(get(isPlaying)).toBe(false);
	});

	it('carries signalId and level through updates', () => {
		audioState.update((s) => ({ ...s, playing: true, signalId: 'sig-42', level: 0.35 }));
		const state = get(audioState);
		expect(state.signalId).toBe('sig-42');
		expect(state.level).toBeCloseTo(0.35);
		expect(get(isPlaying)).toBe(true);
	});
});

describe('audio.level store (§10.6)', () => {
	it('keeps the latest level per signal', () => {
		applyAudioLevel('sig-1', 0.2);
		applyAudioLevel('sig-2', 0.9);
		applyAudioLevel('sig-1', 0.5);
		const $l = get(signalLevels);
		expect($l['sig-1'].level).toBeCloseTo(0.5);
		expect($l['sig-2'].level).toBeCloseTo(0.9);
	});

	it('prunes only entries that stopped receiving events', () => {
		applyAudioLevel('sig-1', 0.4);
		applyAudioLevel('sig-2', 0.6);
		pruneSignalLevels(Date.now()); // fresh — nothing pruned
		expect(Object.keys(get(signalLevels))).toHaveLength(2);
		pruneSignalLevels(Date.now() + 60_000); // long past the TTL
		expect(get(signalLevels)).toEqual({});
	});

	it('clears one signal without touching the rest', () => {
		applyAudioLevel('sig-1', 0.4);
		applyAudioLevel('sig-2', 0.6);
		clearSignalLevel('sig-1');
		const $l = get(signalLevels);
		expect($l['sig-1']).toBeUndefined();
		expect($l['sig-2']).toBeDefined();
	});
});