// §2.2 (P2) fail-loud phrases: status codes map to human phrases that
// stay visible. Promoted from SDRControl's local describe() so every
// control surface (receiver rail, receiver card, future call sites)
// states failures identically.

const PHRASES: Record<number, string> = {
	502: 'capture unreachable',
	404: 'unknown device at capture',
	409: 'device has no scan loop',
	503: 'database unavailable'
};

/**
 * Maps an error thrown by the API client (`API error: <status>`) to
 * its fail-loud phrase (§13.1 semantics). Unknown errors pass through
 * as text — a failure is never swallowed into a generic "error".
 */
export function describeStatus(e: unknown): string {
	const m = /(\d{3})\s*$/.exec(String(e));
	if (!m) return String(e);
	const status = Number(m[1]);
	return PHRASES[status] ?? `error ${status}`;
}
