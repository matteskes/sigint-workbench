<script lang="ts">
	import { onMount } from 'svelte';

	let canvasEl: HTMLCanvasElement;
	let ctx: CanvasRenderingContext2D | null;
	let animFrame = 0;

	// Placeholder: will be driven by real-time FFT data from WebSocket
	let data: number[] = Array(128).fill(0);

	function draw() {
		if (!ctx) return;
		const w = canvasEl.width;
		const h = canvasEl.height;

		ctx.clearRect(0, 0, w, h);
		ctx.fillStyle = '#0f172a';
		ctx.fillRect(0, 0, w, h);

		// Draw spectrum bars
		const barWidth = w / data.length;
		for (let i = 0; i < data.length; i++) {
			const v = data[i];
			const barH = v * h;
			const hue = 220 - v * 180; // blue to red
			ctx.fillStyle = `hsl(${hue}, 80%, 50%)`;
			ctx.fillRect(i * barWidth, h - barH, barWidth - 1, barH);
		}

		// Grid lines
		ctx.strokeStyle = '#1e293b';
		ctx.lineWidth = 1;
		for (let i = 1; i < 4; i++) {
			const y = (h / 4) * i;
			ctx.beginPath();
			ctx.moveTo(0, y);
			ctx.lineTo(w, y);
			ctx.stroke();
		}

		animFrame = requestAnimationFrame(draw);
	}

	onMount(() => {
		ctx = canvasEl.getContext('2d');
		if (ctx) draw();
		return () => cancelAnimationFrame(animFrame);
	});
</script>

<div class="p-3">
	<div class="text-xs font-semibold text-slate-400 mb-2">Spectrum</div>
	<canvas
		bind:this={canvasEl}
		width={224}
		height={128}
		class="w-full rounded bg-slate-900"
	></canvas>
</div>