<script lang="ts">
	// §5/§8: the triage table. Absorbs the old SignalList (windowing,
	// exact-count footer) and the orphaned SearchPanel (search + class
	// chips, §1) into one component used by Operations (compact column
	// set) and Signals (full columns). Sort/filter are store-slice ops;
	// the 250-row render window is load-bearing under saturation
	// (§14.3) — the count stays exact via the footer.
	import { signals, selectedSignal } from '$lib/stores/signals';
	import { connectionState } from '$lib/stores/connection';
	import {
		orderedSignalIds,
		highlightIndex,
		unlocatedOnly,
		tableDensity,
		inspectorOpen
	} from '$lib/stores/ui';
	import { SIGNAL_CLASSES } from '$lib/ui/classColor';
	import ClassChip from '$lib/components/ui/ClassChip.svelte';
	import { freqHz, powerDb, relTime } from '$lib/ui/format';

	let { full = false }: { full?: boolean } = $props();

	type Row = (typeof $signals)[number];

	// ─── Filters (SearchPanel, finally wired) ────────────────────────────
	let searchQuery = $state('');
	let filterClass = $state('all');

	// ─── Sort: default matches the REST order (lastSeen DESC, §5) ───────
	type SortMode = 'default' | 'freqAsc' | 'freqDesc' | 'ageAsc' | 'ageDesc';
	let sortMode = $state<SortMode>('default');

	function cycleSort(which: 'freq' | 'age'): void {
		sortMode =
			sortMode === `${which}Asc`
				? 'default'
				: sortMode === `${which}Desc`
					? `${which}Asc`
					: `${which}Desc`;
	}

	const filtered = $derived.by(() => {
		const q = searchQuery.trim().toLowerCase();
		const list = $signals.filter((s) => {
			if ($unlocatedOnly && s.lat != null) return false;
			if (filterClass !== 'all' && s.class !== filterClass) return false;
			if (!q) return true;
			return (
				s.class.toLowerCase().includes(q) ||
				s.modulation.toLowerCase().includes(q) ||
				s.id.toLowerCase().includes(q) ||
				freqHz(s.freqHz).toLowerCase().includes(q) ||
				(s.freqHz / 1e6).toFixed(3).includes(q)
			);
		});
		switch (sortMode) {
			case 'freqAsc':
				return [...list].sort((a, b) => a.freqHz - b.freqHz);
			case 'freqDesc':
				return [...list].sort((a, b) => b.freqHz - a.freqHz);
			case 'ageAsc':
				return [...list].sort((a, b) => Date.parse(a.lastSeen) - Date.parse(b.lastSeen));
			case 'ageDesc':
				return [...list].sort((a, b) => Date.parse(b.lastSeen) - Date.parse(a.lastSeen));
			default:
				return list; // store order: lastSeen DESC from REST + newest-first ingest
		}
	});

	// §14.3: bounded render window; exact count stays in the header.
	const MAX_VISIBLE = 250;
	const visible = $derived(filtered.slice(0, MAX_VISIBLE));
	const hiddenCount = $derived(Math.max(0, filtered.length - visible.length));

	// The shell's j/k/Enter navigation walks the rows the operator sees.
	$effect(() => {
		orderedSignalIds.set(visible.map((s) => s.id));
	});

	const unlocatedCount = $derived($signals.filter((s) => s.lat == null).length);
	const nearCap = $derived($signals.length >= 450);

	function select(s: Row, index: number): void {
		selectedSignal.set(s);
		highlightIndex.set(index);
		inspectorOpen.set(true);
	}

	function power(s: Row): string {
		const p = powerDb(s.powerDbm, s.powerCalibrated);
		return `${p.value} ${p.unit}`;
	}

	// §8: client-side CSV over the filtered, sorted slice — no endpoint.
	let copied = $state(false);
	async function copyCsv(): Promise<void> {
		const header = [
			'id', 'freqHz', 'class', 'modulation', 'subType', 'bandwidthHz',
			'power', 'unitCalibrated', 'confidence', 'verified', 'lat', 'lon',
			'sdrId', 'firstSeen', 'lastSeen'
		];
		const lines = filtered.map((s) =>
			[
				s.id, s.freqHz, s.class, s.modulation, s.subType, s.bandwidthHz,
				s.powerDbm, s.powerCalibrated, s.confidence, s.verified,
				s.lat ?? '', s.lon ?? '', s.sdrId, s.firstSeen, s.lastSeen
			].join(',')
		);
		try {
			await navigator.clipboard.writeText([header.join(','), ...lines].join('\n'));
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			// clipboard unavailable — the button stays; nothing breaks
		}
	}

	const rowPad = $derived($tableDensity === 'compact' ? 'py-1' : 'py-2');
