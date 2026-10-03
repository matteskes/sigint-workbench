import { get } from 'svelte/store';
import { describe, it, expect, beforeEach } from 'vitest';
import { audioState, isPlaying, type AudioState } from './audio';

const defaultState: AudioState = {
	playing: false,
	volume: 0.8,
	level: 0,
	signalId: null
};

beforeEach(() => {
	audioState.set(defaultState);
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