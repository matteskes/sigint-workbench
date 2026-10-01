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