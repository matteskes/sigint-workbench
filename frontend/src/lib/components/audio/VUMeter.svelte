<script lang="ts">
	let { level = 0 }: { level?: number } = $props();

	// level: 0.0 to 1.0
	const width = $derived(Math.min(level * 100, 100));
	const color = $derived(level > 0.8 ? 'bg-red-500' : level > 0.5 ? 'bg-amber-500' : 'bg-green-500');
</script>

<div class="w-full h-3 bg-slate-800 rounded overflow-hidden">
	<div
		class="h-full {color} transition-all duration-75"
		style="width: {width}%"
	></div>
</div>
<div class="flex justify-between text-[10px] text-slate-600 mt-0.5">
	<span>-∞</span>
	<span>{(level * 100).toFixed(0)}%</span>
	<span>0 dB</span>
</div>