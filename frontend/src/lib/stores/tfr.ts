// §19 (D10) frontend state: the time-frequency picker metadata, the
// §18 drag-select draft, and the pure mappings between canvas
// geometry and request parameters. Computation itself never happens
// here — renders are requested from the recorder through the gateway
// (§19.3) and drawn on a plain <canvas> (§19.4, no new deps).

import { writable, type Writable } from 'svelte/store';

// ─── Method metadata (§19.2, normative notes) ────────────────────────

export type TFRMethod = 'stft' | 'reassigned' | 'spwvd' | 'cwt-morlet';
export type TFRWindow = 'rectangular' | 'gaussian' | 'hamming';

export interface MethodMeta {
	label: string;
	/** Dominant artifact note — the picker surfaces it beside the render (§19.4). */
	artifact: string;
	/** Whether the window parameter applies (stft/reassigned only). */
	windowed: boolean;
	/** Whether reassignment forbids rectangular (no phase gradient). */
	noRectangular?: boolean;
}

export const TFR_METHODS: Record<TFRMethod, MethodMeta> = {
	stft: {
		label: 'STFT',
		artifact: 'window resolution trade-off: a sharp window wide in frequency, a wide window sharp in time',
		windowed: true
	},
	reassigned: {
		label: 'Reassigned',
		artifact: 'reassignment smears noise-dominated regions — artifact, not a bug',
		windowed: true,
		noRectangular: true
	},
	spwvd: {
		label: 'SPWVD',
		artifact:
			'kernel smoothing suppresses cross-terms at the cost of resolution; integer-lag WVD folds content beyond ±fs/4 into mirrored ridges',
		windowed: false
	},
	'cwt-morlet': {
		label: 'CWT (Morlet)',
		artifact: 'scale smearing: low-frequency rows average over proportionally longer time',
		windowed: false
	}
};

export const TFR_WINDOWS: TFRWindow[] = ['hamming', 'gaussian', 'rectangular'];

// §19.5 request caps mirrored for picker bounds (the recorder is the
// authority; the server re-validates and answers 400/413).
export const TFR_MIN_NFFT = 256;
export const TFR_MAX_NFFT = 16384;
export const TFR_MAX_SPAN_S = 30;

// ─── §18 drag-select draft (§19.4 second entry point) ────────────────

/**
 * A drag-select on the §18 waterfall/spectrum canvas: the frequency
 * span (absolute Hz) plus the wall-clock window of the touched rows.
 * `startMs`/`endMs` may be equal (line-canvas drag = point in time).
 */
export interface SpectrumSelection {
	freqLoHz: number;
	freqHiHz: number;
	startMs: number;
	endMs: number;
}

export const spectrumSelection: Writable<SpectrumSelection | null> = writable(null);

// ─── Pure mappings (unit-tested) ─────────────────────────────────────

/** Recording-local fields needed to map a selection onto a recording. */
export interface RecordingWindow {
	startTime: string; // ISO
	durationS: number;
}

/**
 * Maps a §18 drag-select onto a recording's local span [t0, t1]:
 * wall-clock intersection clamped to the recording, capped at the
 * §19.5 span. Returns null when the selection does not overlap the
 * recording — the caller falls back to the recording's head.
 */
export function spanForRecording(
	sel: SpectrumSelection | null,
	rec: RecordingWindow
): [number, number] | null {
	if (!sel) return null;
	const start = Date.parse(rec.startTime);
	if (Number.isNaN(start)) return null;
	const recStartMs = start;
	const recEndMs = start + rec.durationS * 1000;
	const lo = Math.max(recStartMs, Math.min(sel.startMs, sel.endMs));
	const hi = Math.min(recEndMs, Math.max(sel.startMs, sel.endMs));
	if (hi <= lo) return null; // no overlap
	let t0 = (lo - recStartMs) / 1000;
	let t1 = (hi - recStartMs) / 1000;
	if (t1 - t0 > TFR_MAX_SPAN_S) {
		t1 = t0 + TFR_MAX_SPAN_S;
	}
	return [t0, t1];
}

/**
 * Builds the freqSpan for a recording from a selection: absolute Hz
 * clamped into the recording's band. Returns undefined when the
 * selection is null (render the full band).
 */
export function freqSpanForRecording(
	sel: SpectrumSelection | null,
	centerFreq: number,
	sampleRate: number
): [number, number] | undefined {
	if (!sel || sampleRate <= 0) return undefined;
	const half = sampleRate / 2;
	const lo = Math.max(centerFreq - half, Math.min(sel.freqLoHz, sel.freqHiHz));
	const hi = Math.min(centerFreq + half, Math.max(sel.freqLoHz, sel.freqHiHz));
	if (hi <= lo) return undefined;
	return [lo, hi];
}

/**
 * Waterfall drag geometry → selection. `x0/x1` are pixel columns of
 * the drag, `width` the canvas width, `freqLoHz/freqHiHz` the band
 * edges (freqHz ± sampleRate/2, §18.2); `t0Ms/t1Ms` the wall-clock
 * times of the first/last visible rows (equal for the line canvas).
 * Degenerate drags (< 3 px) return null so a click clears instead.
 */
export function dragToSelection(
	x0: number,
	x1: number,
	width: number,
	freqLoHz: number,
	freqHiHz: number,
	t0Ms: number,
	t1Ms: number
): SpectrumSelection | null {
	if (width < 1) return null;
	if (Math.abs(x1 - x0) < 3) return null;
	const lo = Math.min(x0, x1);
	const hi = Math.max(x0, x1);
	const fx = (px: number) => freqLoHz + (Math.max(0, Math.min(width, px)) / width) * (freqHiHz - freqLoHz);
	return {
		freqLoHz: fx(lo),
		freqHiHz: fx(hi),
		startMs: Math.min(t0Ms, t1Ms),
		endMs: Math.max(t0Ms, t1Ms)
	};
}
