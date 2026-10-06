import { writable } from 'svelte/store';
import type { TDOAEvent } from '$lib/api/client';

/**
 * §9.6/§14.2: the latest signal.tdoa attempt per signal. The Inspector's
 * TDOA section renders it and MapView draws the locus for the selected
 * signal. Last event wins (§14.3 lossy-by-design — the next attempt or
 * an accepted fix's signal.update self-heals the state); entries are
 * cleared when the signal is removed.
 */
export const tdoaResults = writable<Record<string, TDOAEvent>>({});

/** Applies one signal.tdoa event (ignored when the payload is broken). */
export function applyTDOAEvent(ev: TDOAEvent): void {
	if (!ev?.signalId) return;
	tdoaResults.update(($t) => ({ ...$t, [ev.signalId]: ev }));
}

/** Drops one signal's TDOA state (signal.removed). */
export function clearTDOA(signalId: string): void {
	tdoaResults.update(($t) => {
		if (!(signalId in $t)) return $t;
		const out = { ...$t };
		delete out[signalId];
		return out;
	});
}
