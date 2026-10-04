<script lang="ts">
	import { onMount } from 'svelte';
	import MapView from '$lib/components/map/MapView.svelte';
	import SignalList from '$lib/components/signals/SignalList.svelte';
	import SignalDetail from '$lib/components/signals/SignalDetail.svelte';
	import SpectrumAnalyzer from '$lib/components/spectrum/SpectrumAnalyzer.svelte';
	import SDRControl from '$lib/components/control/SDRControl.svelte';
	import { selectedSignal, signals, upsertSignal, removeSignal, type Signal } from '$lib/stores/signals';
	import { sdrs, applySDRStatus, type SDRStatus } from '$lib/stores/sdrs';
	import { fetchSignals, fetchSDRs, connectWebSocket, type WSEvent } from '$lib/api/client';

	// Full (re)sync from the REST API — on boot and after every
	// (re)connect, so events missed while disconnected are healed.
	async function bootstrap(): Promise<void> {
		try {
			const [sigs, devices] = await Promise.all([fetchSignals(), fetchSDRs()]);
			signals.set(sigs);
			sdrs.set(devices);
		} catch (err) {
			console.error('dashboard bootstrap failed', err);
		}
	}

	function handleEvent(ev: WSEvent): void {
		switch (ev.type) {
			case 'signal.new':
			case 'signal.update':
				upsertSignal(ev.payload as Signal);
				break;
			case 'signal.removed':
				if (ev.payload?.id) removeSignal(ev.payload.id);
				break;
			case 'sdr.status':
				applySDRStatus(ev.payload as SDRStatus);
				break;
		}
	}

	onMount(() => {
		bootstrap();

		let ws: WebSocket | undefined;
		let retries = 0;
		let disposed = false;

		const connect = () => {
			ws = connectWebSocket(handleEvent, () => {
				retries = 0;
				bootstrap();
			});
			ws.onclose = () => {
				if (disposed) return;
				const delay = Math.min(1000 * 2 ** retries++, 15_000);
				setTimeout(connect, delay);
			};
		};
		connect();

		return () => {
			disposed = true;
			ws?.close();
		};
	});
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