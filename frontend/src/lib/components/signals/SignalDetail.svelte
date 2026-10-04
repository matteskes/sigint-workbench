<script lang="ts">
	import type { Signal } from '$lib/stores/signals';
	import AudioPlayer from '../audio/AudioPlayer.svelte';

	let { signal }: { signal: Signal } = $props();

	function freqMHz(hz: number): string {
		if (hz >= 1e9) return `${(hz / 1e9).toFixed(3)} GHz`;
		if (hz >= 1e6) return `${(hz / 1e6).toFixed(3)} MHz`;
		return `${(hz / 1e3).toFixed(1)} kHz`;
	}

	function bandwidthStr(bw: number): string {
		if (bw >= 1e6) return `${(bw / 1e6).toFixed(1)} MHz`;
		if (bw >= 1e3) return `${(bw / 1e3).toFixed(1)} kHz`;
		return `${bw} Hz`;
	}
</script>

<div class="p-3 space-y-4">
	<h2 class="text-sm font-semibold text-slate-300">Signal Detail</h2>

	<!-- Frequency -->
	<div>
		<div class="text-xs text-slate-500">Frequency</div>
		<div class="text-lg font-mono text-white">{freqMHz(signal.freqHz)}</div>
	</div>

	<!-- Classification -->
	<div class="grid grid-cols-2 gap-2">
		<div>
			<div class="text-xs text-slate-500">Modulation</div>
			<div class="text-sm">{signal.modulation} {signal.subType && `(${signal.subType})`}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Class</div>
			<div class="text-sm capitalize">{signal.class}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Bandwidth</div>
			<div class="text-sm font-mono">{bandwidthStr(signal.bandwidthHz)}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Confidence</div>
			<div class="text-sm font-mono">{(signal.confidence * 100).toFixed(0)}%</div>
		</div>
	</div>

	<!-- Power (§5.6: "dBm" only for calibrated SDRs) -->
	<div>
		<div class="text-xs text-slate-500">Power</div>
		<div class="text-sm font-mono">
			{signal.powerDbm?.toFixed(1)} {signal.powerCalibrated ? 'dBm' : 'dB (rel.)'}
		</div>
	</div>

	<!-- Location -->
	<div>
		<div class="text-xs text-slate-500">Location</div>
		{#if signal.lat != null && signal.lon != null}
			<div class="text-sm font-mono">
				{signal.lat.toFixed(4)}, {signal.lon.toFixed(4)}
			</div>
			<div class="text-xs text-slate-500">±{signal.accuracyM.toFixed(0)} m</div>
		{:else}
			<div class="text-sm text-slate-500">Unlocated</div>
		{/if}
	</div>

	<!-- Timing -->
	<div class="grid grid-cols-2 gap-2">
		<div>
			<div class="text-xs text-slate-500">First Seen</div>
			<div class="text-xs font-mono">
				{new Date(signal.firstSeen).toLocaleTimeString()}
			</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Last Seen</div>
			<div class="text-xs font-mono">
				{new Date(signal.lastSeen).toLocaleTimeString()}
			</div>
		</div>
	</div>

	<!-- SDR -->
	<div>
		<div class="text-xs text-slate-500">Source SDR</div>
		<div class="text-sm">{signal.sdrId}</div>
	</div>

	<!-- Status -->
	<div class="flex items-center gap-2">
		{#if signal.verified}
			<span class="text-xs px-2 py-0.5 bg-green-900/50 text-green-400 rounded">
				✓ Verified (2 SDRs)
			</span>
		{:else}
			<span class="text-xs px-2 py-0.5 bg-amber-900/50 text-amber-400 rounded">
				Unverified
			</span>
		{/if}
	</div>

	<!-- Audio -->
	<div class="border-t border-slate-700 pt-3">
		<AudioPlayer signal={signal} />
	</div>
</div>