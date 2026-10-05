import { writable, derived } from 'svelte/store';

export interface Signal {
	id: string;
	freqHz: number;
	bandwidthHz: number;
	modulation: string;
	subType: string;
	class: string;
	confidence: number;
	powerDbm: number;
	/** §5.6: false = powerDbm is uncalibrated relative dB (label "dB (rel.)"). */
	powerCalibrated: boolean;
	lat: number | null;
	lon: number | null;
	accuracyM: number;
	firstSeen: string;
	lastSeen: string;
	sdrId: string;
	verified: boolean;
}

export const signals = writable<Signal[]>([]);
export const selectedSignal = writable<Signal | null>(null);

export const signalCount = derived(signals, ($s) => $s.length);

export const filteredSignals = (filter: (s: Signal) => boolean) =>
	derived(signals, ($s) => $s.filter(filter));

/** Inserts a signal or replaces the existing one with the same id. */
export function upsertSignal(sig: Signal): void {
	signals.update(($s) => {
		const i = $s.findIndex((x) => x.id === sig.id);
		if (i === -1) return [sig, ...$s];
		const copy = [...$s];
		copy[i] = sig;
		return copy;
	});
}

/**
 * §14.3 coalesced ingest: applies a batch of signal.new/update
 * payloads in a single store write. Last write in the batch wins per
 * id (a coalesced payload always supersedes earlier ones); signals
 * not yet present are prepended newest-first — the same semantics as
 * upsertSignal, at UI rate instead of detector rate. Every $signals
 * consumer (list diff, map rebuild) re-renders once per batch, not
 * once per detector event.
 */
export function upsertSignals(batch: Signal[]): void {
	if (batch.length === 0) return;
	signals.update(($s) => {
		const pos = new Map<string, number>();
		for (let i = 0; i < $s.length; i++) pos.set($s[i].id, i);
		const next = $s.slice();
		const added: Signal[] = [];
		const addedPos = new Map<string, number>();
		for (const sig of batch) {
			const i = pos.get(sig.id);
			if (i === undefined) {
				// Not in the store — but it may already be pending in this
				// batch; the later payload must win (§14.3 last-wins).
				const j = addedPos.get(sig.id);
				if (j === undefined) {
					addedPos.set(sig.id, added.length);
					added.push(sig);
				} else {
					added[j] = sig;
				}
			} else {
				next[i] = sig;
			}
		}
		if (added.length === 0) return next;
		added.reverse();
		return [...added, ...next];
	});
}

/** Removes a signal by id and clears the selection if it was selected. */
export function removeSignal(id: string): void {
	signals.update(($s) => $s.filter((x) => x.id !== id));
	selectedSignal.update((sel) => (sel?.id === id ? null : sel));
}