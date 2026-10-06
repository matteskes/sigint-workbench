// §3/§13: cross-view UI state that outlives route changes. Active
// view is the URL (SvelteKit owns it); this store holds everything
// the shell and views share: layer toggles, table chrome, keyboard
// highlight, and the live-audio toggle registry (Space shortcut).

import { writable } from 'svelte/store';

/** §5 map layer toggles — bottom-left popover on Operations. */
export interface LayerToggles {
	signals: boolean;
	receivers: boolean;
	tracks: boolean;
	labels: boolean;
}

export const layers = writable<LayerToggles>({
	signals: true,
	receivers: true,
	tracks: true,
	labels: true
});

/** Inspector drawer visibility (§6 — overlay on narrow screens). */
export const inspectorOpen = writable(true);

/** §8 table density (comfortable / compact rows). */
export type TableDensity = 'comfortable' | 'compact';
export const tableDensity = writable<TableDensity>('comfortable');

/** `?` shortcut overlay. */
export const shortcutsOpen = writable(false);

/**
 * Inspector "center map" action → MapView. Carries the target plus a
 * timestamp so repeat requests for the same point re-trigger. MapView
 * is the sole consumer; selection alone never moves the viewport (F1).
 */
export const mapCenterRequest = writable<{ lat: number; lon: number; ts: number } | null>(null);

/** Asks every MapView to center on a coordinate (§6 Actions). */
export function requestMapCenter(lat: number, lon: number): void {
	mapCenterRequest.set({ lat, lon, ts: Date.now() });
}

/** Receiver selected on the map — opens the receiver card popover (§5). */
export const mapReceiverId = writable<string | null>(null);

/** §5 unlocated chip: filter the table to signals without a position. */
export const unlocatedOnly = writable(false);

// ─── Keyboard table navigation (§15) ─────────────────────────────────

/**
 * The table publishes its current filter+sort order here so the
 * shell's j/k/Enter shortcuts navigate the rows the operator sees.
 * The table is the writer; the shell is the reader.
 */
export const orderedSignalIds = writable<string[]>([]);
export const highlightIndex = writable(-1);

// ─── Live-audio toggle registry (§15 `Space`) ────────────────────────

let liveAudioToggle: (() => void) | null = null;

/** LiveAudioPlayer registers its toggle while mounted; Space dispatches it. */
export function registerLiveAudioToggle(fn: (() => void) | null): void {
	liveAudioToggle = fn;
}

/** Dispatched by the shell's Space handler; no-op when nothing is mounted. */
export function toggleLiveAudio(): void {
	liveAudioToggle?.();
}
