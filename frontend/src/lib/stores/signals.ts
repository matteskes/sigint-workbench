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