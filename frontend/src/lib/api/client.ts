const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080';
// Events enter through the api-gateway /ws relay (§2.2 A3) — never
// ws-hub directly (internal-only since the relay landed).
const WS_URL = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080/ws';

export interface Signal {
	id: string;
	freqHz: number;
	bandwidthHz: number;
	modulation: string;
	subType: string;
	class: string;
	confidence: number;
	powerDbm: number;
	lat: number | null;
	lon: number | null;
	accuracyM: number;
	firstSeen: string;
	lastSeen: string;
	sdrId: string;
	verified: boolean;
}

export interface SDRStatus {
	id: string;
	model: string;
	freqHz: number;
	gainDb: number;
	bwHz: number;
	active: boolean;
}

export interface BBox {
	minLat: number;
	minLon: number;
	maxLat: number;
	maxLon: number;
}

export type WSEventType = 'signal.new' | 'signal.update' | 'signal.removed' | 'sdr.status' | 'audio.level';

export interface WSEvent {
	type: WSEventType;
	payload: any;
}

/** Fetches active signals, optionally limited to a bounding box. */
export async function fetchSignals(bbox?: BBox): Promise<Signal[]> {
	const qs = bbox
		? `?${new URLSearchParams({
				minLat: String(bbox.minLat),
				minLon: String(bbox.minLon),
				maxLat: String(bbox.maxLat),
				maxLon: String(bbox.maxLon)
			})}`
		: '';
	const res = await fetch(`${API_URL}/api/signals${qs}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

export async function fetchSignal(id: string): Promise<Signal> {
	const res = await fetch(`${API_URL}/api/signals/${id}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/**
 * Fetches all SDRs known to the gateway and maps the db.SDRDevice
 * JSON (§12.1) to the dashboard SDRStatus shape. The database row has
 * no bandwidth field, so bwHz starts at 0 and is refreshed by
 * sdr.status events.
 */
export async function fetchSDRs(): Promise<SDRStatus[]> {
	const res = await fetch(`${API_URL}/api/sdrs`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	const devices: any[] = await res.json();
	return devices.map((d) => ({
		id: String(d.id),
		model: String(d.model),
		freqHz: Number(d.freqHz ?? 0),
		gainDb: Number(d.gainDb ?? 0),
		bwHz: 0,
		active: Boolean(d.active)
	}));
}

/**
 * Opens the event WebSocket. onOpen fires on every (re)connect so the
 * caller can re-bootstrap state missed while disconnected.
 */
export function connectWebSocket(onEvent: (event: WSEvent) => void, onOpen?: () => void): WebSocket {
	const ws = new WebSocket(WS_URL);
	if (onOpen) {
		ws.onopen = () => onOpen();
	}
	ws.onmessage = (e) => {
		try {
			const event = JSON.parse(e.data);
			onEvent(event);
		} catch {
			// ignore parse errors
		}
	};
	return ws;
}