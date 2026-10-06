<script lang="ts">
	// §3.2/§14: the truth about /ws. A dead hub renders as amber/red
	// here within one close event — never as an empty band (§1 audit).
	import { onMount } from 'svelte';
	import { connectionState, connectionAttempt, lastEventAt } from '$lib/stores/connection';

	// "last event Xs ago" ticks once per second while the pill is live.
	let now = $state(Date.now());
	onMount(() => {
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	const ageS = $derived(
		$lastEventAt > 0 ? Math.max(0, Math.round((now - $lastEventAt) / 1000)) : null
	);
</script>

<span
	class="inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs"
	role="status"
	aria-live="polite"
>
	{#if $connectionState === 'live'}
		<span class="flex items-center gap-1.5 rounded bg-green-900/50 px-2 py-1 text-green-300">
			<span class="h-1.5 w-1.5 rounded-full bg-green-400"></span>
			live{ageS !== null ? ` · ${ageS}s` : ''}
		</span>
	{:else if $connectionState === 'reconnecting'}
		<span class="flex items-center gap-1.5 rounded bg-amber-900/50 px-2 py-1 text-amber-300" title="The event stream dropped — retrying with backoff; state re-syncs from REST on reconnect">
			<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400"></span>
			reconnecting{$connectionAttempt > 0 ? ` (${$connectionAttempt})` : ''}
		</span>
	{:else if $connectionState === 'connecting'}
		<span class="flex items-center gap-1.5 rounded bg-slate-800 px-2 py-1 text-slate-400">
			<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-slate-500"></span>
			connecting…
		</span>
	{:else}
		<span class="flex items-center gap-1.5 rounded bg-red-900/50 px-2 py-1 text-red-300">
			<span class="h-1.5 w-1.5 rounded-full bg-red-400"></span>
			offline — retrying
		</span>
	{/if}
</span>
