<script lang="ts">
	import { sdrs, sdrRuntime, applySDRStatus, applySdrRuntime } from '$lib/stores/sdrs';
	import {
		retuneSdr,
		setGain,
		setScan,
		fetchSDRStatus,
		type SDRDeviceStatus
	} from '$lib/api/client';
	import { describeStatus } from '$lib/ui/describeStatus';

	// §7.4 control surface: a manual tune/gain PUTs through the gateway,
	// which persists the row AND forwards to sdr-capture (§13.1:
	// capture unreachable ⇒ 502, unknown device there ⇒ 404). The WS
	// `sdr.status` feed stays the source of truth (§14.4.3); the
	// local store update is only immediate feedback.
	//
	// `filterId` renders a single device card — the Operations map
	// receiver popover (§5) reuses this component for exactly that.
	let { filterId = null }: { filterId?: string | null } = $props();

	let busy = $state<Record<string, boolean>>({});
	let errors = $state<Record<string, string>>({});

	// Live scan state per device (§7.4): `scanning` = a D3 sweep loop
	// is attached; `scanPaused` = parked (boot park via
	// scan_autostart: false, or a manual tune). Cached in the shared
	// sdrRuntime store so the map receiver halos and the spectrum view
	// read the same fetch as this rail (§5).
	let requested = $state<Record<string, boolean>>({});

	async function refreshStatus(id: string): Promise<void> {
		try {
			applySdrRuntime(await fetchSDRStatus(id));
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
			errors = { ...errors, [sdr.id]: describeStatus(e) };
		} finally {
			busy = { ...busy, [sdr.id]: false };
		}
	}

	async function changeGain(
		sdr: { id: string; gainDb: number },
		gainDb: number
	): Promise<void> {
		errors = { ...errors, [sdr.id]: '' };
		// §13.1: the PUT is partial; a no-op gain re-send is pointless.
		if (!Number.isFinite(gainDb) || gainDb === sdr.gainDb) return;
		busy = { ...busy, [sdr.id]: true };
		try {
			applySDRStatus(await setGain(sdr.id, gainDb));
		} catch (e) {
			errors = { ...errors, [sdr.id]: describeStatus(e) };
		} finally {
			busy = { ...busy, [sdr.id]: false };
		}
	}

	async function toggleScan(id: string): Promise<void> {
		const cur = $sdrRuntime[id];
		if (!cur) return;
		errors = { ...errors, [id]: '' };
		busy = { ...busy, [id]: true };
		try {
			applySdrRuntime(await setScan(id, cur.scanPaused));
			// Immediate feedback in the same shape as sdr.status (§14.4.3);
			// the device row keeps its registered position.
			const sdr = $sdrs.find((d) => d.id === id);
			if (sdr) {
				applySDRStatus({ ...sdr, freqHz: cur.freqHz, gainDb: cur.gainDb, active: cur.active, bwHz: cur.bwHz });
			}
		} catch (e) {
			errors = { ...errors, [id]: describeStatus(e) };
		} finally {
			busy = { ...busy, [id]: false };
		}
	}
</script>

<div class="p-3 space-y-3">
	{#if !filterId}
		<div class="text-xs font-semibold text-slate-400">SDR Controls</div>
	{/if}

	{#each $sdrs as sdr (sdr.id)}
		{#if !filterId || sdr.id === filterId}
		<div class="space-y-2 rounded bg-slate-800 p-2">
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
				{(sdr.freqHz / 1e6).toFixed(3)} MHz | {sdr.gainDb} dB
			</div>
			<div class="flex gap-1">
				<label class="min-w-0 flex-1">
					<span class="sr-only">Frequency (MHz) for {sdr.id}</span>
					<input
						type="number"
						step="0.001"
						min="0.024"
						max="6000"
						value={(sdr.freqHz / 1e6).toFixed(3)}
						class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 text-xs
							text-slate-100 focus:border-sky-500 focus:outline-none
							disabled:opacity-50"
						disabled={busy[sdr.id]}
						onblur={(e) => tune(sdr, parseFloat(e.currentTarget.value))}
						aria-label="Frequency (MHz) for {sdr.id}"
					/>
				</label>
				<label class="w-20 shrink-0">
					<span class="sr-only">Gain (dB) for {sdr.id}</span>
					<input
						type="number"
						step="0.1"
						min="0"
						max="49.6"
						value={sdr.gainDb}
						class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 text-xs
							text-slate-100 focus:border-sky-500 focus:outline-none
							disabled:opacity-50"
						disabled={busy[sdr.id]}
						onblur={(e) => changeGain(sdr, parseFloat(e.currentTarget.value))}
						aria-label="Gain (dB) for {sdr.id}"
					/>
				</label>
			</div>
			{#if $sdrRuntime[sdr.id]?.scanning}
				<div class="flex items-center justify-between">
					<button
						class="rounded px-2 py-0.5 text-xs disabled:opacity-50
							{$sdrRuntime[sdr.id].scanPaused
							? 'bg-green-900/60 text-green-200 hover:bg-green-800'
							: 'bg-amber-900/60 text-amber-200 hover:bg-amber-800'}"
						disabled={busy[sdr.id]}
						onclick={() => toggleScan(sdr.id)}
					>
						{$sdrRuntime[sdr.id].scanPaused ? 'Start sweep' : 'Stop sweep'}
					</button>
					<span
						class="text-xs {$sdrRuntime[sdr.id].scanPaused
							? 'text-amber-400'
							: 'text-green-400'}"
					>
						{$sdrRuntime[sdr.id].scanPaused ? '■ parked' : '▶ sweeping'}
					</span>
				</div>
			{/if}
			{#if busy[sdr.id]}
				<div class="text-xs text-amber-400">applying…</div>
			{:else if errors[sdr.id]}
				<div class="text-xs text-red-400">Failed: {errors[sdr.id]}</div>
			{/if}
		</div>
		{/if}
	{:else}
		<div class="text-xs text-slate-500">
			no receivers — add one in <a href="/setup" class="text-sky-400 hover:underline">setup</a>
		</div>
	{/each}

	{#if !filterId}
		<div class="text-xs text-slate-600">
			Manual tune parks that device's sweep — Start sweep resumes it from
			the currently tuned frequency (§7.4). Gain changes do not park the
			sweep. Devices with scan_autostart: false boot parked; a park resets
			on capture restart.
		</div>
	{/if}
</div>