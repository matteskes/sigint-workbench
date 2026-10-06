import { get } from 'svelte/store';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
	connectionState,
	connectionAttempt,
	lastEventAt,
	restoredAt,
	startConnection,
	bootstrap,
	resetConnectionForTests
} from './connection';
import { signals } from './signals';
import { sdrs } from './sdrs';
import { connectWebSocket, fetchSignals, fetchSDRs } from '$lib/api/client';
import type { WSEvent } from '$lib/api/client';

// Mock the API layer: connection.ts owns the WS lifecycle, so the test
// drives a fake socket and counts bootstrap fetches.
vi.mock('$lib/api/client', () => ({
	connectWebSocket: vi.fn(),
	fetchSignals: vi.fn(),
	fetchSDRs: vi.fn()
}));

const wsMock = vi.mocked(connectWebSocket);
const fetchSignalsMock = vi.mocked(fetchSignals);
const fetchSDRsMock = vi.mocked(fetchSDRs);

interface FakeWS {
	url: string;
	onmessage: ((e: { data: string }) => void) | null;
	onopen: (() => void) | null;
	onclose: (() => void) | null;
	/** Mirrors the real connectWebSocket wiring (parse + dispatch). */
	wire(onEvent: (ev: WSEvent) => void, onOpen?: () => void): void;
	emit(ev: WSEvent): void;
	open(): void;
	close(): void;
	closed(): void;
}

let lastSocket: FakeWS | null = null;

function makeFakeWS(): FakeWS {
	const ws: FakeWS = {
		url: 'ws://test/ws',
		onmessage: null,
		onopen: null,
		onclose: null,
		wire(onEvent, onOpen) {
			this.onmessage = (e) => {
				try {
					onEvent(JSON.parse(e.data));
				} catch {
					// ignore parse errors — real semantics
				}
			};
			if (onOpen) this.onopen = onOpen;
		},
		emit(ev) {
			this.onmessage?.({ data: JSON.stringify(ev) });
		},
		open() {
			this.onopen?.();
		},
		close() {
			this.onclose?.();
		},
		closed() {
			this.onclose?.();
		}
	};
	return ws;
}

function sig(id: string) {
	return {
		id,
		freqHz: 145_500_000,
		bandwidthHz: 12_500,
		modulation: 'FM',
		subType: 'NFM',
		class: 'amateur',
		confidence: 0.8,
		powerDbm: -60,
		powerCalibrated: false,
		lat: null,
		lon: null,
		accuracyM: 0,
		firstSeen: '2026-10-05T00:00:00Z',
		lastSeen: '2026-10-05T00:00:01Z',
		sdrId: 'rtlsdr-0',
		verified: false
	};
}

beforeEach(() => {
	vi.clearAllMocks();
	resetConnectionForTests();
	lastSocket = null;
	signals.set([]);
	sdrs.set([]);
	connectionState.set('connecting');
	connectionAttempt.set(0);
	lastEventAt.set(0);
	restoredAt.set(0);
	wsMock.mockImplementation((onEvent: (ev: WSEvent) => void, onOpen?: () => void) => {
		const ws = makeFakeWS();
		ws.wire(onEvent, onOpen);
		lastSocket = ws;
		return ws as unknown as WebSocket;
	});
	fetchSignalsMock.mockResolvedValue([sig('sig-1')]);
	fetchSDRsMock.mockResolvedValue([]);
});

describe('connection store (§3.2)', () => {
	it('opens the socket, goes live on open, and bootstraps REST state', async () => {
		const stop = startConnection();
		expect(lastSocket).not.toBeNull();
		expect(get(connectionState)).toBe('connecting');

		lastSocket!.open();
		// bootstrap resolves async
		await vi.waitFor(() => {
			expect(get(connectionState)).toBe('live');
		});
		expect(fetchSignalsMock).toHaveBeenCalled();
		expect(fetchSDRsMock).toHaveBeenCalled();
		await vi.waitFor(() => {
			expect(get(signals).map((s) => s.id)).toEqual(['sig-1']);
		});
		// First connect never shows the "restored" banner (F5).
		expect(get(restoredAt)).toBe(0);
		stop();
	});

	it('survives idempotent starts (one socket, one lifecycle)', () => {
		const stop1 = startConnection();
		const stop2 = startConnection();
		expect(wsMock).toHaveBeenCalledTimes(1);
		stop1();
		stop2();
	});

	it('marks reconnecting with an attempt count on close, and restores after a drop', async () => {
		const stop = startConnection();
		lastSocket!.open();
		await vi.waitFor(() => expect(get(connectionState)).toBe('live'));

		lastSocket!.closed();
		expect(get(connectionState)).toBe('reconnecting');
		expect(get(connectionAttempt)).toBeGreaterThan(0);

		// The backoff reconnect creates a second socket (~1 s delay).
		await vi.waitFor(
			() => expect(wsMock).toHaveBeenCalledTimes(2),
			{ timeout: 5000 }
		);
		lastSocket!.open();
		await vi.waitFor(() => expect(get(connectionState)).toBe('live'), { timeout: 5000 });
		expect(get(restoredAt)).toBeGreaterThan(0); // one-line banner (F5)
		expect(get(connectionAttempt)).toBe(0);
		stop();
	}, 10_000);

	it('ingests signal.update coalesced and updates lastEventAt', async () => {
		vi.useFakeTimers();
		const stop = startConnection();
		lastSocket!.open();
		lastSocket!.emit({ type: 'signal.update', payload: sig('sig-9') });
		// Not yet — the 200 ms coalescing window (§14.3) is open.
		expect(get(signals)).toHaveLength(0);
		await vi.advanceTimersByTimeAsync(210);
		expect(get(signals).map((s) => s.id)).toContain('sig-9');
		expect(get(lastEventAt)).toBeGreaterThan(0);
		stop();
		vi.useRealTimers();
	});

	it('removes signals on signal.removed', async () => {
		signals.set([sig('sig-1'), sig('sig-2')]);
		const stop = startConnection();
		lastSocket!.open();
		lastSocket!.emit({ type: 'signal.removed', payload: { id: 'sig-1' } });
		expect(get(signals).map((s) => s.id)).toEqual(['sig-2']);
		stop();
	});

	it('bootstrap() heals REST state directly', async () => {
		await bootstrap();
		expect(get(signals)).toHaveLength(1);
	});
});
