<script lang="ts">
	import { requestTFR, type Recording, type TFRRequest, type TFRResult } from '$lib/api/client';
	import {
		TFR_METHODS,
		TFR_WINDOWS,
		TFR_MIN_NFFT,
		TFR_MAX_NFFT,
		spectrumSelection,
		freqSpanForRecording,
		spanForRecording,
		type TFRMethod,
		type TFRWindow
	} from '$lib/stores/tfr';

	// §19.4: the time-frequency inspector. One recording per panel;
	// method + parameter picker with the §19.2 artifact note beside the
	// render; plain <canvas>, no new dependencies (D10).
	let { recording }: { recording: Recording } = $props();

	let method = $state<TFRMethod>('stft');
	let win = $state<TFRWindow>('hamming');
	let nfft = $state<number>(512);
	let overlap = $state<number>(0.75);
	let t0 = $state<number>(0);
	let t1 = $state<number>(0);
	let result = $state<TFRResult | null>(null);
	let error = $state('');
	let loading = $state(false);
	let canvasEl = $state<HTMLCanvasElement | undefined>();

	const meta = $derived(TFR_METHODS[method]);
	const nfftChoices = $derived(
		[256, 512, 1024, 2048, 4096, 8192, 16384].filter((n) => n <= TFR_MAX_NFFT)
	);

	// Open with the §18 drag-select prefilled when it overlaps this
	// recording (§19.4: the waterfall gesture seeds the inspector).
	$effect(() => {
		const sel = $spectrumSelection;
		t0 = 0;
		t1 = Math.min(recording.durationS, 30);
		if (sel) {
			const span = spanForRecording(sel, recording);
			if (span) [t0, t1] = span;
		}
	});

	async function render(): Promise<void> {
		loading = true;
		error = '';
		try {
			const req: TFRRequest = { method, nfft, t0, t1, overlap };
			if (meta.windowed) req.window = win;
			const sel = $spectrumSelection;
			const fs = freqSpanForRecording(sel, recording.centerFreq, recording.sampleRate);
			if (fs) req.freqSpan = fs;
			result = await requestTFR(recording.id, req);
		} catch (e) {
			result = null;
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// Tile rendering: int8 dB relative to dbRef (floor −128), heat
	// palette, row 0 = freqLoHz at the bottom (waterfall orientation).
	const HEAT: [number, number, number][] = [
		[2, 6, 23],
		[8, 47, 138],
		[14, 165, 210],
		[74, 222, 128],
		[250, 204, 21],
		[239, 68, 68]
	];

	function heat(v01: number): [number, number, number] {
		const t = Math.max(0, Math.min(1, v01));
		const p = t * (HEAT.length - 1);
		const i = Math.min(HEAT.length - 2, Math.floor(p));
		const u = p - i;
		const a = HEAT[i];
		const b = HEAT[i + 1];
		return [
			Math.round(a[0] + u * (b[0] - a[0])),
			Math.round(a[1] + u * (b[1] - a[1])),
			Math.round(a[2] + u * (b[2] - a[2]))
		];
	}

	$effect(() => {
		const res = result;
		if (!res || !canvasEl) return;
		canvasEl.width = res.cols;
		canvasEl.height = res.rows;
		const ctx = canvasEl.getContext('2d');
		if (!ctx) return;
		const img = ctx.createImageData(res.cols, res.rows);
		const d = img.data;
		for (let row = 0; row < res.rows; row++) {
			const srcRow = res.rows - 1 - row; // row 0 (lowest freq) at the bottom
			for (let col = 0; col < res.cols; col++) {
				const v = res.tile[srcRow * res.cols + col] ?? -128;
				const [r, g, b] = heat((v + 128) / 128);
				const o = (row * res.cols + col) * 4;
				d[o] = r;
				d[o + 1] = g;
				d[o + 2] = b;
				d[o + 3] = 255;
			}
		}
		ctx.putImageData(img, 0, 0);
	});

	function mhz(hz: number): string {
		return (hz / 1e6).toFixed(3);
	}

	function fmtKb(bytes: number): string {
		if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
		return `${Math.round(bytes / 1024)} KB`;
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<div class="text-xs font-semibold text-slate-400">Time-Frequency (§19)</div>
		<div class="text-[10px] text-slate-500">
			{fmtKb(recording.sizeBytes)} · {recording.sampleRate / 1000} kS/s
		</div>
	</div>

	<!-- Method + parameter picker (§19.4) -->
	<div class="grid grid-cols-2 gap-1.5">
		<label class="text-[10px] text-slate-500">
			method
			<select
				class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				bind:value={method}
			>
				{#each Object.keys(TFR_METHODS) as m (m)}
					<option value={m}>{TFR_METHODS[m as TFRMethod].label}</option>
				{/each}
			</select>
		</label>
		{#if meta.windowed}
			<label class="text-[10px] text-slate-500">
				window
				<select
					class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
					bind:value={win}
				>
					{#each TFR_WINDOWS as w (w)}
						<option value={w}>{w}</option>
					{/each}
				</select>
			</label>
		{:else}
			<div></div>
		{/if}
		<label class="text-[10px] text-slate-500">
			nfft
			<select
				class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				bind:value={nfft}
			>
				{#each nfftChoices as n (n)}
					<option value={n}>{n}</option>
				{/each}
			</select>
		</label>
		<label class="text-[10px] text-slate-500">
			overlap
			<select
				class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				bind:value={overlap}
			>
				{#each [0, 0.5, 0.75, 0.9] as o (o)}
					<option value={o}>{o}</option>
				{/each}
			</select>
		</label>
		<label class="text-[10px] text-slate-500">
			t0 (s)
			<input
				type="number"
				class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				bind:value={t0}
				min="0"
				max={recording.durationS}
				step="0.1"
			/>
		</label>
		<label class="text-[10px] text-slate-500">
			t1 (s)
			<input
				type="number"
				class="mt-0.5 w-full rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				bind:value={t1}
				min="0"
				max={recording.durationS}
				step="0.1"
			/>
		</label>
	</div>

	<button
		class="w-full py-1.5 rounded text-xs font-medium bg-sky-700 text-white hover:bg-sky-600 disabled:opacity-40"
		disabled={loading}
		onclick={render}
	>
		{loading ? 'rendering…' : 'Render'}
	</button>

	{#if error}
		<div class="text-[11px] text-red-400">
			{error}
			{#if error.includes('feature disabled')}
				— set <span class="font-mono">tfr.enabled: true</span> in recorder.yaml
			{/if}
		</div>
	{/if}

	{#if result}
		<!-- Render with the §19.2 artifact note beside it (§19.4) -->
		<div class="space-y-1">
			<canvas bind:this={canvasEl} class="block h-44 w-full rounded bg-slate-950"></canvas>
			<div class="flex justify-between text-[10px] text-slate-500">
				<span>{mhz(result.freqLoHz)} MHz</span>
				<span>t {result.t0.toFixed(1)}–{result.t1.toFixed(1)} s · dB (rel.)</span>
				<span>{mhz(result.freqHiHz)} MHz</span>
			</div>
			<div class="text-[10px] text-slate-500">
				{result.method}{result.window ? ` · ${result.window}` : ''} · nfft {result.nfft} · hop
				{result.hop} · {result.rows}×{result.cols} · {result.elapsedMs} ms
			</div>
			<div class="text-[10px] text-amber-400/90">{result.artifact}</div>
			{#if result.note}
				<div class="text-[10px] text-slate-500">{result.note}</div>
			{/if}
		</div>
	{:else if !error}
		<div class="text-[10px] text-slate-600">
			{#if $spectrumSelection}
				§18 selection prefills the span and frequency crop.
			{:else}
				pick a method and render — or drag a span on the §18 waterfall first.
			{/if}
		</div>
	{/if}
</div>
