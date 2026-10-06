// §4 design tokens: shared formatting helpers. Power unit honesty
// (§5.6) lives here so no component can re-implement it wrong.

/** Frequency label: GHz / MHz (3 decimals) / kHz. */
export function freqHz(hz: number): string {
	if (hz >= 1e9) return `${(hz / 1e9).toFixed(3)} GHz`;
	if (hz >= 1e6) return `${(hz / 1e6).toFixed(3)} MHz`;
	return `${(hz / 1e3).toFixed(1)} kHz`;
}

/** Bandwidth label: MHz / kHz / Hz. */
export function bandwidthHz(bw: number): string {
	if (bw >= 1e6) return `${(bw / 1e6).toFixed(1)} MHz`;
	if (bw >= 1e3) return `${(bw / 1e3).toFixed(1)} kHz`;
	return `${bw} Hz`;
}

/**
 * §5.6: power with its honest unit — `dBm` only when the receiver is
 * calibrated; otherwise relative dB, labeled "dB (rel.)", never "dBm".
 */
export function powerDb(powerDbm: number, calibrated: boolean): { value: string; unit: string } {
	return { value: powerDbm.toFixed(1), unit: calibrated ? 'dBm' : 'dB (rel.)' };
}

/** Sample-rate label: MS/s / kS/s / S/s. */
export function rateSps(rate: number): string {
	if (rate >= 1e6) return `${(rate / 1e6).toFixed(1)} MS/s`;
	if (rate >= 1e3) return `${(rate / 1e3).toFixed(0)} kS/s`;
	return `${rate} S/s`;
}

/** Relative age ("2s ago", "5m ago", "3h ago", "6d ago") for an ISO timestamp. */
export function relTime(iso: string, now: number = Date.now()): string {
	const t = Date.parse(iso);
	if (Number.isNaN(t)) return '';
	const s = Math.max(0, Math.round((now - t) / 1000));
	if (s < 60) return `${s}s ago`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.floor(m / 60);
	if (h < 24) return `${h}h ago`;
	return `${Math.floor(h / 24)}d ago`;
}

/** Short clock time (locale) for a row — absolute date stays on hover. */
export function timeOfDay(iso: string): string {
	const t = new Date(iso);
	if (Number.isNaN(t.getTime())) return iso;
	return t.toLocaleTimeString();
}

/** Full locale date+time for title attributes / metadata headers. */
export function dateFull(iso: string): string {
	const t = new Date(iso);
	if (Number.isNaN(t.getTime())) return iso;
	return t.toLocaleString();
}
