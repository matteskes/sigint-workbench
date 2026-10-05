<script lang="ts">
	import '../app.css';
	import { sdrs } from '$lib/stores/sdrs';
	import { signalCount } from '$lib/stores/signals';

	let { children } = $props();

	// §14.4.3: the header reflects live state from the stores — the
	// sdr.status feed (backed by REST bootstrap on connect) for the
	// device count, and the signals store for the event count.
	const activeSdrs = $derived($sdrs.filter((s) => s.active).length);
	const totalSdrs = $derived($sdrs.length);
</script>

<div class="min-h-screen bg-slate-900 text-slate-100 flex flex-col">
	<!-- Header -->
	<header class="h-12 border-b border-slate-700 flex items-center px-4 gap-4 shrink-0">
		<h1 class="text-lg font-bold tracking-tight">
			<span class="text-blue-400">SIGINT</span> Workbench
		</h1>
		<span class="text-xs text-slate-500">Signal Monitoring & Analysis</span>
		<div class="ml-auto flex items-center gap-3">
			<span class="text-xs text-slate-400" id="sdr-status">
				{#if totalSdrs === 0}
					SDR: no devices
				{:else if activeSdrs > 0}
					SDR: {activeSdrs}/{totalSdrs} streaming
				{:else}
					SDR: {totalSdrs} idle
				{/if}
			</span>
			<span class="text-xs text-slate-400" id="signal-count">{signalCount} signals</span>
		</div>
	</header>

	<!-- Main content -->
	<main class="flex-1 flex overflow-hidden">
		{@render children()}
	</main>
</div>