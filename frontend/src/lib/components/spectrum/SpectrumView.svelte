<script lang="ts">
	import { onMount } from 'svelte';
	import {
		WATERFALL_ROWS,
		spectrumSdrIds,
		selectedSdrId,
		selectedSpectrum,
		markedFreqHz
	} from '$lib/stores/spectrum';
	import { spectrumSelection, dragToSelection } from '$lib/stores/tfr';
	import { signals } from '$lib/stores/signals';
	import { freqHz } from '$lib/ui/format';
	import type { SpectrumFrame } from '$lib/api/client';

	// §18.3: plain <canvas> + requestAnimationFrame, no new dependencies.
	let lineEl: HTMLCanvasElement;
	let waterfallEl: HTMLCanvasElement;
	let tickEl: HTMLCanvasElement;
	let anim = 0;
	let open = true;

	// §16: reduced motion slows the redraw cadence to 1 fps — the data
	// stays current, the waterfall stops streaming (a "paused" caption
	// says so honestly).
	const REDUCED_MOTION =
		typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

	// §18.2 honesty: db values are §5.6 uncalibrated relative dB, so the
	// axis reads "dB (rel.)" — default span −100…0 with an autoscale
	// toggle (§18.3).
	const DB_MIN = -100;
	const DB_MAX = 0;
	let autoDb = false;

	// The rAF loop redraws at most once per animation frame, and only
	// when the selected spectrum actually changed (feed ≤ rate_hz, §18.1).
	let dirty = true;

	$: selectedId = $selectedSdrId ?? $spectrumSdrIds[0] ?? '';
	$: bins = $selectedSpectrum?.latest.bins ?? 256;
	$: latest = $selectedSpectrum?.latest ?? null;
	$: spanLabel = latest
		? `${(latest.freqHz / 1e6).toFixed(3)} MHz ±${(latest.sampleRate / 2 / 1e6).toFixed(3)}`
		: 'waiting for frames…';

	const HEAT: [number, number, number][] = [
		[2, 6, 23], // floor
		[8, 47, 138], // blue
		[14, 165, 210], // cyan
		[74, 222, 128], // green
		[250, 204, 21], // amber
		[239, 68, 68] // hot
	];

	function heatRGB(v01: number): [number, number, number] {
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

	function autoRange(rows: SpectrumFrame[]): [number, number] {
		let lo = Infinity;
		let hi = -Infinity;
		for (const r of rows.slice(-60)) {
			for (const v of r.db) {
				if (v < lo) lo = v;
				if (v > hi) hi = v;
			}
		}
		if (!Number.isFinite(lo)) return [DB_MIN, DB_MAX];
		return [Math.floor(lo) - 5, Math.ceil(hi) + 5];
	}

	function drawLine(f: SpectrumFrame | null, lo: number, hi: number): void {
		const ctx = lineEl?.getContext('2d');
		if (!ctx) return;
		const w = lineEl.width;
		const h = lineEl.height;
		ctx.fillStyle = '#020617';
		ctx.fillRect(0, 0, w, h);
		const span = hi - lo || 1;

		// Quarter grid with dB labels (§18.2: relative dB, never dBm).
		ctx.strokeStyle = '#1e293b';
		ctx.fillStyle = '#64748b';
		ctx.lineWidth = 1;
		ctx.font = '9px ui-monospace, monospace';
		ctx.textAlign = 'left';
		for (let i = 0; i <= 4; i++) {
			const y = Math.round((h / 4) * i) + 0.5;
			ctx.beginPath();
			ctx.moveTo(0, y);
			ctx.lineTo(w, y);
			ctx.stroke();
			ctx.fillText(String(Math.round(hi - (span * i) / 4)), 2, Math.min(h - 1, y + 9));
		}

		if (!f || f.db.length === 0) return;

		// Center-frequency marker (freqHz sits mid-span, §18.2).
		ctx.strokeStyle = 'rgba(148, 163, 184, 0.35)';
		ctx.beginPath();
		ctx.moveTo(w / 2 + 0.5, 0);
		ctx.lineTo(w / 2 + 0.5, h);
		ctx.stroke();

		// Trace: db[0] is the left edge = −fs/2 (§18.2 ordering).
		ctx.strokeStyle = '#38bdf8';
		ctx.beginPath();
		const n = f.db.length;
		for (let i = 0; i < n; i++) {
			const x = n === 1 ? 0 : (i / (n - 1)) * (w - 1);
			const v01 = Math.max(0, Math.min(1, (f.db[i] - lo) / span));
			const y = h - 1 - v01 * (h - 2);
			if (i === 0) ctx.moveTo(x, y);
			else ctx.lineTo(x, y);
		}
		ctx.stroke();
	}

	function drawWaterfall(rows: SpectrumFrame[], lo: number, hi: number): void {
		const ctx = waterfallEl?.getContext('2d');
		if (!ctx) return;
		const w = waterfallEl.width;
		const h = waterfallEl.height;
		ctx.fillStyle = '#020617';
		ctx.fillRect(0, 0, w, h);
		if (rows.length === 0) return;

		const span = hi - lo || 1;
		const img = ctx.createImageData(w, h);
		const d = img.data;
		// Newest frame at the top, history growing downward. Rows are the
		// frames we received — a frame that never arrived (§18.1 pacing,
		// §14.3 saturation) is a skipped row, never interpolated (§18.3).
		for (let r = 0; r < rows.length && r < h; r++) {
			const y = rows.length - 1 - r;
			const db = rows[r].db;
			if (!db || db.length === 0) continue;
			const rowOff = y * w;
			for (let x = 0; x < w; x++) {
				const v = db[Math.min(db.length - 1, Math.floor((x * db.length) / w))];
				const c = heatRGB((v - lo) / span);
				const o = (rowOff + x) * 4;
				d[o] = c[0];
				d[o + 1] = c[1];
				d[o + 2] = c[2];
				d[o + 3] = 255;
			}
		}
		ctx.putImageData(img, 0, 0);
	}

	function drawTicks(f: SpectrumFrame | null): void {
		const ctx = tickEl?.getContext('2d');
		if (!ctx) return;
		const w = tickEl.width;
		const h = tickEl.height;
		ctx.clearRect(0, 0, w, h);
		if (!f || !(f.sampleRate > 0)) return;
		// Frequency ticks derive from freqHz ± sampleRate/2 (§18.3).
		ctx.fillStyle = '#64748b';
		ctx.font = '9px ui-monospace, monospace';
		const lo = f.freqHz - f.sampleRate / 2;
		for (let i = 0; i <= 4; i++) {
			const mhz = (lo + (f.sampleRate * i) / 4) / 1e6;
			ctx.textAlign = i === 0 ? 'left' : i === 4 ? 'right' : 'center';
			ctx.fillText(mhz.toFixed(3), (i / 4) * (w - 1), h - 2);
		}
	}

	function toggleOpen(): void {
		open = !open;
		dirty = true;
	}

	function toggleAuto(): void {
		autoDb = !autoDb;
		dirty = true;
	}

	function pickSdr(e: Event): void {
		const id = (e.currentTarget as HTMLSelectElement).value;
		selectedSdrId.set(id === '' ? null : id);
		dirty = true;
	}

	// §19.4 drag-select (D10): a horizontal drag across the waterfall
	// picks a frequency span for the TFR inspector; the time context is
	// the visible waterfall window. A plain click (degenerate drag)
	// clears the selection. Purely a client-side draft — nothing is
	// fetched until a recording render is requested in the inspector.
	let dragX0: number | null = null;
	let dragX1: number | null = null;

	$: selection = $spectrumSelection;
	$: selLabel = selection
		? `sel ${(Math.min(selection.freqLoHz, selection.freqHiHz) / 1e6).toFixed(3)}–${(
				Math.max(selection.freqLoHz, selection.freqHiHz) / 1e6
		  ).toFixed(3)} MHz`
		: '';

	function canvasX(e: PointerEvent): number {
		if (!waterfallEl) return 0;
		const rect = waterfallEl.getBoundingClientRect();
		if (rect.width < 1) return 0;
		return ((e.clientX - rect.left) / rect.width) * waterfallEl.width;
	}

	function dragDown(e: PointerEvent): void {
		dragX0 = canvasX(e);
		dragX1 = dragX0;
	}

	function dragMove(e: PointerEvent): void {
		if (dragX0 !== null) dragX1 = canvasX(e);
	}

	function dragUp(e: PointerEvent): void {
		if (dragX0 === null || !waterfallEl) {
			dragX0 = dragX1 = null;
			return;
		}
		const x1 = canvasX(e);
		const f: SpectrumFrame | null = latest;
		if (f && f.sampleRate > 0) {
			const t0 = $selectedSpectrum?.rows.length
				? Date.parse($selectedSpectrum.rows[0].t)
				: NaN;
			const t1 = Date.parse(f.t);
			const sel = dragToSelection(
				dragX0,
				x1,
				waterfallEl.width,
				f.freqHz - f.sampleRate / 2,
				f.freqHz + f.sampleRate / 2,
				Number.isNaN(t0) ? 0 : t0,
				Number.isNaN(t1) ? 0 : t1
			);
			spectrumSelection.set(sel);
		}
		dragX0 = dragX1 = null;
	}

	/**
	 * §7 signal overlay (passive): active signals whose freqHz ±
	 * bandwidthHz/2 intersects the frame span render as small ticks on
	 * the line canvas, plus the Inspector's marked frequency (§6) as an
	 * amber line. Reads stores directly — no per-event work beyond the
	 * coalesced flush that marks the canvas dirty (§14.3).
	 */
	function drawSignalOverlay(f: SpectrumFrame | null): void {
		const ctx = lineEl?.getContext('2d');
		if (!ctx || !f || f.sampleRate <= 0) return;
		const w = lineEl.width;
		const h = lineEl.height;
		const lo = f.freqHz - f.sampleRate / 2;
		const span = f.sampleRate;
		const xFor = (hz: number) => ((hz - lo) / span) * (w - 1);

		// Inspector's marked frequency (spectrum span ↗ action).
		const marked = $markedFreqHz;
		if (marked !== null && marked >= lo && marked <= lo + span) {
			const x = Math.round(xFor(marked)) + 0.5;
			ctx.strokeStyle = '#f59e0b';
			ctx.lineWidth = 1;
			ctx.beginPath();
			ctx.moveTo(x, 0);
			ctx.lineTo(x, h);
			ctx.stroke();
		}

		// Active-signal ticks (white) — derived once per redraw.
		ctx.strokeStyle = '#e2e8f0';
		ctx.fillStyle = '#cbd5e1';
		ctx.font = '8px ui-monospace, monospace';
		ctx.textAlign = 'center';
		let drawn = 0;
		for (const s of $signals) {
			if (s.freqHz + s.bandwidthHz / 2 < lo || s.freqHz - s.bandwidthHz / 2 > lo + span) continue;
			if (drawn >= 24) break; // label budget on narrow canvases
			const x = Math.round(xFor(s.freqHz)) + 0.5;
			if (x < 0 || x > w) continue;
			ctx.beginPath();
			ctx.moveTo(x, 0);
			ctx.lineTo(x, 6);
			ctx.stroke();
			if (w / (f.sampleRate / 1e6) > 30 || drawn < 8) {
				ctx.fillText(`${(s.freqHz / 1e6).toFixed(2)}`, x, 14);
			}
			drawn++;
		}
	}

	onMount(() => {
		const unsub = selectedSpectrum.subscribe(() => {
			dirty = true;
		});
		// Overlay inputs also mark dirty (cheap booleans; the draw is
		// bounded by the existing rAF + dirty gate).
		const unsubSignals = signals.subscribe(() => {
			dirty = true;
		});
		const unsubMarked = markedFreqHz.subscribe(() => {
			dirty = true;
		});
		let lastDraw = 0;
		const loop = () => {
			if (dirty) {
				const now = Date.now();
				// §16: reduced motion → at most one redraw per second.
				if (!REDUCED_MOTION || now - lastDraw >= 1000) {
					dirty = false;
					lastDraw = now;
					const rows = $selectedSpectrum?.rows ?? [];
					const [lo, hi] = autoDb && rows.length > 0 ? autoRange(rows) : [DB_MIN, DB_MAX];
					drawLine(latest, lo, hi);
					drawSignalOverlay(latest);
					drawWaterfall(rows, lo, hi);
					drawTicks(latest);
				}
			}
			anim = requestAnimationFrame(loop);
		};
		anim = requestAnimationFrame(loop);
		return () => {
			cancelAnimationFrame(anim);
			unsub();
			unsubSignals();
			unsubMarked();
		};
	});
</script>

<div class="p-3">
	<button
		class="mb-2 flex w-full items-center justify-between text-xs font-semibold text-slate-400"
		on:click={toggleOpen}
	>
		<span>Spectrum{selectedId ? ` · ${selectedId}` : ''}</span>
		<span>{open ? '▾' : '▸'}</span>
	</button>

	{#if open}
		<div class="mb-2 flex items-center gap-2">
			<select
				class="min-w-0 flex-1 rounded bg-slate-800 px-1 py-0.5 text-xs text-slate-200"
				aria-label="SDR"
				value={selectedId}
				on:change={pickSdr}
			>
				{#each $spectrumSdrIds as id (id)}
					<option value={id}>{id}</option>
				{/each}
			</select>
			<button
				class="rounded px-1.5 py-0.5 text-xs {autoDb
					? 'bg-sky-700 text-white'
					: 'bg-slate-800 text-slate-400'}"
				title="Autoscale the dB axis to the recent frames"
				on:click={toggleAuto}
			>
				auto
			</button>
		</div>

		<!-- role="img" + aria-label is the intended ARIA pattern for these
		canvases (UI-DESIGN.md); Svelte's a11y heuristic counts <canvas> as
		interactive, so the non-interactive role gets flagged. -->
		<!-- svelte-ignore a11y_no_interactive_element_to_noninteractive_role -->
		<canvas
			bind:this={lineEl}
			width={bins}
			height={96}
			class="w-full rounded bg-slate-950"
			role="img"
			aria-label="Spectrum line, {spanLabel}, dB relative"
		></canvas>
		<div class="relative touch-none select-none">
			<!-- svelte-ignore a11y_no_interactive_element_to_noninteractive_role -->
			<canvas
				bind:this={waterfallEl}
				width={bins}
				height={WATERFALL_ROWS}
				class="mt-1 block w-full rounded bg-slate-950 cursor-crosshair"
				role="img"
				aria-label="Waterfall, {spanLabel}, drag to pick an analysis span"
				on:pointerdown={dragDown}
				on:pointermove={dragMove}
				on:pointerup={dragUp}
				on:pointerleave={dragUp}
			></canvas>
			{#if dragX0 !== null && dragX1 !== null && dragX0 !== dragX1}
				<div
					class="pointer-events-none absolute top-0 bottom-0 border-x border-sky-400 bg-sky-400/10"
					style="left:{(Math.min(dragX0, dragX1) / bins) * 100}%; width:{(
						Math.abs(dragX1 - dragX0) /
						bins
					) * 100}%"
				></div>
			{/if}
		</div>
		<canvas bind:this={tickEl} width={bins} height={14} class="w-full"></canvas>

		<div class="mt-1 text-[10px] text-slate-500">
			{spanLabel} · dB (rel.)
			{#if selLabel}
				· <span class="text-sky-400">{selLabel}</span>
			{/if}
			{#if REDUCED_MOTION}
				· <span class="text-amber-400">waterfall paused (reduced motion)</span>
			{/if}
		</div>
		<span class="sr-only" aria-live="off">
			{#if latest}
				Spectrum {spanLabel}. Peak
				{freqHz(latest.freqHz - latest.sampleRate / 2 + latest.db.indexOf(Math.max(...latest.db)) * latest.df)}
				at {Math.max(...latest.db)} dB relative.
			{:else}
				No spectrum frames received yet.
			{/if}
		</span>
		{#if !selection}
			<div class="mt-0.5 text-[10px] text-slate-600">
				drag across the waterfall to pick a span for time-frequency analysis
			</div>
		{/if}
	{/if}
</div>