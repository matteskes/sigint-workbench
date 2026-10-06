import { writable, derived } from 'svelte/store';
import type { SpectrumFrame } from '../api/client';

/**
 * §18.3 spectrum store — the latest frame per SDR plus a per-SDR
 * waterfall ring buffer. Frames arrive capped at spectrum.rate_hz
 * (§18.1) and are dropped first under §14.3 saturation, so the
 * history is "the rows we received" — never interpolated.
 */
export const WATERFALL_ROWS = 300;

export interface SpectrumState {
	/** Most recent accepted frame for this SDR. */
	latest: SpectrumFrame;
	/** Waterfall history, oldest first, FIFO-evicted at WATERFALL_ROWS. */
	rows: SpectrumFrame[];
}

/** Per-SDR spectrum state (§18.3). */
export const spectrum = writable<Record<string, SpectrumState>>({});

/** The SDR whose frame the SpectrumView shows; null = follow the first. */
export const selectedSdrId = writable<string | null>(null);

/**
 * A frequency marked by the Inspector's "spectrum span ↗" action (§6):
 * SpectrumView draws a marker line at it; cleared by the workbench.
 */
export const markedFreqHz = writable<number | null>(null);

export const spectrumSdrIds = derived(spectrum, ($s) => Object.keys($s).sort());

/** Selected SDR's state, falling back to the lexicographically first SDR. */
export const selectedSpectrum = derived(
	[spectrum, selectedSdrId],
	([$s, $sel]) => {
		const id = $sel !== null && $s[$sel] ? $sel : Object.keys($s).sort()[0];
		return id ? $s[id] : null;
	}
);

/**
 * §18.2 envelope check. A malformed frame is a no-op, never a crash
 * (§14.3): types must hold, bins MUST equal len(db), and t must parse.
 */
export function isSpectrumFrame(f: unknown): f is SpectrumFrame {
	if (typeof f !== 'object' || f === null) return false;
	const p = f as Record<string, unknown>;
	return (
		typeof p.sdrId === 'string' &&
		p.sdrId !== '' &&
		typeof p.freqHz === 'number' &&
		Number.isFinite(p.freqHz) &&
		p.freqHz >= 0 &&
		typeof p.sampleRate === 'number' &&
		Number.isFinite(p.sampleRate) &&
		p.sampleRate > 0 &&
		typeof p.t === 'string' &&
		!Number.isNaN(Date.parse(p.t)) &&
		typeof p.df === 'number' &&
		Number.isFinite(p.df) &&
		p.df > 0 &&
		typeof p.bins === 'number' &&
		Number.isInteger(p.bins) &&
		p.bins > 0 &&
		Array.isArray(p.db) &&
		p.db.length === p.bins &&
		p.db.every((v) => typeof v === 'number' && Number.isFinite(v))
	);
}

/**
 * Applies one spectrum.frame (§18.3). Idempotent per `t`: duplicates
 * and out-of-order (older) frames never rewind the waterfall. Gaps
 * are frames that never arrived (§18.1 pacing) — nothing is
 * synthesized for them. Returns true when the frame was accepted.
 */
export function applySpectrumFrame(f: unknown): boolean {
	if (!isSpectrumFrame(f)) return false;
	const ts = Date.parse(f.t);
	let accepted = false;
	spectrum.update(($s) => {
		const prev = $s[f.sdrId];
		if (prev && ts <= Date.parse(prev.latest.t)) return $s;
		accepted = true;
		const rows = prev ? [...prev.rows, f] : [f];
		if (rows.length > WATERFALL_ROWS) rows.splice(0, rows.length - WATERFALL_ROWS);
		return { ...$s, [f.sdrId]: { latest: f, rows } };
	});
	return accepted;
}

/** Drops one SDR's history, or everything when no id is given. */
export function clearSpectrum(sdrId?: string): void {
	spectrum.update(($s) => {
		if (sdrId === undefined) return {};
		if (!(sdrId in $s)) return $s;
		const out = { ...$s };
		delete out[sdrId];
		return out;
	});
}