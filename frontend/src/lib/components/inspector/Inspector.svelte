<script lang="ts">
	// §6: the shared right rail. Reorganizes the old SignalDetail into
	// collapsible sections reordered by frequency of use: identity →
	// live audio → location → recordings → actions → notes. The §19
	// inline TFR expansion is gone — recordings route to the full-size
	// /analysis view (§10). Deep links mirror into ?signal=<id> (§3.1).
	import { goto } from '$app/navigation';
	import {
		fetchAnnotations,
		addAnnotation,
		fetchTrack,
		fetchRecordings,
		recordingAudioUrl
	} from '$lib/api/client';
	import type { Annotation, Recording } from '$lib/api/client';
	import type { Signal } from '$lib/stores/signals';
	import { signalLevels } from '$lib/stores/audio';
	import { tracks, setTrack } from '$lib/stores/tracks';
import { tdoaResults } from '$lib/stores/tdoa';
	import { selectedSpectrum } from '$lib/stores/spectrum';
	import { markedFreqHz } from '$lib/stores/spectrum';
	import { requestMapCenter } from '$lib/stores/ui';
	import LiveAudioPlayer from '../audio/LiveAudioPlayer.svelte';
	import AudioPlayer from '../audio/AudioPlayer.svelte';
	import VUMeter from '../audio/VUMeter.svelte';
	import ClassChip from '../ui/ClassChip.svelte';
	import StatusPill from '../ui/StatusPill.svelte';
	import { freqHz, bandwidthHz, powerDb, relTime, dateFull, rateSps } from '$lib/ui/format';

	let { signal }: { signal: Signal } = $props();

	// ── ?signal=<id> deep-link mirror (§3.1) ──
	$effect(() => {
		const id = signal.id;
		try {
			const url = new URL(window.location.href);
			if (id) url.searchParams.set('signal', id);
			else url.searchParams.delete('signal');
			window.history.replaceState(null, '', url);
		} catch {
			// history unavailable (embeddings) — cosmetic only
		}
	});

	// ── Signal notes (§12.5 annotations), with the existing
	// cancellation guard per selection ──
	let notes = $state<Annotation[]>([]);
	let noteText = $state('');
	let saving = $state(false);
	let notesError = $state('');

	$effect(() => {
		const id = signal.id;
		let cancelled = false;
		notes = [];
		noteText = '';
		notesError = '';
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
		notesError = '';
		try {
			const created = await addAnnotation(signal.id, text);
			notes = [created, ...notes];
			noteText = '';
		} catch (e) {
			// Keep the text so the note can be retried (§6).
			notesError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	// ── Recordings (§12.3): fetched on first expand + manual refresh —
	// no WS event exists for new recordings (§9/F7) ──
	let recordings = $state<Recording[]>([]);
	let recsLoaded = $state(false);
	let recsLoading = $state(false);
	let recsError = $state('');
	let playId = $state<string | null>(null);

	$effect(() => {
		// Reset per selection; loads happen on expand/refresh.
		recordings = [];
		recsLoaded = false;
		recsError = '';
		playId = null;
	});

	async function loadRecordings(): Promise<void> {
		recsLoading = true;
		recsError = '';
		try {
			recordings = await fetchRecordings({ signalId: signal.id, limit: 20 });
			recsLoaded = true;
		} catch (e) {
			recsError = e instanceof Error ? e.message : String(e);
		} finally {
			recsLoading = false;
		}
	}

	function onRecsToggle(e: Event): void {
		const open = (e.currentTarget as HTMLDetailsElement).open;
		if (open && !recsLoaded && !recsLoading) void loadRecordings();
	}

	// ── Track (§9.4): persisted path + movement, same guard as before ──
	$effect(() => {
		const id = signal.id;
		let cancelled = false;
		fetchTrack(id)
			.then((t) => {
				if (!cancelled) setTrack(t);
			})
			.catch(() => {
				// No track for this signal — fine.
			});
		return () => {
			cancelled = true;
		};
	});

	// ── Actions (§6) ──
	let copiedId = $state(false);
	async function copyId(): Promise<void> {
		try {
			await navigator.clipboard.writeText(signal.id);
			copiedId = true;
			setTimeout(() => (copiedId = false), 2000);
		} catch {
			// clipboard unavailable — non-essential affordance
		}
	}

	const placed = $derived(signal.lat != null && signal.lon != null);

	const inSpectrumSpan = $derived.by(() => {
		const st = $selectedSpectrum;
		if (!st) return false;
		const half = st.latest.sampleRate / 2;
		return Math.abs(signal.freqHz - st.latest.freqHz) <= half;
	});

	async function openSpectrumSpan(): Promise<void> {
		if (!inSpectrumSpan) return;
		markedFreqHz.set(signal.freqHz);
		await goto('/spectrum');
	}

	function analyze(rec: Recording): void {
		void goto(`/analysis?recording=${encodeURIComponent(rec.id)}&signal=${encodeURIComponent(signal.id)}`);
	}

	const level = $derived($signalLevels[signal.id]?.level ?? null);

	// §9.6/§14.2: the latest TDOA attempt for this signal (last event
	// wins) — renders the TDOA section below; MapView draws the locus.
	const tdoa = $derived($tdoaResults[signal.id]);

	function fmtM(m: number): string {
		return m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${m.toFixed(0)} m`;
	}
</script>

<div class="space-y-3 p-3">
	<!-- ── Identity (always open) ── -->
	<section aria-label="Signal identity">
		<div class="flex items-baseline justify-between gap-2">
			<div class="font-mono text-lg text-white">{freqHz(signal.freqHz)}</div>
			{#if signal.verified}
				<StatusPill tone="ok" label="✓ verified" title="Two-receiver verification (§8)" />
			{/if}
		</div>
		<div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-slate-400">
			<ClassChip cls={signal.class} />
			<span>{signal.modulation}{signal.subType ? ` (${signal.subType})` : ''}</span>
			<span class="font-mono">{bandwidthHz(signal.bandwidthHz)}</span>
			<span>conf {signal.confidence.toFixed(2)}</span>
		</div>
		<div class="mt-1.5 grid grid-cols-2 gap-x-2 gap-y-1 text-xs">
			<div>
				<span class="text-slate-500">power </span>
				<span class="font-mono" title="§5.6: unit reflects receiver calibration">
					{powerDb(signal.powerDbm, signal.powerCalibrated).value}
					{powerDb(signal.powerDbm, signal.powerCalibrated).unit}
				</span>
			</div>
			<div>
				<span class="text-slate-500">source </span>
				<span class="font-mono">{signal.sdrId}</span>
			</div>
			<div class="col-span-2" title={dateFull(signal.lastSeen)}>
				<span class="text-slate-500">seen </span>
				<span>{relTime(signal.lastSeen)}</span>
				<span class="text-slate-500"> · first {relTime(signal.firstSeen)}</span>
			</div>
		</div>
		{#if level !== null}
			<div class="mt-1.5 flex items-center gap-2">
				<span class="text-[10px] text-slate-500">live level</span>
				<div class="h-1 w-24 overflow-hidden rounded bg-slate-800">
					<div class="h-full bg-green-400" style="width: {Math.round(level * 100)}%"></div>
				</div>
			</div>
		{/if}
	</section>

	<!-- ── Live audio (§10.4, promoted to second position) ── -->
	<section class="border-t border-slate-700 pt-3" aria-label="Live audio">
		<LiveAudioPlayer {signal} />
	</section>

	<!-- ── Location (§9.2/§9.3: honest about the unplaced) ── -->
	<details class="border-t border-slate-700 pt-3" open={placed}>
		<summary class="cursor-pointer text-xs font-semibold uppercase tracking-wide text-slate-500">
			Location
		</summary>
		<div class="mt-2">
			{#if placed}
				<div class="font-mono text-sm">
					{signal.lat!.toFixed(5)}, {signal.lon!.toFixed(5)}
					<span class="text-xs text-slate-500">±{signal.accuracyM} m</span>
				</div>
				{#if $tracks[signal.id]}
					<div class="mt-2 grid grid-cols-3 gap-2">
						<div>
							<div class="text-[10px] text-slate-500">Speed</div>
							<div class="font-mono text-sm">{$tracks[signal.id].speedKmh.toFixed(1)} km/h</div>
						</div>
						<div>
							<div class="text-[10px] text-slate-500">Heading</div>
							<div class="font-mono text-sm">{$tracks[signal.id].headingDeg.toFixed(0)}°</div>
						</div>
						<div>
							<div class="text-[10px] text-slate-500">Status</div>
							<StatusPill
								tone={$tracks[signal.id].isMoving ? 'ok' : 'muted'}
								label={$tracks[signal.id].isMoving ? 'Moving' : 'Static'}
							/>
						</div>
					</div>
				{/if}
			{:else}
				<div class="text-xs text-slate-500">
					No position — signal not placable from current receivers (A1).
				</div>
			{/if}
		</div>
	</details>

	<!-- ── TDOA (§9.6): the latest multilateration attempt ── -->
	<details class="border-t border-slate-700 pt-3" open={!!tdoa}>
		<summary class="cursor-pointer text-xs font-semibold uppercase tracking-wide text-slate-500">
			TDOA{tdoa
				? tdoa.accepted
					? ' · fix'
					: tdoa.locus
						? ' · locus'
						: ' · rejected'
				: ''}
		</summary>
		<div class="mt-2">
			{#if tdoa}
				{#if tdoa.fix}
					<div class="font-mono text-sm">
						{tdoa.fix.lat.toFixed(5)}, {tdoa.fix.lng.toFixed(5)}
					</div>
					<div class="mt-1 grid grid-cols-2 gap-x-2 gap-y-1 text-[10px]">
						<div><span class="text-slate-500">residual </span><span class="font-mono">{tdoa.fix.residualNs.toFixed(0)} ns</span></div>
						<div><span class="text-slate-500">pairs </span><span class="font-mono">{tdoa.fix.pairsUsed}</span></div>
						<div><span class="text-slate-500">baseline </span><span class="font-mono">{fmtM(tdoa.fix.maxBaselineM)}</span></div>
						<div><span class="text-slate-500">covariance </span><span class="font-mono">{tdoa.fix.covPosDef ? 'pos-def' : 'singular'}</span></div>
					</div>
					{#if tdoa.persisted}
						<div class="mt-1 text-[10px] text-green-400">
							fix persisted{tdoa.reference ? ` (reference ${tdoa.reference})` : ''} — placement owned by TDOA (§9.6 flip-flop guard)
						</div>
					{/if}
				{:else if tdoa.locus}
					<div class="text-xs text-slate-400">
						No unique fix — hyperbolic locus drawn on the map (§9.6).
					</div>
				{:else}
					<div class="text-xs text-amber-400">rejected: {tdoa.reason ?? 'unknown reason'}</div>
				{/if}
				<div class="mt-1 text-[10px] text-slate-500" title={dateFull(tdoa.at)}>
					receivers: {tdoa.receivers.join(', ')} · {relTime(tdoa.at)}
				</div>
			{:else}
				<div class="text-xs text-slate-500">
					No multilateration attempts — the engine needs tdoa.enabled and ≥2 receivers with positions (§9.6).
				</div>
			{/if}
		</div>
	</details>

	<!-- ── Recordings (§12.3): play WAV, analyze ↗ any format ── -->
	<details class="border-t border-slate-700 pt-3" ontoggle={onRecsToggle}>
		<summary class="flex cursor-pointer items-center justify-between text-xs font-semibold uppercase tracking-wide text-slate-500">
			<span>Recordings{recsLoaded ? ` (${recordings.length})` : ''}</span>
			{#if recsLoaded}
				<button
					class="text-[10px] normal-case text-slate-500 hover:text-slate-300"
					onclick={(e) => {
						e.preventDefault();
						loadRecordings();
					}}
				>refresh</button>
			{/if}
		</summary>
		<div class="mt-2">
			{#if recsLoading}
				<div class="text-xs text-slate-500">loading…</div>
			{:else if recsError}
				<div class="text-xs text-red-400">{recsError}</div>
			{:else if recordings.length === 0}
				<div class="text-xs text-slate-500">None yet — recordings are created automatically while a demodulated signal is active.</div>
			{:else}
				<ul class="space-y-1.5">
					{#each recordings as rec (rec.id)}
						<li class="flex items-center justify-between gap-2">
							<div class="min-w-0 text-xs">
								<div class="truncate font-mono text-slate-300" title={dateFull(rec.startTime)}>
									{dateFull(rec.startTime).split(', ').pop()}
								</div>
								<div class="text-[10px] text-slate-500">
									{rec.durationS.toFixed(1)} s · {rec.fileFormat.toUpperCase()}
									· {rateSps(rec.sampleRate)}
									· {(rec.sizeBytes / 1e6).toFixed(1)} MB
								</div>
							</div>
							<div class="flex shrink-0 items-center gap-1">
								{#if rec.fileFormat.toLowerCase() === 'wav'}
									<button
										class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-300 hover:bg-slate-700"
										aria-label="Play recording from {rec.startTime}"
										onclick={() => (playId = playId === rec.id ? null : rec.id)}
									>{playId === rec.id ? '✕' : '▶'}</button>
								{/if}
								<a
									href={recordingAudioUrl(rec.id)}
									download
									class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-300 hover:bg-slate-700"
									aria-label="Download recording from {rec.startTime}"
								>⭳</a>
								<button
									class="rounded bg-sky-900/60 px-1.5 py-0.5 text-[10px] text-sky-300 hover:bg-sky-900"
									title="Open in the full-size time-frequency analysis view (§19)"
									onclick={() => analyze(rec)}
								>analyze ↗</button>
							</div>
						</li>
						{#if playId === rec.id}
							<li class="rounded border border-slate-700 bg-slate-900/60 p-2">
								<AudioPlayer recording={rec} />
							</li>
						{/if}
					{/each}
				</ul>
				<div class="mt-1 text-[10px] text-slate-600">
					<span class="text-slate-500">⏳</span> no live update event exists — use refresh (F7)
				</div>
			{/if}
		</div>
	</details>

	<!-- ── Actions ── -->
	<details class="border-t border-slate-700 pt-3">
		<summary class="cursor-pointer text-xs font-semibold uppercase tracking-wide text-slate-500">
			Actions
		</summary>
		<div class="mt-2 flex flex-wrap gap-1.5 text-[10px]">
			<button
				class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700"
				onclick={copyId}
			>{copiedId ? 'copied ✓' : 'copy id'}</button>
			<button
				class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700 disabled:opacity-40"
				disabled={!placed}
				title={placed ? 'Center the map on this signal' : 'No position to center on'}
				onclick={() => placed && requestMapCenter(signal.lat!, signal.lon!)}
			>center map</button>
			<button
				class="rounded bg-slate-800 px-2 py-1 text-slate-300 hover:bg-slate-700 disabled:opacity-40"
				disabled={!inSpectrumSpan}
				title={inSpectrumSpan
					? "Open the spectrum workbench with this frequency marked"
					: "Signal is outside the selected receiver's current frame span"}
				onclick={openSpectrumSpan}
			>spectrum span ↗</button>
		</div>
	</details>

	<!-- ── Notes (§12.5, exactly as before + retry-on-failure) ── -->
	<details class="border-t border-slate-700 pt-3" open>
		<summary class="flex cursor-pointer items-center justify-between text-xs font-semibold uppercase tracking-wide text-slate-500">
			<span>Notes{notes.length > 0 ? ` (${notes.length})` : ''}</span>
		</summary>
		<div class="mt-2">
			{#if notes.length === 0}
				<div class="text-xs text-slate-500">No notes yet</div>
			{:else}
				<ul class="space-y-2">
					{#each notes as note (note.id)}
						<li>
							<div class="text-sm text-slate-200">{note.userNote}</div>
							<div class="text-[10px] text-slate-500">{dateFull(note.createdAt)}</div>
						</li>
					{/each}
				</ul>
			{/if}
			{#if notesError}
				<div class="mt-1 text-[10px] text-red-400">save failed: {notesError} — text kept, try again</div>
			{/if}
			<form
				class="mt-3 flex gap-2"
				onsubmit={(e) => {
					e.preventDefault();
					submitNote();
				}}
			>
				<input
					class="min-w-0 flex-1 rounded bg-slate-800 px-2 py-1 text-sm text-slate-200 placeholder-slate-500 outline-none focus:ring-1 focus:ring-sky-500"
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
	</details>
</div>