</script>

<div class="flex h-full flex-col bg-slate-900">
	<!-- Toolbar: search + class chips + unlocated chip + CSV + density -->
	<div class="flex flex-wrap items-center gap-2 border-b border-slate-700 px-3 py-2">
		<h2 class="text-sm font-semibold">Signals</h2>
		<span class="text-xs text-slate-500">{$signals.length} active</span>
		<input
			data-signal-search
			bind:value={searchQuery}
			placeholder="Search: freq, class, mod, id…  ( / )"
			aria-label="Search signals"
			class="ml-2 w-56 rounded border border-slate-700 bg-slate-800 px-2 py-1 text-xs text-slate-100 placeholder-slate-500 focus:border-sky-500 focus:outline-none"
		/>
		<div class="flex flex-wrap gap-1" role="group" aria-label="Filter by class">
			<button
				class="rounded-full px-2 py-0.5 text-[10px] {filterClass === 'all'
					? 'bg-sky-900 text-sky-300'
					: 'bg-slate-800 text-slate-400 hover:bg-slate-700'}"
				onclick={() => (filterClass = 'all')}
			>all</button>
			{#each SIGNAL_CLASSES as cls (cls)}
				<button
					class="rounded-full px-2 py-0.5 text-[10px] {filterClass === cls
						? 'bg-sky-900 text-sky-300'
						: 'bg-slate-800 text-slate-400 hover:bg-slate-700'}"
					onclick={() => (filterClass = filterClass === cls ? 'all' : cls)}
				>{cls.replace(/_/g, ' ')}</button>
			{/each}
		</div>
		<div class="ml-auto flex items-center gap-2">
			{#if unlocatedCount > 0}
				<button
					class="rounded-full px-2 py-0.5 text-[10px] {$unlocatedOnly
						? 'bg-amber-900/60 text-amber-300'
						: 'bg-slate-800 text-slate-400 hover:bg-slate-700'}"
					title="Unlocated signals are never plotted (A1, §9.3) — this chip is their honest representation"
					onclick={() => unlocatedOnly.update((v) => !v)}
				>○ {unlocatedCount} without position</button>
			{/if}
			<button
				class="rounded bg-slate-800 px-2 py-0.5 text-[10px] text-slate-400 hover:bg-slate-700 disabled:opacity-40"
				onclick={copyCsv}
				disabled={filtered.length === 0}
			>{copied ? 'Copied ✓' : 'Copy CSV'}</button>
			<button
				class="rounded bg-slate-800 px-2 py-0.5 text-[10px] text-slate-400 hover:bg-slate-700"
				title="Row density"
				onclick={() => tableDensity.update((d) => (d === 'compact' ? 'comfortable' : 'compact'))}
			>{$tableDensity === 'compact' ? 'compact rows' : 'comfortable rows'}</button>
		</div>
	</div>

	<!-- Column headers -->
	<div class="flex items-center gap-3 border-b border-slate-700 bg-slate-900 px-3 py-1 text-[10px] uppercase tracking-wide text-slate-500">
		<button class="w-28 shrink-0 text-left hover:text-slate-300" onclick={() => cycleSort('freq')}>
			Freq {sortMode === 'freqAsc' ? '▲' : sortMode === 'freqDesc' ? '▼' : ''}
		</button>
		<span class="w-24 shrink-0">Class</span>
		<span class="w-24 shrink-0">Mod</span>
		<span class="w-32 shrink-0 text-right">Power</span>
		<span class="w-16 shrink-0 text-center">Verif</span>
		<button class="w-16 shrink-0 text-left hover:text-slate-300" onclick={() => cycleSort('age')}>
			Age {sortMode === 'ageAsc' ? '▲' : sortMode === 'ageDesc' ? '▼' : ''}
		</button>
		<span class="w-24 shrink-0">SDR</span>
		{#if full}
			<span class="w-14 shrink-0 text-right">Conf</span>
			<span class="w-20 shrink-0 text-right">BW</span>
			<span class="w-24 shrink-0">First seen</span>
			<span class="min-w-0 flex-1 truncate">Signal id</span>
		{/if}
	</div>
	<!-- Rows -->
	<div class="min-h-0 flex-1 overflow-y-auto" role="table" aria-label="Detected signals">
		{#if visible.length === 0}
			<div class="p-4 text-center text-sm text-slate-500">
				{#if $signals.length === 0 && $connectionState !== 'live'}
					waiting for first events…
				{:else if $signals.length === 0}
					no signals detected
				{:else if searchQuery || filterClass !== 'all' || $unlocatedOnly}
					no signals match the current filter
				{:else}
					no signals in this window
				{/if}
			</div>
		{/if}
		{#each visible as signal, i (signal.id)}
			<button
				role="row"
				class="flex w-full items-center gap-3 border-l-2 px-3 text-left text-sm transition-colors {rowPad}
					{$selectedSignal?.id === signal.id
					? 'border-sky-500 bg-slate-800'
					: $highlightIndex === i
						? 'border-transparent bg-slate-800/60'
						: 'border-transparent hover:bg-slate-800/60'}"
				aria-selected={$selectedSignal?.id === signal.id}
				onclick={() => select(signal, i)}
			>
				<span class="w-28 shrink-0 font-mono">{freqHz(signal.freqHz)}</span>
				<span class="w-24 shrink-0"><ClassChip cls={signal.class} size="xs" /></span>
				<span class="w-24 shrink-0 truncate text-xs text-slate-400">{signal.modulation}</span>
				<span class="w-32 shrink-0 text-right font-mono text-xs {signal.powerCalibrated ? 'text-slate-300' : 'text-slate-400'}">
					{power(signal)}
				</span>
				<span class="w-16 shrink-0 text-center text-xs">
					{#if signal.verified}<span class="text-green-400" title="Two-receiver verification (§8)">✓</span>
					{:else}<span class="text-slate-600">—</span>{/if}
				</span>
				<span class="w-16 shrink-0 text-xs text-slate-500" title={new Date(signal.lastSeen).toLocaleString()}>
					{relTime(signal.lastSeen)}
				</span>
				<span class="w-24 shrink-0 truncate text-xs text-slate-500">{signal.sdrId}</span>
				{#if full}
					<span class="w-14 shrink-0 text-right font-mono text-xs text-slate-500">{signal.confidence.toFixed(2)}</span>
					<span class="w-20 shrink-0 text-right font-mono text-xs text-slate-500">{signal.bandwidthHz >= 1e3 ? `${(signal.bandwidthHz / 1e3).toFixed(1)} kHz` : `${signal.bandwidthHz} Hz`}</span>
					<span class="w-24 shrink-0 truncate text-xs text-slate-500">{new Date(signal.firstSeen).toLocaleTimeString()}</span>
					<span class="min-w-0 flex-1 truncate font-mono text-[10px] text-slate-600" title={signal.id}>
						{signal.id}
					</span>
				{/if}
			</button>
		{/each}
		{#if hiddenCount > 0}
			<div class="px-3 py-2 text-center text-xs text-slate-600">
				+{hiddenCount} more not rendered — narrow with search or class filter
			</div>
		{/if}
		{#if nearCap}
			<div class="px-3 py-1.5 text-center text-xs text-amber-500/80">
				near the 500-row API cap — narrow with search or map extent
			</div>
		{/if}
	</div>
</div>
