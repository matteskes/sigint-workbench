<script lang="ts">
	import '../app.css';
	let { children } = $props();
</script>

<div class="min-h-screen bg-slate-900 text-slate-100 flex flex-col">
	<!-- Header -->
	<header class="h-12 border-b border-slate-700 flex items-center px-4 gap-4 shrink-0">
		<h1 class="text-lg font-bold tracking-tight">
			<span class="text-blue-400">SIGINT</span> Workbench
		</h1>
		<span class="text-xs text-slate-500">Signal Monitoring & Analysis</span>
		<div class="ml-auto flex items-center gap-3">
			<span class="text-xs text-slate-400" id="sdr-status">SDR: disconnected</span>
			<span class="text-xs text-slate-400" id="signal-count">0 signals</span>
		</div>
	</header>

	<!-- Main content -->
	<main class="flex-1 flex overflow-hidden">
		{@render children()}
	</main>
</div>