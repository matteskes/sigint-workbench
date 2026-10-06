<script lang="ts">
	// §15: the `?` overlay — every shortcut the app implements, listed.
	import { shortcutsOpen } from '$lib/stores/ui';

	const SHORTCUTS: [string, string][] = [
		['1…6', 'switch view (Operations, Spectrum, Signals, Recordings, Analysis, Setup)'],
		['/', 'focus the signal search input'],
		['j / k · ↓ / ↑', 'next / previous signal in the current sort order'],
		['Enter', 'open the inspector for the highlighted row'],
		['Esc', 'clear selection · close overlay · close popover'],
		['Space', 'play / pause the live audio stream'],
		['s', 'park / resume sweep on the focused receiver card']
	];
</script>

{#if $shortcutsOpen}
	<div
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
		role="presentation"
		onclick={(e) => {
			if (e.target === e.currentTarget) shortcutsOpen.set(false);
		}}
		onkeydown={() => {}}
	>
		<div class="w-[26rem] rounded border border-slate-700 bg-slate-900 p-4 shadow-2xl" role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" tabindex="-1">
			<div class="mb-3 flex items-center justify-between">
				<div class="text-xs font-semibold uppercase tracking-wide text-slate-400">Keyboard shortcuts</div>
				<button class="text-xs text-slate-400 hover:text-slate-200" onclick={() => shortcutsOpen.set(false)}>
					Esc
				</button>
			</div>
			<dl class="space-y-2 text-xs">
				{#each SHORTCUTS as [key, action] (key)}
					<div class="flex gap-3">
						<dt class="w-28 shrink-0 font-mono text-sky-300">{key}</dt>
						<dd class="text-slate-300">{action}</dd>
					</div>
				{/each}
			</dl>
		</div>
	</div>
{/if}
