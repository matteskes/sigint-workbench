import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fetchSignals, fetchSignal, connectWebSocket } from './client';

// Minimal WebSocket double: records the URL and lets tests emit messages.
class FakeWebSocket {
	url: string;
	onmessage: ((e: { data: string }) => void) | null = null;
	constructor(url: string) {
		this.url = url;
	}
	emit(data: string) {
		this.onmessage?.({ data });
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

		const result = await fetchSignals(50, 30, 51, 31);

		expect(result).toEqual([{ id: 'sig-1' }]);
		expect(fetchMock).toHaveBeenCalledTimes(1);
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/signals');
		expect(url.searchParams.get('minLat')).toBe('50');
		expect(url.searchParams.get('minLon')).toBe('30');
		expect(url.searchParams.get('maxLat')).toBe('51');
		expect(url.searchParams.get('maxLon')).toBe('31');
	});

	it('throws on non-OK response', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404, json: async () => ({}) } as Response));
		await expect(fetchSignals(0, 0, 1, 1)).rejects.toThrow('API error: 404');
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

describe('connectWebSocket', () => {
	it('connects to the default WS URL', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const ws = connectWebSocket(() => {}) as unknown as FakeWebSocket;
		expect(ws.url).toBe('ws://localhost:8081');
	});

	it('delivers parsed JSON events to the callback', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const events: unknown[] = [];
		const ws = connectWebSocket((e) => events.push(e)) as unknown as FakeWebSocket;
		ws.emit(JSON.stringify({ type: 'signal.new', payload: { id: 'x' } }));
		expect(events).toEqual([{ type: 'signal.new', payload: { id: 'x' } }]);
	});

	it('swallows malformed JSON instead of throwing', () => {
		vi.stubGlobal('WebSocket', FakeWebSocket);
		const events: unknown[] = [];
		const ws = connectWebSocket((e) => events.push(e)) as unknown as FakeWebSocket;
		expect(() => ws.emit('not-json')).not.toThrow();
		expect(events).toEqual([]);
	});
});