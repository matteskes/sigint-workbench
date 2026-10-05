<script lang="ts">
	import { onMount } from 'svelte';
	import MapView from '$lib/components/map/MapView.svelte';
	import SignalList from '$lib/components/signals/SignalList.svelte';
	import SignalDetail from '$lib/components/signals/SignalDetail.svelte';
	import SpectrumView from '$lib/components/spectrum/SpectrumView.svelte';
	import SDRControl from '$lib/components/control/SDRControl.svelte';
	import { selectedSignal, signals, upsertSignal, removeSignal, type Signal } from '$lib/stores/signals';
	import { sdrs, applySDRStatus, type SDRStatus } from '$lib/stores/sdrs';
	import { applyAudioLevel, pruneSignalLevels, clearSignalLevel } from '$lib/stores/audio';
	import { applyTrackUpdate, clearTrack } from '$lib/stores/tracks';
	import { applySpectrumFrame } from '$lib/stores/spectrum';
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
				if (ev.payload?.id) {
					removeSignal(ev.payload.id);
					clearSignalLevel(ev.payload.id);
					clearTrack(ev.payload.id);
				}
				break;
			case 'sdr.status':
				applySDRStatus(ev.payload as SDRStatus);
				break;
			case 'audio.level':
				// §10.6: coarse level per actively demodulated signal.
				if (ev.payload?.signalId) applyAudioLevel(ev.payload.signalId, ev.payload.level ?? 0);
				break;
			case 'track.update':
				// §9.4: movement summary per located signal.
				if (ev.payload?.signalId) applyTrackUpdate(ev.payload);
				break;
			case 'spectrum.frame':
				// §18: decimated per-SDR spectrum/waterfall feed; malformed
				// frames are no-ops in the store (§14.3).
				applySpectrumFrame(ev.payload);
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

		// audio.level entries expire when their feed stops (§10.6).
		const levelPrune = setInterval(() => pruneSignalLevels(), 500);

		return () => {
			disposed = true;
			clearInterval(levelPrune);
			ws?.close();
		};
	});
</script>

<div class="flex h-full">
	<!-- Left sidebar: SDR controls -->
	<aside class="w-64 border-r border-slate-700 overflow-y-auto shrink-0">
		<SDRControl />
		<div class="border-t border-slate-700">
			<SpectrumView />
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