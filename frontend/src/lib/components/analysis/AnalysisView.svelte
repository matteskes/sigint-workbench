<script lang="ts">
	// §10: the §19 time-frequency instrument at full size. The parameter
	// form is the old TfrPanel's, with each method's artifact note under
	// the selector instead of hidden; the render is server-side compute
	// (§19.1) so the status line shows in-flight state and never fakes
	// progress. Seeds: ?recording=&signal= from the library/inspector,
	// or the §18 waterfall drag-selection via spectrumSelection (F3).
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		requestTFR,
		fetchRecordings,
		type Recording,
		type TFRRequest,
		type TFRResult
	} from '$lib/api/client';
	import {
		TFR_METHODS,
		TFR_WINDOWS,
		TFR_MIN_NFFT,
		TFR_MAX_NFFT,
		TFR_MAX_SPAN_S,
		spectrumSelection,
		freqSpanForRecording,
		spanForRecording,
		type TFRMethod,
		type TFRWindow
	} from '$lib/stores/tfr';
	import { signals } from '$lib/stores/signals';
	import TfrCanvas from './TfrCanvas.svelte';
	import ClassChip from '../ui/ClassChip.svelte';
	import { freqHz, rateSps, dateFull } from '$lib/ui/format';

	// ── Recording resolution ──
	let recording = $state<Recording | null>(null);
	let recError = $state('');
	let resolving = $state(false);
	// Picker state when no recording is known (plain /analysis).
	let picker = $state<Recording[]>([]);
	let pickerLoading = $state(false);

	const signalIdParam = $derived(page.url.searchParams.get('signal') ?? '');
	const recordingIdParam = $derived(page.url.searchParams.get('recording') ?? '');

	async function resolveRecording(id: string, signalId: string): Promise<void> {
		resolving = true;
		recError = '';
		try {
			// The API has no single-recording GET — list the signal's
			// recordings and find the row (§13.1 honest mapping).
			const recs = await fetchRecordings({ signalId, limit: 100 });
			const rec = recs.find((r) => r.id === id);
			if (rec) {
				recording = rec;
			} else {
				recording = null;
				recError = 'recording not found for this signal — it may have been purged';
			}
		} catch (e) {
			recording = null;
			recError = e instanceof Error ? e.message : String(e);
		} finally {
			resolving = false;
		}
	}

	async function loadPicker(): Promise<void> {
		pickerLoading = true;
		try {
			picker = await fetchRecordings({ limit: 50 });
		} catch {
			picker = [];
		} finally {
			pickerLoading = false;
		}
	}

	// React to deep links.
	$effect(() => {
		const rid = recordingIdParam;
		const sid = signalIdParam;
		if (rid) {
			void resolveRecording(rid, sid);
		} else {
			recording = null;
			if (!pickerLoading && picker.length === 0) void loadPicker();
		}
	});

	function pickFromList(rec: Recording): void {
		recording = rec;
		void goto(`/analysis?recording=${encodeURIComponent(rec.id)}&signal=${encodeURIComponent(rec.signalId)}`, {
			replaceState: true
		});
	}

	const signalInfo = $derived(
		recording ? $signals.find((s) => s.id === recording!.signalId) ?? null : null
	);

	// ── Parameters (identical controls to the old TfrPanel) ──
	let method = $state<TFRMethod>('stft');
	let win = $state<TFRWindow>('hamming');
	let nfft = $state<number>(512);
	let overlap = $state<number>(0.75);
	let t0 = $state<number>(0);
	let t1 = $state<number>(0);
	let cropFull = $state(true);
	let cropLoMHz = $state<number>(0);
	let cropHiMHz = $state<number>(0);

	let result = $state<TFRResult | null>(null);
	let renderError = $state('');
	let loading = $state(false);
	let spanWarning = $derived(
		t1 - t0 > TFR_MAX_SPAN_S
			? `span ${(t1 - t0).toFixed(1)} s exceeds the ${TFR_MAX_SPAN_S} s cap — it will be clamped by the recorder`
			: ''
	);

	const meta = $derived(TFR_METHODS[method]);
	const nfftChoices = $derived(
		[256, 512, 1024, 2048, 4096, 8192, 16384].filter((n) => n <= TFR_MAX_NFFT && n >= TFR_MIN_NFFT)
	);

	// Seeds: waterfall selection prefills time + crop when it overlaps
	// this recording (§19.4); otherwise the recording's head.
	$effect(() => {
		const rec = recording;
		if (!rec) return;
		t0 = 0;
		t1 = Math.min(rec.durationS, TFR_MAX_SPAN_S);
		const sel = $spectrumSelection;
		if (sel) {
			const span = spanForRecording(sel, rec);
			if (span) [t0, t1] = span;
		}
		const fs = freqSpanForRecording(sel, rec.centerFreq, rec.sampleRate);
		if (fs) {
			cropFull = false;
			cropLoMHz = Number((fs[0] / 1e6).toFixed(3));
			cropHiMHz = Number((fs[1] / 1e6).toFixed(3));
		} else {
			cropFull = true;
		}
	});

	async function render(): Promise<void> {
		const rec = recording;
		if (!rec || loading) return;
		loading = true;
		renderError = '';
		try {
			const req: TFRRequest = { method, nfft, t0, t1, overlap };
			if (meta.windowed) req.window = win;
			if (!cropFull && cropHiMHz > cropLoMHz) {
				req.freqSpan = [cropLoMHz * 1e6, cropHiMHz * 1e6];
			}
			result = await requestTFR(rec.id, req);
		} catch (e) {
			result = null;
			const msg = e instanceof Error ? e.message : String(e);
			// §14: 404 covers both tfr.enabled: false and purged rows.
			renderError = msg.includes('404')
				? 'tfr service not configured (404) — set tfr.enabled: true in recorder.yaml, or the recording is gone'
				: msg;
		} finally {
			loading = false;
		}
	}
