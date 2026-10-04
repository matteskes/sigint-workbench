import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fetchSignals, fetchSignal, fetchSDRs, connectWebSocket } from './client';

// Minimal WebSocket double: records the URL and lets tests emit messages.
class FakeWebSocket {
	url: string;
	onmessage: ((e: { data: string }) => void) | null = null;
	onopen: (() => void) | null = null;
	constructor(url: string) {
		this.url = url;
	}
	emit(data: string) {
		this.onmessage?.({ data });
	}
	open() {
		this.onopen?.();
	}
}

function okJSON(body: unknown): Response {
	return { ok: true, status: 200, json: async () => body } as Response;
}

beforeEach(() => {
	vi.unstubAllGlobals();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('fetchSignals', () => {
	it('sends bounding-box query params', async () => {
		const fetchMock = vi.fn().mockResolvedValue(okJSON([{ id: 'sig-1' }]));
		vi.stubGlobal('fetch', fetchMock);

		const result = await fetchSignals({ minLat: 50, minLon: 30, maxLat: 51, maxLon: 31 });

		expect(result).toEqual([{ id: 'sig-1' }]);
		expect(fetchMock).toHaveBeenCalledTimes(1);
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/signals');
		expect(url.searchParams.get('minLat')).toBe('50');
		expect(url.searchParams.get('minLon')).toBe('30');
		expect(url.searchParams.get('maxLat')).toBe('51');
		expect(url.searchParams.get('maxLon')).toBe('31');
	});

	it('omits query params when no bounding box is given', async () => {
		const fetchMock = vi.fn().mockResolvedValue(okJSON([]));
		vi.stubGlobal('fetch', fetchMock);

		await fetchSignals();

		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/signals');
		expect(url.search).toBe('');
	});

	it('throws on non-OK response', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404, json: async () => ({}) } as Response));
		await expect(fetchSignals()).rejects.toThrow('API error: 404');
	});
});

describe('fetchSignal', () => {
	it('requests the signal by id', async () => {
		const fetchMock = vi.fn().mockResolvedValue(okJSON({ id: 'abc' }));
		vi.stubGlobal('fetch', fetchMock);

		const sig = await fetchSignal('abc');

		expect(sig.id).toBe('abc');
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/signals/abc');
		expect(url.search).toBe('');
	});

	it('throws on non-OK response', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 500, json: async () => ({}) } as Response));
		await expect(fetchSignal('abc')).rejects.toThrow('API error: 500');
	});
});

describe('fetchSDRs', () => {
	it('maps db.SDRDevice JSON to SDRStatus', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON([
				{ id: 'rtlsdr-0', model: 'RTL2832U', serial: '0001', lat: 40.7, lon: -74.0, gainDb: 40, freqHz: 146_520_000, active: true },
				{ id: 'simulator-0', model: 'Simulator', gainDb: 40, freqHz: 0, active: false }
			])
		);
		vi.stubGlobal('fetch', fetchMock);

		const sdrs = await fetchSDRs();

		expect(sdrs).toEqual([
			{ id: 'rtlsdr-0', model: 'RTL2832U', freqHz: 146_520_000, gainDb: 40, bwHz: 0, active: true },
			{ id: 'simulator-0', model: 'Simulator', freqHz: 0, gainDb: 40, bwHz: 0, active: false }
		]);
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/sdrs');
	});

	it('throws on non-OK response', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503, json: async () => ({}) } as Response));
		await expect(fetchSDRs()).rejects.toThrow('API error: 503');
	});
});

describe('connectWebSocket', () => {
	it('connects to the gateway relay by default', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const ws = connectWebSocket(() => {}) as unknown as FakeWebSocket;
		expect(ws.url).toBe('ws://localhost:8080/ws');
	});

	it('delivers parsed JSON events to the callback', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const events: unknown[] = [];
		const ws = connectWebSocket((e) => events.push(e)) as unknown as FakeWebSocket;
		ws.emit(JSON.stringify({ type: 'signal.new', payload: { id: 'x' } }));
		expect(events).toEqual([{ type: 'signal.new', payload: { id: 'x' } }]);
	});

	it('fires onOpen on connect', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		let opened = 0;
		const ws = connectWebSocket(() => {}, () => opened++) as unknown as FakeWebSocket;
		expect(opened).toBe(0);
		ws.open();
		expect(opened).toBe(1);
	});

	it('swallows malformed JSON instead of throwing', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const events: unknown[] = [];
		const ws = connectWebSocket((e) => events.push(e)) as unknown as FakeWebSocket;
		expect(() => ws.emit('not-json')).not.toThrow();
		expect(events).toEqual([]);
	});
});