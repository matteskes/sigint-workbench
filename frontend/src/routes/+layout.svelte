<script lang="ts">
	// §3.2 app shell: owns the single WS connection and the health poll
	// (lifecycles that must survive route changes), the first-run and
	// reconnect banners, and the §15 keyboard map.
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
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

	// ── §3.1 ?signal=<id> deep-link mirror (UI-BUGCHECK B14/B15) ─────
	// Lives in the shell, not the Inspector: the Inspector unmounts the
	// instant the selection clears, which destroyed its copy of this
	// effect before the delete-the-param branch could run and left a
	// stale ?signal= that resurrected the inspector on reload. Uses
	// $app/navigation's replaceState — raw history.replaceState fights
	// SvelteKit's router and logs a console warning (B15).
	$effect(() => {
		const id = $selectedSignal?.id ?? null;
		const url = new URL(window.location.href);
		if (url.searchParams.get('signal') === id) return;
		if (id) url.searchParams.set('signal', id);
		else url.searchParams.delete('signal');
		replaceState(url, {});
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

	/** UI-BUGCHECK B12: keys that natively activate the focused element —
	 * the global map must not swallow them (Space on a button, Enter on a
	 * link), or those controls become keyboard-unreachable. */
	function isActivator(t: EventTarget | null): boolean {
		return (
			t instanceof HTMLElement &&
			(t.tagName === 'BUTTON' ||
				t.tagName === 'A' ||
				t.tagName === 'SUMMARY' ||
				t.getAttribute('role') === 'button')
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
				// B12: let Enter natively activate a focused button/link;
				// only hijack it for row selection from a non-activator.
				if (sig && !isActivator(e.target)) {
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
				// registered toggle no-ops when no player is mounted. B12:
				// skip when focus sits on an activatable element so Space
				// keeps its native press semantics there.
				if (!isActivator(e.target)) {
					e.preventDefault();
					toggleLiveAudio();
				}
				break;
		}
	}
</script>

<!-- §15 keyboard map (below) is global — view switches, ? overlay,
     j/k navigation, Esc close, Space audio. -->
<svelte:window onkeydown={handleKeydown} />

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