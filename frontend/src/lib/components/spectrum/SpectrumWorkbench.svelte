<script lang="ts">
	// §7 Spectrum workbench: §18 promoted from a 160 px sidebar widget
	// to a first-class view, with the RF controls that shape it in one
	// left rail. Sources appear only when spectrum.frame events arrive
	// — never from the SDR list alone (the rule that saved us yesterday
	// is stated in the empty-state copy). No per-event work here: the
	// staleness dots tick at 1 Hz, canvases redraw on the existing
	// dirty-flag loop.
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import SpectrumView from './SpectrumView.svelte';
	import SDRControl from '../control/SDRControl.svelte';
	import {
		spectrum,
		spectrumSdrIds,
		selectedSdrId as pickStore,
		markedFreqHz
	} from '$lib/stores/spectrum';
	import { spectrumSelection } from '$lib/stores/tfr';
	import { sdrRuntime } from '$lib/stores/sdrs';
	import { freqHz } from '$lib/ui/format';

	let now = $state(Date.now());
	onMount(() => {
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	type SourceRow = { id: string; freshness: 'live' | 'stale' | 'none'; ageS: number | null };

	const sources = $derived.by<SourceRow[]>(() => {
		const sp = $spectrum;
		return $spectrumSdrIds.map((id) => {
			const latest = sp[id]?.latest;
			if (!latest) return { id, freshness: 'none' as const, ageS: null };
			const ageS = Math.max(0, Math.round((now - Date.parse(latest.t)) / 1000));
			return { id, freshness: ageS < 3 ? ('live' as const) : ('stale' as const), ageS };
		});
	});

	const sweeping = (id: string): boolean => {
		const rt = $sdrRuntime[id];
		return Boolean(rt?.scanning && !rt?.scanPaused);
	};

	function clearSelection(): void {
		spectrumSelection.set(null);
		markedFreqHz.set(null);
	}

	async function analyzeSelection(): Promise<void> {
		// F3: the selection store survives navigation — /analysis seeds
		// its form from it (§10).
		await goto('/analysis');
	}

	const sel = $derived($spectrumSelection);
</script>

<div class="flex min-w-0 flex-1">
	<!-- Left rail: RF controls + sources -->
	<aside class="w-64 shrink-0 overflow-y-auto border-r border-slate-700 bg-slate-900">
		<SDRControl />
		<div class="border-t border-slate-700 p-3">
			<div class="mb-2 text-xs font-semibold text-slate-400">Sources</div>
			<div class="space-y-1">
				{#each sources as s (s.id)}
					<button
						class="flex w-full items-center justify-between rounded px-2 py-1 text-xs {$pickStore === s.id
							? 'bg-slate-800 text-slate-200'
							: 'text-slate-400 hover:bg-slate-800/60'}"
						onclick={() => pickStore.set(s.id)}
					>
						<span class="flex items-center gap-1.5">
							{#if s.freshness === 'live'}
								<span class="h-1.5 w-1.5 rounded-full bg-green-400" title="frames < 3 s old"></span>
							{:else if s.freshness === 'stale'}
								<span class="h-1.5 w-1.5 rounded-full bg-amber-400" title="frames stopped arriving"></span>
							{:else}
								<span class="h-1.5 w-1.5 rounded-full bg-slate-600" title="no frames"></span>
							{/if}
							{s.id}
							{#if sweeping(s.id)}<span class="text-[10px] text-sky-400" title="sweep loop attached">▨</span>{/if}
						</span>
						<span class="text-[10px] text-slate-500">
							{s.freshness === 'none' ? 'no frames' : `${s.ageS}s`}
						</span>
					</button>
				{:else}
					<div class="text-[10px] text-slate-600">
						sources appear only when spectrum.frame events arrive — never from the receiver list alone
					</div>
				{/each}
			</div>
		</div>
	</aside>

	<!-- Canvas block -->
	<section class="flex min-w-0 flex-1 flex-col items-center overflow-y-auto p-4">
		<div class="w-full max-w-5xl">
			{#if sources.length === 0}
				<div class="mt-16 rounded border border-slate-700 bg-slate-900/60 p-6 text-center">
					<div class="text-sm text-slate-300">no frames yet</div>
					<div class="mx-auto mt-2 max-w-md text-xs text-slate-500">
						Sources appear here only when <span class="font-mono">spectrum.frame</span> events
						arrive via the gateway. If receivers are listed in the rail but nothing appears,
						they may be parked, or sdr-capture is down — check the health chips above.
					</div>
				</div>
			{/if}

			<div class="rounded border border-slate-700 bg-slate-900/60 p-3">
				<SpectrumView />
			</div>

			<!-- Selection bar (§18.3 → §19.4 gesture) -->
			<div class="mt-3 flex flex-wrap items-center gap-3 rounded border border-slate-700 bg-slate-900/60 px-3 py-2 text-xs">
				{#if sel}
					<span class="font-mono text-sky-300">
						{freqHz(Math.min(sel.freqLoHz, sel.freqHiHz))} – {freqHz(Math.max(sel.freqLoHz, sel.freqHiHz))}
					</span>
					<span class="text-slate-400">
						{(Math.abs(sel.endMs - sel.startMs) / 1000).toFixed(1)} s selected
					</span>
					<button
						class="rounded bg-sky-700 px-2 py-1 text-[11px] font-medium text-white hover:bg-sky-600"
						onclick={analyzeSelection}
					>analyze ↗</button>
					<button
						class="rounded bg-slate-800 px-2 py-1 text-[11px] text-slate-300 hover:bg-slate-700"
						onclick={clearSelection}
					>clear</button>
				{:else if $markedFreqHz !== null}
					<span class="text-amber-400">marked: {freqHz($markedFreqHz)}</span>
					<button
						class="rounded bg-slate-800 px-2 py-1 text-[11px] text-slate-300 hover:bg-slate-700"
						onclick={clearSelection}
					>clear mark</button>
				{:else}
					<span class="text-slate-500">drag across the waterfall to pick a span for time-frequency analysis</span>
				{/if}
			</div>
		</div>
	</section>
</div>
