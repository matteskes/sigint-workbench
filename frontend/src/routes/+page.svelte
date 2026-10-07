<script lang="ts">
	// §5 Operations view: geographic situational awareness. The map is
	// the stage; the signal table and the inspector surround it. The
	// WS connection, ingest, and first-run logic live in the shell and
	// the connection store — this route is a pure composition.
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import MapView from '$lib/components/map/MapView.svelte';
	import SignalTable from '$lib/components/signals/SignalTable.svelte';
	import Inspector from '$lib/components/inspector/Inspector.svelte';
	import SDRControl from '$lib/components/control/SDRControl.svelte';
	import { selectedSignal } from '$lib/stores/signals';
	import { layers, mapReceiverId, inspectorOpen } from '$lib/stores/ui';
	import { popoverDismiss } from '$lib/ui/actions';
	import { fetchSignal } from '$lib/api/client';

	// ?signal=<id> deep link (§3.1): hydrate the selection on landing.
	// Soft-fail — a stale id just leaves the selection empty.
	onMount(() => {
		const id = page.url.searchParams.get('signal');
		if (!id) return;
		fetchSignal(id)
			.then((s) => selectedSignal.update((cur) => cur ?? s))
			.catch(() => {
				// unknown id — no fake selection
			});
	});

	let layersOpen = $state(false);
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<div class="flex min-h-0 flex-1">
		<!-- Center: Map (full stage) -->
		<section class="relative min-w-0 flex-1">
			<MapView />

			<!-- Layer toggles (bottom-left popover, §5); §15 dismissal —
			     the wrapper spans trigger + panel -->
			<div
				class="absolute bottom-3 left-3 z-10"
				use:popoverDismiss={{ open: layersOpen, onDismiss: () => (layersOpen = false) }}
			>
				<button
					class="rounded border border-slate-700 bg-slate-900/90 px-2 py-1 text-xs text-slate-300 hover:bg-slate-800"
					aria-expanded={layersOpen}
					onclick={() => (layersOpen = !layersOpen)}
				>layers ▾</button>
				{#if layersOpen}
					<div class="mt-1 w-40 space-y-1 rounded border border-slate-700 bg-slate-900/95 p-2 text-xs shadow-xl">
						<label class="flex items-center gap-2 text-slate-300">
							<input type="checkbox" bind:checked={$layers.signals} /> signals
						</label>
						<label class="flex items-center gap-2 text-slate-300">
							<input type="checkbox" bind:checked={$layers.receivers} /> receivers
						</label>
						<label class="flex items-center gap-2 text-slate-300">
							<input type="checkbox" bind:checked={$layers.tracks} /> tracks
						</label>
						<label class="flex items-center gap-2 text-slate-300">
							<input type="checkbox" bind:checked={$layers.labels} /> labels
						</label>
					</div>
				{/if}
			</div>

			<!-- Receiver card popover (marker click, §5): freq, gain, sweep -->
			{#if $mapReceiverId}
				<div class="absolute right-3 top-3 z-10 w-64 rounded border border-slate-700 bg-slate-900/95 shadow-xl">
					<div class="flex items-center justify-between border-b border-slate-700 px-2 py-1">
						<span class="text-xs font-semibold uppercase tracking-wide text-slate-400">Receiver</span>
						<button
							class="text-xs text-slate-500 hover:text-slate-200"
							aria-label="Close receiver card"
							onclick={() => mapReceiverId.set(null)}
						>✕</button>
					</div>
					<SDRControl filterId={$mapReceiverId} />
				</div>
			{/if}
		</section>

<!-- INSPECTOR -->
		{#if $selectedSignal && $inspectorOpen}
			<aside class="w-80 shrink-0 overflow-y-auto border-l border-slate-700">
				<Inspector signal={$selectedSignal} />
			</aside>
		{/if}
	</div>

	<!-- Signal table (bottom, ~15 rem) -->
	<div class="h-60 shrink-0 border-t border-slate-700">
		<SignalTable />
	</div>
</div>
