const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080';
const WS_URL = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8081';

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

export async function fetchSignals(
	minLat: number,
	minLon: number,
	maxLat: number,
	maxLon: number
): Promise<Signal[]> {
	const params = new URLSearchParams({
		minLat: String(minLat),
		minLon: String(minLon),
		maxLat: String(maxLat),
		maxLon: String(maxLon)
	});
	const res = await fetch(`${API_URL}/api/signals?${params}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

export async function fetchSignal(id: string): Promise<Signal> {
	const res = await fetch(`${API_URL}/api/signals/${id}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

export function connectWebSocket(onEvent: (event: any) => void): WebSocket {
	const ws = new WebSocket(WS_URL);
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