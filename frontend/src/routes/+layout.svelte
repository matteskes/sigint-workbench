<script lang="ts">
	// §3.2 app shell: owns the single WS connection and the health poll
	// (lifecycles that must survive route changes), the first-run and
	// reconnect banners, and the §15 keyboard map.
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { get } from 'svelte/store';
	import AppBar from '$lib/components/shell/AppBar.svelte';
	import FirstRunBanner from '$lib/components/shell/FirstRunBanner.svelte';
	import ShortcutsOverlay from '$lib/components/shell/ShortcutsOverlay.svelte';
	import { startConnection, restoredAt } from '$lib/stores/connection';
	import { startHealthPolling, stopHealthPolling } from '$lib/stores/health';
	import { signals, selectedSignal } from '$lib/stores/signals';
	import {
		shortcutsOpen,
		mapReceiverId,
		orderedSignalIds,
		highlightIndex,
		inspectorOpen,
		toggleLiveAudio
	} from '$lib/stores/ui';

	let { children } = $props();

	let stopConnection: (() => void) | null = null;

	// F5: one-line "connection restored — state reloaded" banner,
	// self-dismissing (no toast storm).
	let showRestored = $state(false);
	let restoredTimer: ReturnType<typeof setTimeout> | null = null;
	$effect(() => {
		if ($restoredAt > 0) {
			showRestored = true;
			if (restoredTimer) clearTimeout(restoredTimer);
			restoredTimer = setTimeout(() => (showRestored = false), 4000);
		}
	});

	onMount(() => {
		stopConnection = startConnection();
		startHealthPolling();
		return () => {
			stopConnection?.();
			stopHealthPolling();
			if (restoredTimer) clearTimeout(restoredTimer);
		};
	});

	// ─── §15 keyboard map ───────────────────────────────────────────────
	const VIEW_ROUTES = ['/', '/spectrum', '/signals', '/recordings', '/analysis', '/setup'];

	function isTypingTarget(t: EventTarget | null): boolean {
		return (
			t instanceof HTMLElement &&
			(t.tagName === 'INPUT' ||
				t.tagName === 'TEXTAREA' ||
				t.tagName === 'SELECT' ||
				t.isContentEditable)
		);
	}

	function handleKeydown(e: KeyboardEvent): void {
		if (isTypingTarget(e.target) || e.metaKey || e.ctrlKey || e.altKey) return;

		// View switches 1…6.
		if (/^[1-6]$/.test(e.key)) {
			e.preventDefault();
			goto(VIEW_ROUTES[Number(e.key) - 1]);
			return;
		}

		switch (e.key) {
			case '?':
				e.preventDefault();
				shortcutsOpen.update((v) => !v);
				break;
			case '/': {
				e.preventDefault();
				document.querySelector<HTMLInputElement>('[data-signal-search]')?.focus();
				break;
			}
			case 'j':
			case 'ArrowDown': {
				const n = get(orderedSignalIds).length;
				if (n === 0) break;
				e.preventDefault();
				highlightIndex.update((i) => Math.min(n - 1, i < 0 ? 0 : i + 1));
				break;
			}
			case 'k':
			case 'ArrowUp': {
				const n = get(orderedSignalIds).length;
				if (n === 0) break;
				e.preventDefault();
				highlightIndex.update((i) => Math.max(0, i < 0 ? 0 : i - 1));
				break;
			}
			case 'Enter': {
				const list = get(orderedSignalIds);
				const i = get(highlightIndex);
				const id = i >= 0 ? list[i] : undefined;
				const sig = id ? get(signals).find((s) => s.id === id) : undefined;
				if (sig) {
					e.preventDefault();
					selectedSignal.set(sig);
					inspectorOpen.set(true);
				}
				break;
			}
			case 'Escape':
				if (get(shortcutsOpen)) {
					shortcutsOpen.set(false);
				} else if (get(mapReceiverId)) {
					mapReceiverId.set(null);
				} else if (get(selectedSignal)) {
					selectedSignal.set(null);
				}
				break;
			case ' ':
				// Play/pause live audio (F4) — one stream at a time; the
				// registered toggle no-ops when no player is mounted.
				e.preventDefault();
				toggleLiveAudio();
				break;
		}
	}
</script>

<div class="flex h-screen flex-col bg-slate-900 text-slate-100">
	<AppBar />
	<FirstRunBanner />
	{#if showRestored}
		<div class="border-b border-green-800 bg-green-950/50 px-4 py-1.5 text-xs text-green-300" role="status">
			connection restored — state reloaded
		</div>
	{/if}

	<!-- Active view (per route) -->
	<main class="flex min-h-0 flex-1 overflow-hidden">
		{@render children()}
	</main>

	<ShortcutsOverlay />
</div>