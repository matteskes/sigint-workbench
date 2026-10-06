<script lang="ts">
	// §9/§6: WAV playback for one recording via the gateway's
	// /api/recordings/{id}/audio (A3). Recording-scoped — the old
	// version passed a signal id into the recordings path and could
	// never find a row; the library and inspector play real rows now.
	// IQ recordings get no play button (§9: honest about formats).
	import type { Recording } from '$lib/api/client';
	import { recordingAudioUrl } from '$lib/api/client';
	import { audioState, claimWavPlayback, releaseWavPlayback } from '$lib/stores/audio';
	import VUMeter from './VUMeter.svelte';

	let { recording }: { recording: Recording } = $props();

	let audioCtx: AudioContext | undefined;
	let sourceNode: AudioBufferSourceNode | undefined;
	let analyser: AnalyserNode | undefined;
	let animFrame = 0;
	let error = $state('');

	const isOurs = $derived($audioState.playing && $audioState.signalId === recording.id);

	function updateLevel(): void {
		if (!analyser) return;
		const data = new Float32Array(analyser.fftSize);
		analyser.getFloatTimeDomainData(data);
		let sum = 0;
		for (let i = 0; i < data.length; i++) sum += data[i] * data[i];
		const rms = Math.sqrt(sum / data.length);
		audioState.update((s) => ({ ...s, level: rms }));
		animFrame = requestAnimationFrame(updateLevel);
	}

	function stopPlayback(): void {
		sourceNode?.stop();
		sourceNode = undefined;
		cancelAnimationFrame(animFrame);
		releaseWavPlayback(recording.id);
	}

	async function togglePlayback(): Promise<void> {
		if (isOurs) {
			stopPlayback();
			return;
		}
		error = '';
		try {
			if (!audioCtx) audioCtx = new AudioContext();
			if (audioCtx.state === 'suspended') await audioCtx.resume();

			// F4: one stream at a time — claiming stops any other row.
			const res = await fetch(recordingAudioUrl(recording.id));
			if (!res.ok) throw new Error(`playback failed (recorder ${res.status})`);
			const arrayBuf = await res.arrayBuffer();
			const audioBuffer = await audioCtx.decodeAudioData(arrayBuf);

			sourceNode = audioCtx.createBufferSource();
			sourceNode.buffer = audioBuffer;
			analyser = audioCtx.createAnalyser();
			analyser.fftSize = 256;
			sourceNode.connect(analyser);
			analyser.connect(audioCtx.destination);

			claimWavPlayback(recording.id, () => {
				// Called when another recording claims playback.
				sourceNode?.stop();
				sourceNode = undefined;
				cancelAnimationFrame(animFrame);
			});
			sourceNode.onended = () => stopPlayback();
			sourceNode.start();
			updateLevel();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			releaseWavPlayback(recording.id);
		}
	}
</script>

<div class="space-y-1">
	<button
		class="w-full rounded py-1 text-xs font-medium
			{isOurs
			? 'bg-red-900/50 text-red-300 hover:bg-red-900'
			: 'bg-sky-900/50 text-sky-300 hover:bg-sky-900'}"
		onclick={togglePlayback}
	>
		{isOurs ? '■ Stop' : '▶ Play'}
	</button>
	{#if isOurs}
		<VUMeter level={$audioState.level} />
	{/if}
	{#if error}
		<div class="text-[10px] text-red-400">{error}</div>
	{/if}
</div>
