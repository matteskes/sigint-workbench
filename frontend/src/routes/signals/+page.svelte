<script lang="ts">
	// §8 Signals view: the same triage table as Operations, full-width,
	// with every column plus the inspector as an overlay drawer when a
	// signal is selected (§3.3: the drawer pattern for narrow stages).
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import SignalTable from '$lib/components/signals/SignalTable.svelte';
	import Inspector from '$lib/components/inspector/Inspector.svelte';
	import { selectedSignal } from '$lib/stores/signals';
	import { inspectorOpen } from '$lib/stores/ui';
	import { fetchSignal } from '$lib/api/client';

	// ?signal=<id> deep link (§3.1) — soft-fail on stale ids.
	onMount(() => {
		const id = page.url.searchParams.get('signal');
		if (!id) return;
		fetchSignal(id)
			.then((s) => selectedSignal.update((cur) => cur ?? s))
			.catch(() => {
				// unknown id — no fake selection
			});
	});
</script>

<div class="relative min-w-0 flex-1">
	<div class="flex h-full flex-col">
		<SignalTable full />
	</div>

	<!-- Inspector as an overlay drawer (§3.3) -->
	{#if $selectedSignal && $inspectorOpen}
		<div class="absolute inset-y-0 right-0 z-20 w-80 overflow-y-auto border-l border-slate-700 bg-slate-900 shadow-2xl">
			<Inspector signal={$selectedSignal} />
		</div>
	{/if}
</div>
