<script lang="ts">
	// §3.2/§20.4–20.5: the first-run banner moved from +page.svelte into
	// the shell so it shows on every view. Probe failures leave it
	// hidden (best-effort, as before).
	import { onMount } from 'svelte';
	import { fetchSetupState, completeSetup } from '$lib/api/client';

	let firstRun = $state(false);

	onMount(() => {
		fetchSetupState()
			.then((s) => (firstRun = s.first_run))
			.catch(() => {
				// banner is best-effort; ignore probe failures
			});
	});
</script>

{#if firstRun}
	<div class="flex items-center gap-3 border-b border-blue-800 bg-blue-950/60 px-4 py-2 text-sm" role="status">
		<span class="text-blue-200">First run: configure receivers and processing in the setup wizard.</span>
		<a href="/setup" class="rounded bg-blue-700 px-2 py-0.5 text-xs font-medium hover:bg-blue-600">Open setup</a>
		<button
			class="text-xs text-slate-400 hover:text-slate-200"
			onclick={() => completeSetup(false).then(() => (firstRun = false))}
		>Dismiss</button>
	</div>
{/if}
