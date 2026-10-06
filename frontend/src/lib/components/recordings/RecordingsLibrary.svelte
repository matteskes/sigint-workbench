<script lang="ts">
	// §9: the cross-signal library the /api/recordings endpoint always
	// supported but no screen showed. No WS event exists for new
	// recordings, so this is the second permitted poller (30 s while
	// visible, §17) with an honest "refreshed Xs ago" caption and a
	// manual refresh. Playback is WAV-only by format honesty (§9): IQ
	// rows get download + analyze, never a fake play button. "Load
	// more" raises ?limit toward the API's 500 cap — no invented
	// infinite scroll. And no record button: recordings are triggered
	// in-band by the detector (§11.1, §20).
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		fetchRecordings,
		recordingAudioUrl,
		type Recording
	} from '$lib/api/client';
	import { signals } from '$lib/stores/signals';
	import ClassChip from '$lib/components/ui/ClassChip.svelte';
	import AudioPlayer from '$lib/components/audio/AudioPlayer.svelte';
	import { freqHz, rateSps, timeOfDay, dateFull } from '$lib/ui/format';

	const PAGE_STEPS = [50, 100, 200, 500];

	let recordings = $state<Recording[]>([]);
	let loading = $state(false);
	let error = $state('');
	let limit = $state(50);
	let formatFilter = $state<'all' | 'wav' | 'iq'>('all');
	let signalFilter = $state('');
	let refreshedAt = $state(0);
	let now = $state(Date.now());
	let playId = $state<string | null>(null);
	let copiedNote = $state('');

	// ?signal=<id> deep link seeds the filter (§9).
	$effect(() => {
		const q = page.url.searchParams.get('signal');
		if (q && q !== signalFilter) signalFilter = q;
	});

	async function load(): Promise<void> {
		loading = true;
		error = '';
		try {
			recordings = await fetchRecordings({
				signalId: signalFilter || undefined,
				limit
			});
			refreshedAt = Date.now();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// F7: poll at 30 s while the view is mounted (the route keeps this
	// component alive; teardown stops the timer).
	onMount(() => {
		void load();
		const t = setInterval(() => {
			now = Date.now();
			void load();
		}, 30_000);
		return () => clearInterval(t);
	});

	// Filter changes refetch with the new parameters.
	$effect(() => {
		// Track dependencies explicitly; load() is idempotent.
		void signalFilter;
		void limit;
		if (refreshedAt > 0 || signalFilter) void load();
	});

	const shown = $derived(
		formatFilter === 'all'
			? recordings
			: recordings.filter((r) => r.fileFormat.toLowerCase() === formatFilter)
	);

	const nextLimit = $derived(PAGE_STEPS.find((n) => n > limit) ?? null);

	function signalLabel(id: string): { label: string; known: boolean; cls: string } {
		const s = $signals.find((x) => x.id === id);
		if (!s) return { label: id.length > 18 ? `${id.slice(0, 17)}…` : id, known: false, cls: 'unknown' };
		return { label: freqHz(s.freqHz), known: true, cls: s.class };
	}

	function selectSignal(id: string): void {
		const s = $signals.find((x) => x.id === id);
		if (s) void goto(`/signals?signal=${encodeURIComponent(id)}`);
	}

	function analyze(rec: Recording): void {
		const q = new URLSearchParams({ recording: rec.id, signal: rec.signalId });
		void goto(`/analysis?${q}`);
	}
</script>

<div class="min-w-0 flex-1 overflow-y-auto p-4">
	<div class="mx-auto max-w-6xl">
		<div class="mb-3 flex flex-wrap items-center gap-3">
			<h2 class="text-sm font-semibold">Recordings</h2>
			<span class="text-xs text-slate-500">showing latest {shown.length}{limit < 500 ? ` of ${limit} window` : ''}</span>
			<label class="ml-auto flex items-center gap-1 text-xs text-slate-400">
				format
				<select
					class="rounded bg-slate-800 px-1.5 py-1 text-xs text-slate-200"
					bind:value={formatFilter}
				>
					<option value="all">all formats</option>
					<option value="wav">wav</option>
					<option value="iq">iq</option>
				</select>
			</label>
			<label class="flex items-center gap-1 text-xs text-slate-400">
				signal
				<select
					class="max-w-44 rounded bg-slate-800 px-1.5 py-1 text-xs text-slate-200"
					bind:value={signalFilter}
				>
					<option value="">all signals</option>
					{#each $signals.slice(0, 200) as s (s.id)}
						<option value={s.id}>{freqHz(s.freqHz)} · {s.class}</option>
					{/each}
				</select>
			</label>
			<button
				class="rounded bg-slate-800 px-2 py-1 text-xs text-slate-300 hover:bg-slate-700 disabled:opacity-40"
				disabled={loading}
				onclick={load}
			>{loading ? 'refreshing…' : 'refresh'}</button>
			<span class="text-[10px] text-slate-600" title="No WS event exists for new recordings — this list polls every 30 s (F7)">
				{#if refreshedAt > 0}
					refreshed {Math.max(0, Math.round((now - refreshedAt) / 1000))}s ago
				{:else}
					refreshing…
				{/if}
			</span>
		</div>
<!-- TABLE -->
		{#if error}
			<div class="mb-2 rounded border border-red-900 bg-red-950/40 px-3 py-2 text-xs text-red-300">
				{error}
			</div>
		{/if}

		<div class="overflow-hidden rounded border border-slate-700">
			<table class="w-full text-left text-xs">
				<thead class="bg-slate-900 text-[10px] uppercase tracking-wide text-slate-500">
					<tr>
						<th class="px-3 py-2 font-medium">Started</th>
						<th class="px-3 py-2 font-medium">Signal</th>
						<th class="px-3 py-2 text-right font-medium">Dur</th>
						<th class="px-3 py-2 font-medium">Fmt</th>
						<th class="px-3 py-2 text-right font-medium">Rate</th>
						<th class="px-3 py-2 text-right font-medium">Size</th>
						<th class="px-3 py-2 text-right font-medium">Actions</th>
					</tr>
				</thead>
				<tbody>
					{#each shown as rec (rec.id)}
						<tr class="border-t border-slate-800 hover:bg-slate-800/40">
							<td class="px-3 py-2 font-mono" title={dateFull(rec.startTime)}>{timeOfDay(rec.startTime)}</td>
							<td class="px-3 py-2">
								<button class="flex items-center gap-1.5 text-slate-300 hover:text-sky-300" onclick={() => selectSignal(rec.signalId)}>
									{#if signalLabel(rec.signalId).known}
										<span class="font-mono">{signalLabel(rec.signalId).label}</span>
										<ClassChip cls={signalLabel(rec.signalId).cls} size="xs" />
									{:else}
										<span class="font-mono text-slate-500" title="signal no longer active">{signalLabel(rec.signalId).label}</span>
									{/if}
								</button>
							</td>
							<td class="px-3 py-2 text-right font-mono">{rec.durationS.toFixed(1)}s</td>
							<td class="px-3 py-2 uppercase">{rec.fileFormat}</td>
							<td class="px-3 py-2 text-right font-mono">{rateSps(rec.sampleRate)}</td>
							<td class="px-3 py-2 text-right font-mono">{(rec.sizeBytes / 1e6).toFixed(1)} MB</td>
							<td class="px-3 py-2">
								<div class="flex items-center justify-end gap-1">
									{#if rec.fileFormat.toLowerCase() === 'wav'}
										<button
											class="rounded bg-slate-800 px-1.5 py-0.5 hover:bg-slate-700"
											aria-label="Play recording {rec.id}"
											onclick={() => (playId = playId === rec.id ? null : rec.id)}
										>{playId === rec.id ? '✕' : '▶'}</button>
									{:else}
										<span class="text-[10px] text-slate-600" title="IQ capture — not playable, analyze instead">no audio</span>
									{/if}
									<a
										href={recordingAudioUrl(rec.id)}
										download
										class="rounded bg-slate-800 px-1.5 py-0.5 hover:bg-slate-700"
										aria-label="Download recording {rec.id}"
									>⭳</a>
									<button
										class="rounded bg-sky-900/60 px-1.5 py-0.5 text-sky-300 hover:bg-sky-900"
										title="Open in the time-frequency analysis view (§19)"
										onclick={() => analyze(rec)}
									>analyze ↗</button>
								</div>
							</td>
						</tr>
						{#if playId === rec.id}
							<tr class="border-t border-slate-800 bg-slate-900/80">
								<td colspan="7" class="px-3 py-2">
									<div class="max-w-xs">
										<AudioPlayer recording={rec} />
									</div>
								</td>
							</tr>
						{/if}
					{:else}
						<tr>
							<td colspan="7" class="px-3 py-8 text-center text-slate-500">
								{#if loading}
									loading…
								{:else if error}
									{error}
								{:else}
									recordings are created automatically while a demodulated signal is active — none yet
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		{#if nextLimit}
			<div class="mt-2 flex items-center justify-center gap-3">
				<button
					class="rounded bg-slate-800 px-3 py-1 text-xs text-slate-300 hover:bg-slate-700 disabled:opacity-40"
					disabled={loading || recordings.length === 0}
					onclick={() => (limit = nextLimit)}
				>load more (up to {nextLimit})</button>
				{#if recordings.length === 0}
					<span class="text-[10px] text-slate-600">nothing more to load</span>
				{/if}
			</div>
		{:else if recordings.length > 0}
			<div class="mt-2 text-center text-[10px] text-slate-600">at the API's 500-row cap</div>
		{/if}
	</div>
</div>
