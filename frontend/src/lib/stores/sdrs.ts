import { writable } from 'svelte/store';
import type { SDRDeviceStatus } from '$lib/api/client';

export interface SDRStatus {
	id: string;
	model: string;
	freqHz: number;
	gainDb: number;
	bwHz: number;
	active: boolean;
	/** Registered receiver position (§12.1); null = unplaced, never plotted. */
	lat: number | null;
	lon: number | null;
}

export const sdrs = writable<SDRStatus[]>([]);
export const sdrCount = writable(0);

/**
 * §7.4 live sweep state per device (`scanning` / `scanPaused`), shared
 * so the receiver rail, the map receiver rings, and the spectrum view
 * read one fetch instead of three (§5). Populated by ReceiverRail's
 * per-device status fetch and by setScan/retune responses.
 */
export const sdrRuntime = writable<Record<string, SDRDeviceStatus>>({});

/** Merges one live device status into the shared runtime cache. */
export function applySdrRuntime(st: SDRDeviceStatus): void {
	sdrRuntime.update(($r) => ({ ...$r, [st.id]: st }));
}

export function selectSDR(id: string): SDRStatus | undefined {
	let found: SDRStatus | undefined;
	sdrs.subscribe(($s) => {
		found = $s.find((s) => s.id === id);
	});
	return found;
}

/**
 * Inserts an SDR or replaces the existing one with the same id.
 * sdr.status events (§14.4.3) carry no position — a null lat/lon in
 * the update preserves the registered position from the REST
 * bootstrap instead of unplacing the device.
 */
export function applySDRStatus(status: SDRStatus): void {
	sdrs.update(($s) => {
		const i = $s.findIndex((s) => s.id === status.id);
		if (i === -1) return [...$s, status];
		const copy = [...$s];
		const prev = copy[i];
		copy[i] = {
			...status,
			lat: status.lat ?? prev.lat ?? null,
			lon: status.lon ?? prev.lon ?? null
		};
		return copy;
	});
}