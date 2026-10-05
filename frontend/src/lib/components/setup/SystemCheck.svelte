<script lang="ts">
	import type { SetupStatus } from '$lib/api/client';

	let { status }: { status: SetupStatus | null } = $props();

	const labels: Record<string, string> = {
		db: 'Database',
		'ws-hub': 'Event hub (ws-hub)',
		recorder: 'Recorder',
		capture: 'SDR capture'
	};
</script>

<div class="rounded border border-slate-700 bg-slate-800/50 p-4">
	<h2 class="text-sm font-semibold text-slate-200">System check</h2>
	{#if !status}
		<p class="mt-2 text-xs text-slate-500">Probing…</p>
	{:else}
		<ul class="mt-2 space-y-1 text-sm">
			{#each Object.entries(status.components) as [name, c]}
				<li class="flex items-center gap-2">
					<span class={c.ok ? 'text-green-400' : 'text-red-400'}>●</span>
					<span class="text-slate-300">{labels[name] ?? name}</span>
					{#if !c.ok}<span class="text-xs text-red-300">{c.detail ?? 'unreachable'}</span>{/if}
				</li>
			{/each}
		</ul>
		{#if !status.config_dir_writable}
			<p class="mt-2 text-xs text-red-300">
				Config directory is not writable — saving settings will fail
				(the gateway needs the §20 rw config mount).
			</p>
		{/if}
	{/if}
</div>
