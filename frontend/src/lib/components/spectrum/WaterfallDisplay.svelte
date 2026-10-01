<script lang="ts">
	import { onMount } from 'svelte';

	let canvasEl: HTMLCanvasElement;
	let ctx: CanvasRenderingContext2D | null;
	let animFrame = 0;
	let row = 0;
	const ROWS = 128;

	function draw() {
		if (!ctx) return;
		const w = canvasEl.width;
		const h = canvasEl.height;

		// Scroll down
		ctx.drawImage(canvasEl, 0, 1);
		ctx.fillStyle = '#0f172a';
		ctx.fillRect(0, 0, w, 1);

		// Draw new row (random for now, will be real data)
		const imgData = ctx.getImageData(0, 0, w, 1);
		for (let x = 0; x < w; x++) {
			const v = Math.random() * 0.1; // placeholder
			const idx = (0 * w + x) * 4;
			imgData.data[idx] = v * 255;
			imgData.data[idx + 1] = v * 100;
			imgData.data[idx + 2] = 255 - v * 200;
			imgData.data[idx + 3] = 255;
		}
		ctx.putImageData(imgData, 0, 0);

		animFrame = requestAnimationFrame(draw);
	}

	onMount(() => {
		ctx = canvasEl.getContext('2d');
		if (ctx) {
			ctx.fillStyle = '#0f172a';
			ctx.fillRect(0, 0, canvasEl.width, canvasEl.height);
			draw();
		}
		return () => cancelAnimationFrame(animFrame);
	});
</script>

<div class="p-3">
	<div class="text-xs font-semibold text-slate-400 mb-2">Waterfall</div>
	<canvas
		bind:this={canvasEl}
		width={224}
		height={ROWS}
		class="w-full rounded bg-slate-900"
	></canvas>
</div>