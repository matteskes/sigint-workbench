<script lang="ts">
	// §3.2/§14: service health chips from GET /api/setup/status (the
	// documented §13 fan-out exception), polled every 30 s by the
	// health store. Red dot = down; the probe's detail string is on
	// hover and in the popover — a red chip is text-explained, never
	// color-only (§16).
	import { onMount } from 'svelte';
	import { health, HEALTH_POLL_MS, type ComponentStatus } from '$lib/stores/health';
	import { relTime } from '$lib/ui/format';

	const ORDER = ['db', 'ws-hub', 'recorder', 'capture'];

	let open = $state(false);
	let now = $state(Date.now());
	onMount(() => {
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	const chips = $derived.by(() => {
		const names = Object.keys($health.components).sort(
			(a, b) => ORDER.indexOf(a) - ORDER.indexOf(b) || a.localeCompare(b)
		);
		return names.map((name) => ({ name, status: $health.components[name] as ComponentStatus }));
	});

	function chipTitle(name: string, s: ComponentStatus): string {
		const base = s.ok ? `${name}: ok` : `${name}: down${s.detail ? ` — ${s.detail}` : ''}`;
		return base;
	}
</script>

<div class="relative">
	<button
		class="flex items-center gap-2 rounded px-1.5 py-1 hover:bg-slate-800"
		aria-label="Service health"
		aria-expanded={open}
		onclick={() => (open = !open)}
	>
		{#each chips as c (c.name)}
			<span class="flex items-center gap-1 text-[10px] text-slate-400" title={chipTitle(c.name, c.status)}>
				<span class="h-1.5 w-1.5 rounded-full {c.status.ok ? 'bg-green-400' : 'bg-red-400'}"></span>{c.name}
			</span>
		{/each}
		{#if !$health.configDirWritable}
			<span class="flex items-center gap-1 text-[10px] text-slate-400" title="config directory not writable">
				<span class="h-1.5 w-1.5 rounded-full bg-red-400"></span>cfg
			</span>
		{/if}
	</button>

	{#if open}
		<div class="absolute right-0 top-8 z-40 w-72 rounded border border-slate-700 bg-slate-900 p-3 text-xs shadow-xl">
			<div class="mb-2 text-[10px] uppercase tracking-wide text-slate-500">Service health</div>
			<ul class="space-y-1.5">
				{#each chips as c (c.name)}
					<li class="flex items-center justify-between gap-2">
						<span class="flex items-center gap-1.5 text-slate-300">
							<span class="h-1.5 w-1.5 rounded-full {c.status.ok ? 'bg-green-400' : 'bg-red-400'}"></span>
							{c.name}
						</span>
						<span class="text-right {c.status.ok ? 'text-green-400' : 'text-red-400'}">
							{c.status.ok ? 'ok' : `down${c.status.detail ? ` — ${c.status.detail}` : ''}`}
						</span>
					</li>
				{:else}
					<li class="text-slate-500">no probe data yet…</li>
				{/each}
				{#if !$health.configDirWritable}
					<li class="flex items-center justify-between text-red-400">
						<span>config dir</span><span>not writable</span>
					</li>
				{/if}
			</ul>
			<div class="mt-2 border-t border-slate-800 pt-2 text-[10px] text-slate-500">
				{#if $health.probeError}
					<span class="text-amber-400">{$health.probeError} — showing last known state ·</span>
				{/if}
				{#if $health.checkedAt > 0}
					checked {relTime(new Date($health.checkedAt).toISOString(), now)}
				{:else}
					not checked yet
				{/if}
				· every {HEALTH_POLL_MS / 1000}s ·
				<a href="/setup" class="text-sky-400 hover:underline">setup →</a>
			</div>
		</div>
	{/if}
</div>
