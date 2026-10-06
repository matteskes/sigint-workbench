// §3.2/§13: service health chips. GET /api/setup/status is the
// documented fan-out exception (§13) — the gateway probes db,
// ws-hub, recorder and capture itself. No WS event exists for service
// health, so this is one of the two permitted pollers (30 s, §17).

import { writable } from 'svelte/store';
import { fetchSetupStatus } from '$lib/api/client';

export interface ComponentStatus {
	ok: boolean;
	detail?: string;
}

export interface HealthState {
	components: Record<string, ComponentStatus>;
	allOk: boolean;
	configDirWritable: boolean;
	/** Date.now() of the last completed probe (success or failure). */
	checkedAt: number;
	/** Set when the probe itself fails — chips show stale data honestly. */
	probeError: string;
}

export const HEALTH_POLL_MS = 30_000;

export const health = writable<HealthState>({
	components: {},
	allOk: false,
	configDirWritable: true,
	checkedAt: 0,
	probeError: ''
});

export async function refreshHealth(): Promise<void> {
	try {
		const st = await fetchSetupStatus();
		health.set({
			components: st.components ?? {},
			allOk: Boolean(st.all_ok),
			configDirWritable: st.config_dir_writable !== false,
			checkedAt: Date.now(),
			probeError: ''
		});
	} catch {
		// Keep the last known component states; flag the staleness.
		health.update(($h) => ({ ...$h, checkedAt: Date.now(), probeError: 'status probe failed' }));
	}
}

let timer: ReturnType<typeof setInterval> | null = null;

/** Starts the 30 s poll (idempotent). The layout calls this on mount. */
export function startHealthPolling(): void {
	if (timer) return;
	void refreshHealth();
	timer = setInterval(refreshHealth, HEALTH_POLL_MS);
}

export function stopHealthPolling(): void {
	if (timer) {
		clearInterval(timer);
		timer = null;
	}
}
