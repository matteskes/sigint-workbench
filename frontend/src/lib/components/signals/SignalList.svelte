<script lang="ts">
	import { signals, selectedSignal, type Signal } from '$lib/stores/signals';

	function selectSignal(s: Signal) {
		selectedSignal.set(s);
	}

	function freqMHz(hz: number): string {
		if (hz >= 1e9) return `${(hz / 1e9).toFixed(2)} GHz`;
		if (hz >= 1e6) return `${(hz / 1e6).toFixed(2)} MHz`;
		return `${(hz / 1e3).toFixed(1)} kHz`;
	}

	// §14.3: the store holds every active signal — thousands when a
	// receiver sits on a busy band — and the keyed each below is the
	// DOM bottleneck under saturation. Render a bounded window: the
	// count stays exact, and the tail self-heals from the update churn.
	const MAX_VISIBLE = 250;
	$: visibleSignals = $signals.slice(0, MAX_VISIBLE);
	$: hiddenCount = Math.max(0, $signals.length - visibleSignals.length);

	const classColors: Record<string, string> = {
		aviation: 'text-blue-400',
		land_mobile: 'text-green-400',
		marine: 'text-cyan-400',
		broadcast: 'text-amber-400',
		amateur: 'text-purple-400',
		gnss: 'text-red-400',
		wifi: 'text-indigo-400',
		unknown: 'text-slate-400'
	};
</script>

<div class="flex flex-col h-full">
	<div class="px-3 py-2 border-b border-slate-700 flex items-center justify-between">
		<h2 class="text-sm font-semibold">Signals</h2>
		<span class="text-xs text-slate-500">{$signals.length} active</span>
	</div>

	<div class="flex-1 overflow-y-auto">
		{#each visibleSignals as signal (signal.id)}
			<button
				class="w-full px-3 py-2 text-left hover:bg-slate-800 transition-colors
					{$selectedSignal?.id === signal.id ? 'bg-slate-800 border-l-2 border-blue-500' : 'border-l-2 border-transparent'}"
				onclick={() => selectSignal(signal)}
			>
				<div class="flex items-center justify-between">
					<span class="text-sm font-mono">{freqMHz(signal.freqHz)}</span>
					{#if signal.verified}
						<span class="text-xs text-green-400">✓ verified</span>
					{/if}
				</div>
				<div class="flex items-center gap-2 mt-0.5">
					<span class="text-xs {classColors[signal.class] ?? 'text-slate-400'}">
						{signal.class}
					</span>
					<span class="text-xs text-slate-500">{signal.modulation}</span>
					<span class="text-xs text-slate-500 ml-auto">
						{signal.powerDbm?.toFixed(1)} {signal.powerCalibrated ? 'dBm' : 'dB (rel.)'}
					</span>
				</div>
			</button>
		{:else}
			<div class="p-4 text-center text-slate-500 text-sm">
				No signals detected
			</div>
		{/each}
		{#if hiddenCount > 0}
			<div class="px-3 py-2 text-center text-xs text-slate-600">
				+{hiddenCount} more not rendered
			</div>
		{/if}
	</div>
</div>