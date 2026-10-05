<script lang="ts">
	import { sdrs, applySDRStatus } from '$lib/stores/sdrs';
	import {
		retuneSdr,
		setScan,
		fetchSDRStatus,
		type SDRDeviceStatus
	} from '$lib/api/client';

	// §7.4 control surface: a manual tune PUTs through the gateway,
	// which persists the row AND forwards to sdr-capture (§13.1:
	// capture unreachable ⇒ 502, unknown device there ⇒ 404). The WS
	// `sdr.status` feed stays the source of truth (§14.4.3); the
	// local store update is only immediate feedback.
	let busy = $state<Record<string, boolean>>({});
	let errors = $state<Record<string, string>>({});

	// Live scan state per device (§7.4): `scanning` = a D3 sweep loop
	// is attached; `scanPaused` = parked (boot park via
	// scan_autostart: false, or a manual tune). Refreshed on mount,
	// after a tune and after toggling.
	let scan = $state<Record<string, SDRDeviceStatus>>({});
	let requested = $state<Record<string, boolean>>({});

	function describe(e: unknown): string {
		const m = /(\d{3})\s*$/.exec(String(e));
		if (!m) return String(e);
		const status = Number(m[1]);
		if (status === 502) return 'capture unreachable';
		if (status === 404) return 'unknown device at capture';
		if (status === 409) return 'device has no scan loop';
		if (status === 503) return 'database unavailable';
		return `error ${status}`;
	}

	async function refreshStatus(id: string): Promise<void> {
		try {
			const st = await fetchSDRStatus(id);
			scan = { ...scan, [id]: st };
		} catch {
			// Best-effort: the toggle needs capture up anyway, and
			// freq/gain keep flowing via sdr.status regardless.
		}
	}

	// One status fetch per device as it appears (monitors too — the
	// fetch is what tells us there is no sweep toggle to render).
	$effect(() => {
		for (const s of $sdrs) {
			if (!requested[s.id]) {
				requested = { ...requested, [s.id]: true };
				void refreshStatus(s.id);
			}
		}
	});

	async function tune(sdr: { id: string; freqHz: number }, freqMHz: number): Promise<void> {
		errors = { ...errors, [sdr.id]: '' };
		const freqHz = Math.round(freqMHz * 1e6);
		// Invalid or unchanged input: no request (the PUT is partial,
		// so re-sending the current frequency would be a no-op retune).
		if (!Number.isFinite(freqHz) || freqHz === sdr.freqHz) return;
		busy = { ...busy, [sdr.id]: true };
		try {
			applySDRStatus(await retuneSdr(sdr.id, freqHz));
			// §7.4: a manual tune parks the sweep — refresh the badge.
			void refreshStatus(sdr.id);
		} catch (e) {
			errors = { ...errors, [sdr.id]: describe(e) };
		} finally {
			busy = { ...busy, [sdr.id]: false };
		}
	}

	async function toggleScan(id: string): Promise<void> {
		const cur = scan[id];
		if (!cur) return;
		errors = { ...errors, [id]: '' };
		busy = { ...busy, [id]: true };
		try {
			const st = await setScan(id, cur.scanPaused);
			scan = { ...scan, [id]: st };
			applySDRStatus(st);
		} catch (e) {
			errors = { ...errors, [id]: describe(e) };
		} finally {
			busy = { ...busy, [id]: false };
		}
	}
</script>

<div class="p-3 space-y-3">
	<div class="text-xs font-semibold text-slate-400">SDR Controls</div>

	{#each $sdrs as sdr (sdr.id)}
		<div class="space-y-2 p-2 bg-slate-800 rounded">
			<div class="flex items-center justify-between">
				<span class="text-sm font-medium">{sdr.id}</span>
				<span
					class="text-xs {sdr.active ? 'text-green-400' : 'text-slate-500'}"
				>
					● {sdr.active ? 'active' : 'idle'}
				</span>
			</div>
			<div class="text-xs text-slate-500">{sdr.model}</div>
			<div class="text-xs font-mono text-slate-400">
				{(sdr.freqHz / 1e6).toFixed(2)} MHz | {sdr.gainDb} dB
			</div>
			<input
				type="number"
				step="0.01"
				min="0.024"
				max="6000"
				value={(sdr.freqHz / 1e6).toFixed(2)}
				class="w-full px-2 py-1 text-xs bg-slate-900 border border-slate-700 rounded
					text-slate-100 focus:outline-none focus:border-blue-500
					disabled:opacity-50"
				disabled={busy[sdr.id]}
				onblur={(e) => tune(sdr, parseFloat(e.currentTarget.value))}
				aria-label="Frequency (MHz) for {sdr.id}"
			/>
			{#if scan[sdr.id]?.scanning}
				<div class="flex items-center justify-between">
					<button
						class="rounded px-2 py-0.5 text-xs disabled:opacity-50
							{scan[sdr.id].scanPaused
							? 'bg-green-900/60 text-green-200 hover:bg-green-800'
							: 'bg-amber-900/60 text-amber-200 hover:bg-amber-800'}"
						disabled={busy[sdr.id]}
						onclick={() => toggleScan(sdr.id)}
					>
						{scan[sdr.id].scanPaused ? 'Start sweep' : 'Stop sweep'}
					</button>
					<span
						class="text-xs {scan[sdr.id].scanPaused
							? 'text-amber-400'
							: 'text-green-400'}"
					>
						{scan[sdr.id].scanPaused ? '■ parked' : '▶ sweeping'}
					</span>
				</div>
			{/if}
			{#if busy[sdr.id]}
				<div class="text-xs text-amber-400">tuning…</div>
			{:else if errors[sdr.id]}
				<div class="text-xs text-red-400">Failed: {errors[sdr.id]}</div>
			{/if}
		</div>
	{:else}
		<div class="text-xs text-slate-500">No SDRs connected</div>
	{/each}

	<div class="text-xs text-slate-600">
		Manual tune parks that device's sweep — Start sweep resumes it from
		the currently tuned frequency (§7.4). Devices with
		scan_autostart: false boot parked; a park resets on capture restart.
	</div>
</div>