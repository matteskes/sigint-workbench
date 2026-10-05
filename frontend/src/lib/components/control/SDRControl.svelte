<script lang="ts">
	import { sdrs, applySDRStatus } from '$lib/stores/sdrs';
	import { retuneSdr } from '$lib/api/client';

	// §7.4 control surface: a manual tune PUTs through the gateway,
	// which persists the row AND forwards to sdr-capture (§13.1:
	// capture unreachable ⇒ 502, unknown device there ⇒ 404). The WS
	// `sdr.status` feed stays the source of truth (§14.4.3); the
	// local store update is only immediate feedback.
	let busy = $state<Record<string, boolean>>({});
	let errors = $state<Record<string, string>>({});

	function describe(e: unknown): string {
		const m = /(\d{3})\s*$/.exec(String(e));
		if (!m) return String(e);
		const status = Number(m[1]);
		if (status === 502) return 'capture unreachable';
		if (status === 404) return 'unknown device at capture';
		if (status === 503) return 'database unavailable';
		return `error ${status}`;
	}

	async function tune(sdr: { id: string; freqHz: number }, freqMHz: number): Promise<void> {
		errors = { ...errors, [sdr.id]: '' };
		const freqHz = Math.round(freqMHz * 1e6);
		// Invalid or unchanged input: no request (the PUT is partial,
		// so re-sending the current frequency would be a no-op retune).
		if (!Number.isFinite(freqHz) || freqHz === sdr.freqHz) return;
		busy = { ...busy, [sdr.id]: true };
		try {
			applySDRStatus(await retuneSdr(sdr.id, freqHz));
		} catch (e) {
			errors = { ...errors, [sdr.id]: describe(e) };
		} finally {
			busy = { ...busy, [sdr.id]: false };
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
			{#if busy[sdr.id]}
				<div class="text-xs text-amber-400">tuning…</div>
			{:else if errors[sdr.id]}
				<div class="text-xs text-red-400">Retune failed: {errors[sdr.id]}</div>
			{/if}
		</div>
	{:else}
		<div class="text-xs text-slate-500">No SDRs connected</div>
	{/each}

	<div class="text-xs text-slate-600">
		Manual tune pauses that device's scan loop until restart (§7.4).
	</div>
</div>