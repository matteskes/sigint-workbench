<script lang="ts">
	import { fetchAnnotations, addAnnotation } from '$lib/api/client';
	import type { Annotation } from '$lib/api/client';
	import type { Signal } from '$lib/stores/signals';
	import AudioPlayer from '../audio/AudioPlayer.svelte';

	let { signal }: { signal: Signal } = $props();

	// Signal notes (§12.5 annotations). Reloaded whenever another
	// signal is selected; the cancellation guard drops stale responses.
	let notes = $state<Annotation[]>([]);
	let noteText = $state('');
	let saving = $state(false);

	$effect(() => {
		const id = signal.id;
		let cancelled = false;
		notes = [];
		noteText = '';
		fetchAnnotations(id)
			.then((list) => {
				if (!cancelled) notes = list;
			})
			.catch(() => {
				if (!cancelled) notes = [];
			});
		return () => {
			cancelled = true;
		};
	});

	async function submitNote(): Promise<void> {
		const text = noteText.trim();
		if (!text || saving) return;
		saving = true;
		try {
			const created = await addAnnotation(signal.id, text);
			notes = [created, ...notes];
			noteText = '';
		} catch {
			// Keep the text so the note can be retried.
		} finally {
			saving = false;
		}
	}

	function freqMHz(hz: number): string {
		if (hz >= 1e9) return `${(hz / 1e9).toFixed(3)} GHz`;
		if (hz >= 1e6) return `${(hz / 1e6).toFixed(3)} MHz`;
		return `${(hz / 1e3).toFixed(1)} kHz`;
	}

	function bandwidthStr(bw: number): string {
		if (bw >= 1e6) return `${(bw / 1e6).toFixed(1)} MHz`;
		if (bw >= 1e3) return `${(bw / 1e3).toFixed(1)} kHz`;
		return `${bw} Hz`;
	}
</script>

<div class="p-3 space-y-4">
	<h2 class="text-sm font-semibold text-slate-300">Signal Detail</h2>

	<!-- Frequency -->
	<div>
		<div class="text-xs text-slate-500">Frequency</div>
		<div class="text-lg font-mono text-white">{freqMHz(signal.freqHz)}</div>
	</div>

	<!-- Classification -->
	<div class="grid grid-cols-2 gap-2">
		<div>
			<div class="text-xs text-slate-500">Modulation</div>
			<div class="text-sm">{signal.modulation} {signal.subType && `(${signal.subType})`}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Class</div>
			<div class="text-sm capitalize">{signal.class}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Bandwidth</div>
			<div class="text-sm font-mono">{bandwidthStr(signal.bandwidthHz)}</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Confidence</div>
			<div class="text-sm font-mono">{(signal.confidence * 100).toFixed(0)}%</div>
		</div>
	</div>

	<!-- Power (§5.6: "dBm" only for calibrated SDRs) -->
	<div>
		<div class="text-xs text-slate-500">Power</div>
		<div class="text-sm font-mono">
			{signal.powerDbm?.toFixed(1)} {signal.powerCalibrated ? 'dBm' : 'dB (rel.)'}
		</div>
	</div>

	<!-- Location -->
	<div>
		<div class="text-xs text-slate-500">Location</div>
		{#if signal.lat != null && signal.lon != null}
			<div class="text-sm font-mono">
				{signal.lat.toFixed(4)}, {signal.lon.toFixed(4)}
			</div>
			<div class="text-xs text-slate-500">±{signal.accuracyM.toFixed(0)} m</div>
		{:else}
			<div class="text-sm text-slate-500">Unlocated</div>
		{/if}
	</div>

	<!-- Timing -->
	<div class="grid grid-cols-2 gap-2">
		<div>
			<div class="text-xs text-slate-500">First Seen</div>
			<div class="text-xs font-mono">
				{new Date(signal.firstSeen).toLocaleTimeString()}
			</div>
		</div>
		<div>
			<div class="text-xs text-slate-500">Last Seen</div>
			<div class="text-xs font-mono">
				{new Date(signal.lastSeen).toLocaleTimeString()}
			</div>
		</div>
	</div>

	<!-- SDR -->
	<div>
		<div class="text-xs text-slate-500">Source SDR</div>
		<div class="text-sm">{signal.sdrId}</div>
	</div>

	<!-- Status -->
	<div class="flex items-center gap-2">
		{#if signal.verified}
			<span class="text-xs px-2 py-0.5 bg-green-900/50 text-green-400 rounded">
				✓ Verified (2 SDRs)
			</span>
		{:else}
			<span class="text-xs px-2 py-0.5 bg-amber-900/50 text-amber-400 rounded">
				Unverified
			</span>
		{/if}
	</div>

	<!-- Audio -->
	<div class="border-t border-slate-700 pt-3">
		<AudioPlayer signal={signal} />
	</div>

	<!-- Notes (§12.5 annotations) -->
	<div class="border-t border-slate-700 pt-3">
		<div class="text-xs text-slate-500">Notes</div>
		{#if notes.length === 0}
			<div class="text-sm text-slate-500">No notes yet</div>
		{:else}
			<ul class="mt-1 space-y-2">
				{#each notes as note (note.id)}
					<li>
						<div class="text-sm text-slate-200">{note.userNote}</div>
						<div class="text-xs text-slate-500">{new Date(note.createdAt).toLocaleString()}</div>
					</li>
				{/each}
			</ul>
		{/if}
		<form
			class="mt-3 flex gap-2"
			onsubmit={(e) => {
				e.preventDefault();
				submitNote();
			}}
		>
			<input
				class="flex-1 rounded bg-slate-800 px-2 py-1 text-sm text-slate-200 placeholder-slate-500 outline-none focus:ring-1 focus:ring-sky-500"
				placeholder="Add a note…"
				bind:value={noteText}
			/>
			<button
				type="submit"
				class="rounded bg-sky-700 px-2 py-1 text-sm text-white disabled:opacity-40"
				disabled={!noteText.trim() || saving}
			>
				{saving ? '…' : 'Add'}
			</button>
		</form>
	</div>
</div>