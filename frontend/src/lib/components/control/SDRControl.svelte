<script lang="ts">
	import { sdrs } from '$lib/stores/sdrs';

	function tune(id: string, freqMHz: number) {
		// TODO: Send tuning command to API
		console.log(`Tune ${id} to ${freqMHz} MHz`);
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
					text-slate-100 focus:outline-none focus:border-blue-500"
				onblur={(e) => tune(sdr.id, parseFloat(e.currentTarget.value))}
			/>
			<label class="text-[10px] text-slate-500">Frequency (MHz)</label>
		</div>
	{:else}
		<div class="text-xs text-slate-500">No SDRs connected</div>
	{/each}
</div>