</script>

<div class="min-w-0 flex-1 overflow-y-auto p-4">
	<div class="mx-auto max-w-6xl space-y-3">
		<!-- ── Metadata header (§10) ── -->
		<div class="flex flex-wrap items-center gap-3 rounded border border-slate-700 bg-slate-900/60 px-3 py-2 text-xs">
			<h2 class="text-sm font-semibold">Analysis</h2>
			{#if recording}
				<span class="font-mono text-slate-300" title={dateFull(recording.startTime)}>
					{dateFull(recording.startTime).split(', ').pop()}
				</span>
				<span class="font-mono text-slate-300">{freqHz(recording.centerFreq)}</span>
				<span class="uppercase text-slate-400">{recording.fileFormat}</span>
				<span class="font-mono text-slate-400">{rateSps(recording.sampleRate)}</span>
				<span class="text-slate-400">{recording.durationS.toFixed(1)} s</span>
				{#if signalInfo}
					<ClassChip cls={signalInfo.class} size="xs" />
				{/if}
				<div class="ml-auto flex items-center gap-2 text-[11px]">
					<button
						class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700"
						onclick={() => goto(`/signals?signal=${encodeURIComponent(recording!.signalId)}`)}
					>↗ signal</button>
					<button
						class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700"
						onclick={() => goto('/spectrum')}
						title="Back to the workbench — the drag-selection store survives navigation (F3)"
					>↩ spectrum</button>
				</div>
			{:else}
				<span class="text-slate-500">
					{#if resolving}
						resolving recording…
					{:else if recError}
						<span class="text-red-400">{recError}</span>
					{:else}
						pick a recording to inspect — or arrive from the workbench selection or the library
					{/if}
				</span>
			{/if}
		</div>

<!-- BLOCK2 -->
		{#if !recording}
			<!-- ── Recording picker (plain /analysis) ── -->
			<div class="rounded border border-slate-700 bg-slate-900/60 p-3">
				<div class="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-500">
					Latest recordings (all signals)
				</div>
				{#if pickerLoading}
					<div class="text-xs text-slate-500">loading…</div>
				{:else if picker.length === 0}
					<div class="text-xs text-slate-500">
						no recordings yet — they are created automatically while a demodulated signal is active
					</div>
				{:else}
					<ul class="divide-y divide-slate-800 text-xs">
						{#each picker as rec (rec.id)}
							<li>
								<button
									class="flex w-full items-center gap-3 px-1 py-1.5 text-left hover:bg-slate-800/60"
									onclick={() => pickFromList(rec)}
								>
									<span class="font-mono text-slate-300">{dateFull(rec.startTime)}</span>
									<span class="font-mono text-slate-400">{freqHz(rec.centerFreq)}</span>
									<span class="uppercase text-slate-500">{rec.fileFormat}</span>
									<span class="text-slate-500">{rec.durationS.toFixed(1)} s · {rateSps(rec.sampleRate)}</span>
									<span class="ml-auto text-sky-400">select</span>
								</button>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		{:else}
<!-- BLOCK3 -->
			<!-- ── Full-size render ── -->
			<div class="rounded border border-slate-700 bg-slate-900/60 p-3">
				{#if result}
					<TfrCanvas {result} />
					<div class="mt-1 flex justify-between text-[10px] text-slate-500">
						<span>{freqHz(result.freqLoHz)}</span>
						<span>
							t {result.t0.toFixed(1)}–{result.t1.toFixed(1)} s · {result.method}{result.window ? ` · ${result.window}` : ''}
							· nfft {result.nfft} · hop {result.hop} · {result.rows}×{result.cols} · {result.elapsedMs} ms
						</span>
						<span>{freqHz(result.freqHiHz)}</span>
					</div>
					<div class="mt-1 text-[11px] text-amber-400/90">⚠ {result.artifact}</div>
					{#if result.note}
						<div class="text-[10px] text-slate-500">{result.note}</div>
					{/if}
				{:else if loading}
					<!-- §19.1: server-side compute — in-flight, never faked -->
					<div class="flex h-40 items-center justify-center rounded border border-dashed border-slate-700 text-xs text-slate-500">
						computing on the recorder… (this is a server-side render)
					</div>
				{:else if renderError}
					<div class="flex h-40 items-center justify-center rounded border border-red-900/60 bg-red-950/20 px-6 text-center text-xs text-red-300">
						{renderError}
					</div>
				{:else}
					<div class="flex h-40 items-center justify-center rounded border border-dashed border-slate-700 text-xs text-slate-500">
						set parameters and render
					</div>
				{/if}
			</div>
<!-- BLOCK4 -->
			<!-- ── Parameters ── -->
			<div class="rounded border border-slate-700 bg-slate-900/60 p-3">
				<div class="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 lg:grid-cols-6">
					<label class="text-[10px] text-slate-500">
						method
						<select class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={method}>
							{#each Object.keys(TFR_METHODS) as m (m)}
								<option value={m}>{TFR_METHODS[m as TFRMethod].label}</option>
							{/each}
						</select>
					</label>
					<label class="text-[10px] text-slate-500">
						window
						{#if meta.windowed}
							<select class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={win}>
								{#each TFR_WINDOWS as w (w)}
									<option value={w}>{w}</option>
								{/each}
							</select>
						{:else}
							<div class="mt-0.5 rounded bg-slate-800/50 px-1 py-1 text-slate-600">n/a</div>
						{/if}
					</label>
					<label class="text-[10px] text-slate-500">
						nfft
						<select class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={nfft}>
							{#each nfftChoices as n (n)}
								<option value={n}>{n}</option>
							{/each}
						</select>
					</label>
					<label class="text-[10px] text-slate-500">
						overlap
						<select class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={overlap}>
							{#each [0, 0.5, 0.75, 0.9] as o (o)}
								<option value={o}>{(o * 100).toFixed(0)}%</option>
							{/each}
						</select>
					</label>
					<label class="text-[10px] text-slate-500">
						t0 (s)
						<input type="number" class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={t0} min="0" max={recording.durationS} step="0.1" />
					</label>
					<label class="text-[10px] text-slate-500">
						t1 (s)
						<input type="number" class="mt-0.5 w-full rounded bg-slate-800 px-1 py-1 text-xs text-slate-200" bind:value={t1} min="0" max={recording.durationS} step="0.1" />
					</label>
				</div>

				<!-- freq crop (§10: "freq crop [full]") -->
				<div class="mt-2 flex flex-wrap items-center gap-2 text-[10px] text-slate-500">
					<label class="flex items-center gap-1">
						<input type="checkbox" bind:checked={cropFull} />
						full band ({freqHz(recording.centerFreq - recording.sampleRate / 2)} – {freqHz(recording.centerFreq + recording.sampleRate / 2)})
					</label>
					{#if !cropFull}
						<label>
							crop lo (MHz)
							<input type="number" class="ml-1 w-28 rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200" bind:value={cropLoMHz} step="0.001" />
						</label>
						<label>
							crop hi (MHz)
							<input type="number" class="ml-1 w-28 rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200" bind:value={cropHiMHz} step="0.001" />
						</label>
					{/if}
				</div>

				<!-- §19.2 artifact note under the selector, not hidden (§10) -->
				<div class="mt-2 text-[10px] text-amber-400/80">{meta.artifact}</div>

				<div class="mt-3 flex items-center gap-3">
					<button
						class="rounded bg-sky-700 px-4 py-1.5 text-xs font-medium text-white hover:bg-sky-600 disabled:opacity-40"
						disabled={loading}
						onclick={render}
					>
						{loading ? 'rendering…' : 'Render'}
					</button>
					<span class="text-[10px] text-slate-500">
						{#if loading}
							in flight — server-side compute (§19.1)
						{:else if spanWarning}
							<span class="text-amber-400">{spanWarning}</span>
						{:else}
							status: ready
						{/if}
					</span>
				</div>
			</div>
		{/if}
	</div>
</div>
