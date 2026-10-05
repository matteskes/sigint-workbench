/**
 * §10.4 live-playback tests: the state machine (idle → connecting →
 * live → ended/error), the §10.4 framing rules (one hello, one Opus
 * packet per binary message), and the jitter-buffer scheduler — all
 * against injected Fake WebSocket / decoder / chunk constructors (the
 * vitest environment is plain node, no WebCodecs).
 */
import { describe, it, expect, beforeEach } from 'vitest';
import {
	LiveOpusPlayer,
	rms,
	type AudioDataLike,
	type ChunkCtor,
	type ChunkInit,
	type DecoderCtor,
	type LiveDeps,
	type LiveHandlers,
	type LiveState,
	type WSCtor
} from './liveOpus';
import { audioWsUrl } from '../api/client';

class FakeWebSocket {
	static instances: FakeWebSocket[] = [];
	url: string;
	binaryType = 'blob';
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onmessage: ((ev: any) => void) | null = null;
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onclose: ((ev: any) => void) | null = null;
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onerror: ((ev: any) => void) | null = null;
	closeCode: number | undefined;
	constructor(url: string) {
		this.url = url;
		FakeWebSocket.instances.push(this);
	}
	/** Drives the client's onmessage the way the §10.4 server would. */
	serverSend(data: unknown): void {
		this.onmessage?.({ data });
	}
	serverClose(code: number): void {
		this.onclose?.({ code });
	}
	close(code?: number): void {
		this.closeCode = code;
		this.onmessage = null;
		this.onclose = null;
		this.onerror = null;
	}
}
const FakeWS = FakeWebSocket as unknown as WSCtor;

class FakeChunk {
	static inits: ChunkInit[] = [];
	constructor(init: ChunkInit) {
		FakeChunk.inits.push(init);
	}
}
const FakeChunkCtor = FakeChunk as unknown as ChunkCtor;

class FakeDecoder {
	static instances: FakeDecoder[] = [];
	state = 'unconfigured';
	config: { codec: string; sampleRate: number; numberOfChannels: number } | undefined;
	output: ((d: AudioDataLike) => void) | undefined;
	error: ((e: unknown) => void) | undefined;
	decoded: unknown[] = [];
	constructor(init: { output: (d: AudioDataLike) => void; error: (e: unknown) => void }) {
		this.output = init.output;
		this.error = init.error;
		FakeDecoder.instances.push(this);
	}
	configure(config: { codec: string; sampleRate: number; numberOfChannels: number }): void {
		this.config = config;
		this.state = 'configured';
	}
	decode(chunk: unknown): void {
		this.decoded.push(chunk);
	}
	close(): void {
		this.state = 'closed';
	}
}
const FakeDecoderCtor = FakeDecoder as unknown as DecoderCtor;

/** AudioContext stand-in that records scheduled source start times. */
function fakeContext(): { ctx: AudioContext; starts: number[] } {
	const starts: number[] = [];
	const ctx = {
		currentTime: 100,
		destination: {},
		createBuffer() {
			return { copyToChannel() {} };
		},
		createBufferSource() {
			return {
				buffer: null as unknown,
				connect() {},
				start(when: number) {
					starts.push(when);
				}
			};
		}
	};
	return { ctx: ctx as unknown as AudioContext, starts };
}

function audioData(samples: number[]): AudioDataLike {
	const pcm = Float32Array.from(samples);
	return {
		numberOfFrames: samples.length,
		numberOfChannels: 1,
		sampleRate: 48000,
		copyTo(dest: Float32Array) {
			dest.set(pcm);
		},
		close() {}
	};
}

function makeRecorder(): {
	states: { state: LiveState; detail?: string }[];
	levels: number[];
	handlers: LiveHandlers;
} {
	const states: { state: LiveState; detail?: string }[] = [];
	const levels: number[] = [];
	const handlers: LiveHandlers = {
		onState: (state, detail) => states.push({ state, detail }),
		onLevel: (l) => levels.push(l)
	};
	return { states, levels, handlers };
}

function makePlayer(signalId = 'sig-1', noDecoder = false) {
	const { ctx, starts } = fakeContext();
	const rec = makeRecorder();
	const deps: LiveDeps = {
		WS: FakeWS,
		Decoder: noDecoder ? undefined : FakeDecoderCtor,
		Chunk: FakeChunkCtor
	};
	const player = new LiveOpusPlayer(signalId, ctx, rec.handlers, deps);
	return { player, starts, ...rec };
}

function last<T>(a: T[]): T | undefined {
	return a[a.length - 1];
}

const HELLO = JSON.stringify({
	type: 'audio.meta',
	signalId: 'sig-1',
	centerHz: 146520000,
	modulation: 'FM',
	subType: 'NFM'
});

describe('audioWsUrl (§10.4)', () => {
	it('derives /ws/audio with the signal parameter from the events base', () => {
		expect(audioWsUrl('abc-123')).toBe('ws://localhost:8080/ws/audio?signal=abc-123');
	});
});

