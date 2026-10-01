import { writable } from 'svelte/store';

export interface AudioState {
	playing: boolean;
	volume: number;
	level: number; // 0.0 to 1.0 (RMS)
	signalId: string | null;
}

export const audioState = writable<AudioState>({
	playing: false,
	volume: 0.8,
	level: 0,
	signalId: null
});

export const isPlaying = audioState
	.then ? audioState : undefined; // placeholder, Svelte 5 runes will handle this