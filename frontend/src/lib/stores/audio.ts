import { writable, derived } from 'svelte/store';

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

export const isPlaying = derived(audioState, ($s) => $s.playing);

/**
 * §10.6 audio.level — the latest coarse level per actively demodulated
 * signal. Entries expire via pruneSignalLevels once the feed stops
 * (session finalized ⇒ events stop arriving).
 */
export interface SignalLevel {
	level: number; // 0..1
	at: number; // Date.now() of the last event
}

export const signalLevels = writable<Record<string, SignalLevel>>({});

// The feed runs at ≤ 10 Hz; an entry older than this saw no events
// for ~15 ticks and is stale.
const LEVEL_TTL_MS = 1500;

/** Applies one audio.level event ({signalId, level 0..1}). */
export function applyAudioLevel(signalId: string, level: number): void {
	signalLevels.update(($l) => ({ ...$l, [signalId]: { level, at: Date.now() } }));
}

/** Drops level entries whose signal stopped producing events. */
export function pruneSignalLevels(now: number = Date.now()): void {
	signalLevels.update(($l) => {
		let stale = false;
		for (const entry of Object.values($l)) {
			if (now - entry.at > LEVEL_TTL_MS) {
				stale = true;
				break;
			}
		}
		if (!stale) return $l;
		const out: Record<string, SignalLevel> = {};
		for (const [id, entry] of Object.entries($l)) {
			if (now - entry.at <= LEVEL_TTL_MS) out[id] = entry;
		}
		return out;
	});
}

/** Removes one signal's level entry (signal removed / session ended). */
export function clearSignalLevel(signalId: string): void {
	signalLevels.update(($l) => {
		if (!(signalId in $l)) return $l;
		const out = { ...$l };
		delete out[signalId];
		return out;
	});
}

// ─── WAV playback singleton (§9: one recording at a time) ────────────

let activeWavStop: (() => void) | null = null;

/**
 * Claims exclusive WAV playback: stops whatever recording is playing,
 * marks the store. Returns the previous owner's stop having been
 * called (F4: one stream at a time, made explicit).
 */
export function claimWavPlayback(sourceId: string, stop: () => void): void {
	if (activeWavStop) activeWavStop();
	activeWavStop = stop;
	audioState.update((s) => ({ ...s, playing: true, signalId: sourceId, level: 0 }));
}

/** Releases the claim when `sourceId` still owns it (stop/ended). */
export function releaseWavPlayback(sourceId: string): void {
	if (!activeWavStop) return;
	activeWavStop = null;
	audioState.update((s) => (s.playing && s.signalId === sourceId ? { ...s, playing: false, level: 0 } : s));
}