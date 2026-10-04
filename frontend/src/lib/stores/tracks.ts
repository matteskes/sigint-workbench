import { writable } from 'svelte/store';

/**
 * §9.4 movement state per signal. `path` is only populated by the REST
 * fetch (GET /api/signals/{id}/track); track.update events carry the
 * summary alone and merge into the existing entry.
 */
export interface TrackState {
	signalId: string;
	speedKmh: number;
	headingDeg: number;
	isMoving: boolean;
	lat: number | null;
	lon: number | null;
	path: { lat: number; lon: number }[];
	updatedAt: number; // Date.now() of the last update
}

export const tracks = writable<Record<string, TrackState>>({});

/** Applies a track.update event summary (§14.2), keeping any path. */
export function applyTrackUpdate(t: {
	signalId: string;
	lat?: number;
	lon?: number;
	speedKmh?: number;
	headingDeg?: number;
	isMoving?: boolean;
}): void {
	tracks.update(($t) => {
		const prev = $t[t.signalId];
		return {
			...$t,
			[t.signalId]: {
				signalId: t.signalId,
				speedKmh: t.speedKmh ?? prev?.speedKmh ?? 0,
				headingDeg: t.headingDeg ?? prev?.headingDeg ?? 0,
				isMoving: t.isMoving ?? prev?.isMoving ?? false,
				lat: t.lat ?? prev?.lat ?? null,
				lon: t.lon ?? prev?.lon ?? null,
				path: prev?.path ?? [],
				updatedAt: Date.now()
			}
		};
	});
}

/** Replaces the entry with a full REST track (path included, §9.4). */
export function setTrack(t: {
	signalId: string;
	path: { lat: number; lon: number }[];
	speedKmh: number;
	headingDeg: number;
	updatedAt: string;
}): void {
	tracks.update(($t) => ({
		...$t,
		[t.signalId]: {
			signalId: t.signalId,
			speedKmh: t.speedKmh,
			headingDeg: t.headingDeg,
			// §9.4: IsMoving at > 1 km/h.
			isMoving: t.speedKmh > 1,
			lat: t.path.length ? t.path[t.path.length - 1].lat : null,
			lon: t.path.length ? t.path[t.path.length - 1].lon : null,
			path: t.path,
			updatedAt: Date.now()
		}
	}));
}

/** Drops one signal's track (signal.removed). */
export function clearTrack(signalId: string): void {
	tracks.update(($t) => {
		if (!(signalId in $t)) return $t;
		const out = { ...$t };
		delete out[signalId];
		return out;
	});
}