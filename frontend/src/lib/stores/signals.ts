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

/** Removes a signal by id and clears the selection if it was selected. */
export function removeSignal(id: string): void {
	signals.update(($s) => $s.filter((x) => x.id !== id));
	selectedSignal.update((sel) => (sel?.id === id ? null : sel));
}