describe('LiveOpusPlayer (§10.4)', () => {
	beforeEach(() => {
		FakeWebSocket.instances = [];
		FakeChunk.inits = [];
		FakeDecoder.instances = [];
	});

	it('reports unsupported without AudioDecoder and never opens a socket', () => {
		const { player, states } = makePlayer('sig-1', true);
		player.start();
		expect(last(states)?.state).toBe('unsupported');
		expect(FakeWebSocket.instances).toHaveLength(0);
	});

	it('goes idle → connecting → live on the audio.meta hello', () => {
		const { player, states } = makePlayer();
		player.start();
		expect(states[0]).toEqual({ state: 'connecting' });
		const ws = FakeWebSocket.instances[0];
		expect(ws.url).toBe('ws://localhost:8080/ws/audio?signal=sig-1');
		expect(ws.binaryType).toBe('arraybuffer');
		ws.serverSend(HELLO);
		expect(last(states)?.state).toBe('live');
		expect(FakeDecoder.instances[0].config).toEqual({
			codec: 'opus',
			sampleRate: 48000,
			numberOfChannels: 1
		});
	});

	it('decodes one chunk per binary packet with 20 ms timestamps', () => {
		const { player } = makePlayer();
		player.start();
		const ws = FakeWebSocket.instances[0];
		ws.serverSend(HELLO);
		const dec = FakeDecoder.instances[0];
		ws.serverSend(new ArrayBuffer(10));
		ws.serverSend(new ArrayBuffer(12));
		expect(dec.decoded).toHaveLength(2);
		expect(FakeChunk.inits.map((c) => c.timestamp)).toEqual([0, 20000]);
		expect(FakeChunk.inits.every((c) => c.type === 'key')).toBe(true);
	});

	it('ignores binary packets and foreign text before the hello', () => {
		const { player } = makePlayer();
		player.start();
		const ws = FakeWebSocket.instances[0];
		ws.serverSend(new ArrayBuffer(4));
		ws.serverSend('not json');
		ws.serverSend(JSON.stringify({ type: 'other' }));
		expect(FakeDecoder.instances).toHaveLength(0);
		ws.serverSend(HELLO);
		expect(FakeDecoder.instances).toHaveLength(1);
	});

	it('schedules decoded mono audio and feeds the level meter', () => {
		const { player, starts, levels } = makePlayer();
		player.start();
		const ws = FakeWebSocket.instances[0];
		ws.serverSend(HELLO);
		const dec = FakeDecoder.instances[0];
		dec.output?.(audioData([0.5, -0.5, 0.5, -0.5])); // rms 0.5
		expect(starts).toHaveLength(1);
		expect(starts[0]).toBeCloseTo(100.12, 5); // ctx 100 + 120 ms jitter buffer
		dec.output?.(audioData(new Array(960).fill(0.25)));
		expect(starts).toHaveLength(2);
		expect(starts[1]).toBeGreaterThan(starts[0]);
		expect(levels[0]).toBeCloseTo(0.5, 5);
	});

	it('drops decoded audio that would run more than 500 ms ahead', () => {
		const { player, starts } = makePlayer();
		player.start();
		const ws = FakeWebSocket.instances[0];
		ws.serverSend(HELLO);
		const dec = FakeDecoder.instances[0];
		// 30 × 20 ms chunks with no wall-clock time passing: the tail
		// past the 500 ms resync threshold is dropped, never buffered.
		for (let i = 0; i < 30; i++) dec.output?.(audioData(new Array(960).fill(0.1)));
		expect(starts.length).toBeLessThan(30);
	});

	it('close 1000 while live ends the stream; other codes are errors', () => {
		const first = makePlayer();
		first.player.start();
		FakeWebSocket.instances[0].serverSend(HELLO);
		FakeWebSocket.instances[0].serverClose(1000);
		expect(last(first.states)?.state).toBe('ended');
		expect(FakeDecoder.instances[0].state).toBe('closed');

		const second = makePlayer();
		second.player.start();
		FakeWebSocket.instances[1].serverSend(HELLO);
		FakeWebSocket.instances[1].serverClose(1006);
		expect(last(second.states)?.state).toBe('error');
	});

	it('stop() detaches the socket: no late ended after a user stop', () => {
		const { player, states } = makePlayer();
		player.start();
		const ws = FakeWebSocket.instances[0];
		ws.serverSend(HELLO);
		player.stop();
		expect(last(states)?.state).toBe('idle');
		expect(FakeDecoder.instances[0].state).toBe('closed');
		expect(ws.closeCode).toBe(1000);
		ws.serverClose(1000); // late server close must not re-enter
		expect(last(states)?.state).toBe('idle');
	});

	it('start() is idempotent while connected — one live stream', () => {
		const { player } = makePlayer();
		player.start();
		player.start();
		expect(FakeWebSocket.instances).toHaveLength(1);
	});
});

describe('rms', () => {
	it('is the clamped RMS of the chunk', () => {
		expect(rms(Float32Array.from([0.5, -0.5]))).toBeCloseTo(0.5, 6);
		expect(rms(Float32Array.from([4, -4]))).toBe(1); // clamped
		expect(rms(new Float32Array(0))).toBe(0);
	});
});