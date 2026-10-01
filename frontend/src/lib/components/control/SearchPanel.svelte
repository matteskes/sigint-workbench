<script lang="ts">
	import { signals } from '$lib/stores/signals';

	let searchQuery = $state('');
	let filterClass = $state('all');

	const classes = [
		'all', 'aviation', 'land_mobile', 'marine',
		'broadcast', 'amateur', 'gnss', 'wifi'
	];

	let filtered = $derived(
		$signals.filter((s) => {
			const matchesClass = filterClass === 'all' || s.class === filterClass;
			const q = searchQuery.toLowerCase();
			const matchesQuery =
				!q ||
				s.class.toLowerCase().includes(q) ||
				s.modulation.toLowerCase().includes(q) ||
				(s.freqHz / 1e6).toFixed(1).includes(q);
			return matchesClass && matchesQuery;
		})
	);
</script>

<div class="p-3 space-y-2">
	<input
		bind:value={searchQuery}
		placeholder="Search signals..."
		class="w-full px-3 py-1.5 text-xs bg-slate-800 border border-slate-700 rounded
			text-slate-100 placeholder-slate-500 focus:outline-none focus:border-blue-500"
	/>
	<div class="flex flex-wrap gap-1">
		{#each classes as cls}
			<button
				class="text-[10px] px-2 py-0.5 rounded-full
					{filterClass === cls
						? 'bg-blue-900 text-blue-300'
						: 'bg-slate-800 text-slate-400 hover:bg-slate-700'}"
				onclick={() => (filterClass = cls)}
			>
				{cls}
			</button>
		{/each}
	</div>
</div>