<script lang="ts">
	// §3.2 app shell top bar: brand, view tabs, connection pill, health
	// chips, ⚙ menu (setup + shortcuts + first-run re-arm, §11).
	import { page } from '$app/state';
	import ConnectionPill from './ConnectionPill.svelte';
	import HealthChips from './HealthChips.svelte';
	import { shortcutsOpen } from '$lib/stores/ui';
	import { completeSetup } from '$lib/api/client';
	import { popoverDismiss } from '$lib/ui/actions';

	const NAV = [
		{ href: '/', label: 'Operations' },
		{ href: '/spectrum', label: 'Spectrum' },
		{ href: '/signals', label: 'Signals' },
		{ href: '/recordings', label: 'Recordings' },
		{ href: '/analysis', label: 'Analysis' },
		{ href: '/setup', label: 'Setup' }
	];

	let menuOpen = $state(false);
	const path = $derived(page.url.pathname);

	function isActive(href: string): boolean {
		return href === '/' ? path === '/' : path.startsWith(href);
	}

	async function rearmFirstRun(): Promise<void> {
		// §11: re-arming the first-run banner is the existing
		// completeSetup(true) affordance, restyled as a quiet link.
		try {
			await completeSetup(true);
		} catch {
			// best-effort; the wizard's own probe reports failures
		}
		menuOpen = false;
	}
</script>

<header class="flex h-12 shrink-0 items-center gap-4 border-b border-slate-700 bg-slate-900 px-4">
	<h1 class="text-lg font-bold tracking-tight">
		<span class="text-sky-400">SIGINT</span> Workbench
	</h1>
	<nav aria-label="Views" class="flex items-center gap-0.5 text-sm">
		{#each NAV as item (item.href)}
			<a
				href={item.href}
				aria-current={isActive(item.href) ? 'page' : undefined}
				class="rounded px-2 py-1 transition-colors
					{isActive(item.href)
					? 'text-sky-300 underline decoration-sky-400 underline-offset-4'
					: 'text-slate-400 hover:text-slate-200'}"
			>
				{item.label}
			</a>
		{/each}
	</nav>

	<div class="ml-auto flex items-center gap-3">
		<ConnectionPill />
		<HealthChips />
		<div class="relative" use:popoverDismiss={{ open: menuOpen, onDismiss: () => (menuOpen = false) }}>
			<button
				class="rounded px-2 py-1 text-slate-400 hover:bg-slate-800 hover:text-slate-200"
				aria-label="Settings menu"
				aria-expanded={menuOpen}
				onclick={() => (menuOpen = !menuOpen)}
			>⚙</button>
			{#if menuOpen}
				<div class="absolute right-0 top-9 z-40 w-56 rounded border border-slate-700 bg-slate-900 p-2 text-xs shadow-xl">
					<a href="/setup" class="block rounded px-2 py-1.5 text-slate-300 hover:bg-slate-800" onclick={() => (menuOpen = false)}>
						Setup wizard…
					</a>
					<button
						class="block w-full rounded px-2 py-1.5 text-left text-slate-300 hover:bg-slate-800"
						onclick={() => {
							menuOpen = false;
							shortcutsOpen.set(true);
						}}
					>
						Keyboard shortcuts <span class="font-mono text-slate-500">?</span>
					</button>
					<button
						class="block w-full rounded px-2 py-1.5 text-left text-slate-500 hover:bg-slate-800 hover:text-slate-300"
						onclick={rearmFirstRun}
					>
						Show first-run banner again
					</button>
				</div>
			{/if}
		</div>
	</div>
</header>
