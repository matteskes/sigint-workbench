<script lang="ts">
	import MapView from '$lib/components/map/MapView.svelte';
	import SignalList from '$lib/components/signals/SignalList.svelte';
	import SignalDetail from '$lib/components/signals/SignalDetail.svelte';
	import SpectrumAnalyzer from '$lib/components/spectrum/SpectrumAnalyzer.svelte';
	import SDRControl from '$lib/components/control/SDRControl.svelte';
	import { selectedSignal } from '$lib/stores/signals';
</script>

<div class="flex h-full">
	<!-- Left sidebar: SDR controls -->
	<aside class="w-64 border-r border-slate-700 overflow-y-auto shrink-0">
		<SDRControl />
		<div class="border-t border-slate-700">
			<SpectrumAnalyzer />
		</div>
	</aside>

	<!-- Center: Map -->
	<section class="flex-1 relative">
		<MapView />

		<!-- Signal list overlay (bottom) -->
		<div class="absolute bottom-0 left-0 right-0 h-64 bg-slate-900/95 border-t border-slate-700">
			<SignalList />
		</div>
	</section>

	<!-- Right sidebar: Signal detail -->
	{#if $selectedSignal}
		<aside class="w-80 border-l border-slate-700 overflow-y-auto shrink-0">
			<SignalDetail signal={$selectedSignal} />
		</aside>
	{/if}
</div>