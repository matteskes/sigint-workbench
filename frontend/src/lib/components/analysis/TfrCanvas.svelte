<script lang="ts">
	// §13: the TFR tile renderer extracted from TfrPanel so both the
	// (now full-size) analysis view can draw §19.3 results. Plain
	// <canvas>, no new dependencies (D10): int8 dB relative to dbRef at
	// 1 dB/LSB (floor −128), heat palette, row 0 = freqLoHz at the
	// bottom (waterfall orientation). CSS scales the tile buffer; the
	// pixels stay square and honest — no smoothing.
	import type { TFRResult } from '$lib/api/client';

	let { result }: { result: TFRResult } = $props();

	let canvasEl = $state<HTMLCanvasElement | undefined>();

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

	/** CSS gradient matching the palette, for the dB colorbar legend. */
	const gradient = $derived(
		`linear-gradient(to top, ${HEAT.map(
			(c, i) => `rgb(${c[0]} ${c[1]} ${c[2]}) ${(i / (HEAT.length - 1)) * 100}%`
		).join(', ')})`
	);

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
</script>

<canvas
	bind:this={canvasEl}
	class="block w-full rounded bg-slate-950 {result && result.rows > 0 ? '' : 'hidden'}"
	style="height: 55vh; image-rendering: pixelated;"
	role="img"
	aria-label="Time-frequency render, {result?.method ?? ''}, {result?.freqLoHz ?? 0}–{result?.freqHiHz ?? 0} Hz, {result?.t0 ?? 0}–{result?.t1 ?? 0} s"
></canvas>

<!-- dB (rel.) colorbar (§10): honest unit, honest scale -->
<div class="mt-1 flex items-center gap-2 text-[10px] text-slate-500">
	<span class="font-mono">{result ? result.dbRef - 128 : -128} dB</span>
	<div class="h-2 w-40 rounded" style={gradient} title="dB relative to dbRef, 1 dB per tile step"></div>
	<span class="font-mono">{result ? result.dbRef + 127 : 127} dB</span>
	<span>dB (rel.)</span>
</div>
