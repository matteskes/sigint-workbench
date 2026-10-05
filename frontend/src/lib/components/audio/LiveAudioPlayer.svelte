<script lang="ts">
	import { LiveOpusPlayer, type LiveState } from '$lib/audio/liveOpus';
	import VUMeter from './VUMeter.svelte';
	import type { Signal } from '$lib/stores/signals';

	let { signal }: { signal: Signal } = $props();

	// §10.4 lifecycle: idle → connecting → live → ended (close 1000 =
	// the stream is over: session finalized, or not demodulated). Side
	// states: error, and unsupported (no WebCodecs AudioDecoder in this
	// browser — feature-detected on start; recordings still play).
	// (Named liveState: `state` collides with svelte2tsx's $state
	// rune transform.)
	let liveState = $state<LiveState>('idle');
	let detail = $state('');
	let level = $state(0);

	// Non-reactive on purpose: never rendered directly.
	let audioCtx: AudioContext | undefined;
	let player: LiveOpusPlayer | undefined;

	async function toggle(): Promise<void> {
		if (liveState === 'live' || liveState === 'connecting') {
			player?.stop();
			return;
		}
		if (liveState === 'unsupported') return;
		try {
			if (!audioCtx) audioCtx = new AudioContext();
			if (audioCtx.state === 'suspended') await audioCtx.resume();
			player?.stop(); // §10.4: one live stream at a time
			player = new LiveOpusPlayer(signal.id, audioCtx, {
				onState: (s, d) => {
					liveState = s;
					detail = d ?? '';
					if (s !== 'live') level = 0;
				},
				onLevel: (l) => (level = l)
			});
			player.start();
		} catch (e) {
			liveState = 'error';
			detail = String(e);
		}
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<div class="text-xs font-semibold text-slate-400">Live Audio</div>
		{#if liveState === 'live'}
			<span class="text-xs text-green-400">● live</span>
		{:else if liveState === 'connecting'}
			<span class="text-xs text-amber-400">connecting…</span>
		{:else if liveState === 'unsupported'}
			<span class="text-xs text-slate-500" title="Browser lacks WebCodecs AudioDecoder">
				unsupported
			</span>
		{/if}
	</div>

	<button
		class="w-full py-2 rounded text-sm font-medium
			{liveState === 'live' || liveState === 'connecting'
				? 'bg-red-900/50 text-red-400 hover:bg-red-900'
				: liveState === 'unsupported'
					? 'bg-slate-800 text-slate-500 cursor-not-allowed'
					: 'bg-blue-900/50 text-blue-400 hover:bg-blue-900'}"
		onclick={toggle}
		disabled={liveState === 'unsupported'}
	>
		{#if liveState === 'live' || liveState === 'connecting'}
			■ Stop live
		{:else if liveState === 'unsupported'}
			▶ Live (unsupported)
		{:else if liveState === 'error'}
			▶ Live (retry)
		{:else}
			▶ Live
		{/if}
	</button>

	{#if liveState === 'ended'}
		<div class="text-xs text-slate-500">Stream ended (recorder closed it)</div>
	{:else if liveState === 'error'}
		<div class="text-xs text-red-400">Live stream error{detail ? `: ${detail}` : ''}</div>
	{/if}

	<VUMeter level={level} />
</div>