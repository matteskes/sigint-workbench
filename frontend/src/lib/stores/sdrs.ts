import { writable } from 'svelte/store';

export interface SDRStatus {
	id: string;
	model: string;
	freqHz: number;
	gainDb: number;
	bwHz: number;
	active: boolean;
}

export const sdrs = writable<SDRStatus[]>([]);
export const sdrCount = writable(0);

export function selectSDR(id: string): SDRStatus | undefined {
	let found: SDRStatus | undefined;
	sdrs.subscribe(($s) => {
		found = $s.find((s) => s.id === id);
	});
	return found;
}

/** Inserts an SDR or replaces the existing one with the same id. */
export function applySDRStatus(status: SDRStatus): void {
	sdrs.update(($s) => {
		const i = $s.findIndex((s) => s.id === status.id);
		if (i === -1) return [...$s, status];
		const copy = [...$s];
		copy[i] = status;
		return copy;
	});
}