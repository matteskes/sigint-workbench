<script lang="ts">
	import type { Signal } from '$lib/stores/signals';
	import VUMeter from './VUMeter.svelte';
	import { audioState } from '$lib/stores/audio';

	let { signal }: { signal: Signal } = $props();
	let audioCtx: AudioContext | undefined;
	let sourceNode: AudioBufferSourceNode | undefined;
	let analyser: AnalyserNode | undefined;
	let animFrame = 0;

	async function togglePlayback() {
		if ($audioState.playing && $audioState.signalId === signal.id) {
			stopPlayback();
			return;
		}

		try {
			if (!audioCtx) {
				audioCtx = new AudioContext();
			}
			if (audioCtx.state === 'suspended') {
				await audioCtx.resume();
			}

			const res = await fetch(`/api/recordings/${signal.id}/audio`);
			if (!res.ok) throw new Error('No recording available');

			const arrayBuf = await res.arrayBuffer();
		 const audioBuffer = await audioCtx.decodeAudioData(arrayBuf);

			sourceNode = audioCtx.createBufferSource();
			sourceNode.buffer = audioBuffer;
			analyser = audioCtx.createAnalyser();
			analyser.fftSize = 256;

			sourceNode.connect(analyser);
			analyser.connect(audioCtx.destination);
			sourceNode.start();

			sourceNode.onended = () => {
				audioState.update((s) => ({ ...s, playing: false }));
			};

			audioState.update((s) => ({ ...s, playing: true, signalId: signal.id }));
			updateLevel();
		} catch (e) {
			console.error('Audio playback error:', e);
		}
	}

	function stopPlayback() {
		sourceNode?.stop();
		sourceNode = undefined;
		audioState.update((s) => ({ ...s, playing: false, level: 0 }));
		cancelAnimationFrame(animFrame);
	}

	function updateLevel() {
		if (!analyser) return;
		const data = new Float32Array(analyser.fftSize);
		analyser.getFloatTimeDomainData(data);
		let sum = 0;
		for (let i = 0; i < data.length; i++) sum += data[i] * data[i];
		const rms = Math.sqrt(sum / data.length);
		audioState.update((s) => ({ ...s, level: rms }));
		animFrame = requestAnimationFrame(updateLevel);
	}
</script>

<div class="space-y-2">
	<div class="text-xs font-semibold text-slate-400">Audio</div>

	<button
		class="w-full py-2 rounded text-sm font-medium
			{$audioState.playing && $audioState.signalId === signal.id
				? 'bg-red-900/50 text-red-400 hover:bg-red-900'
				: 'bg-blue-900/50 text-blue-400 hover:bg-blue-900'}"
		onclick={togglePlayback}
	>
		{#if $audioState.playing && $audioState.signalId === signal.id}
			■ Stop
		{:else}
			▶ Play
		{/if}
	</button>

	<VUMeter level={$audioState.level} />
</div>