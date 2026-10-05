/**
 * §10.4 live browser Opus playback: consumes the gateway-relayed
 * /ws/audio stream (one text audio.meta hello, then one binary Opus
 * packet per message) and plays it through the Web Audio API after
 * decoding with WebCodecs' AudioDecoder — zero extra dependencies.
 *
 * Runtimes without AudioDecoder report 'unsupported'; the recording
 * player (§10.5 WAV playback) stays fully functional.
 */
import { audioWsUrl } from '../api/client';

/** Connection/playback lifecycle for one §10.4 stream. */
export type LiveState =
	| 'idle'
	| 'connecting'
	| 'live'
	| 'ended'
	| 'error'
	| 'unsupported';

/** The §10.4 text hello (JSON, exactly one per stream). */
export interface LiveAudioMeta {
	type: string;
	signalId: string;
	centerHz: number;
	modulation: string;
	subType: string;
}

// ─── Minimal WebCodecs + WebSocket surface (injectable for tests) ─────

export interface AudioDataLike {
	numberOfFrames: number;
	numberOfChannels: number;
	sampleRate: number;
	copyTo(dest: Float32Array, options: { planeIndex: number; format?: string }): void;
	close(): void;
}

export interface ChunkInit {
	type: 'key' | 'delta';
	timestamp: number;
	data: BufferSource;
}

export interface DecoderLike {
	configure(config: { codec: string; sampleRate: number; numberOfChannels: number }): void;
	decode(chunk: unknown): void;
	close(): void;
	state: string;
}

export type DecoderCtor = new (init: {
	output: (data: AudioDataLike) => void;
	error: (err: unknown) => void;
}) => DecoderLike;

export type ChunkCtor = new (init: ChunkInit) => unknown;

export interface WSLike {
	binaryType: string;
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onmessage: ((ev: any) => void) | null;
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onclose: ((ev: any) => void) | null;
	/* eslint-disable-next-line @typescript-eslint/no-explicit-any */
	onerror: ((ev: any) => void) | null;
	close(code?: number, reason?: string): void;
}

export type WSCtor = new (url: string) => WSLike;

export interface LiveDeps {
	WS: WSCtor;
	/** undefined ⇒ this runtime has no WebCodecs AudioDecoder. */
	Decoder: DecoderCtor | undefined;
	Chunk: ChunkCtor;
}

/** The real browser implementations, feature-detected at runtime. */
export function defaultDeps(): LiveDeps {
	const g = globalThis as Record<string, unknown>;
	return {
		WS: WebSocket as unknown as WSCtor,
		Decoder: g.AudioDecoder as DecoderCtor | undefined,
		Chunk: g.EncodedAudioChunk as ChunkCtor
	};
}

// ─── Playout constants (§10.4: mono 48 kHz, 20 ms Opus frames) ────────

const CODEC = 'opus';
const SAMPLE_RATE = 48000; // normative decoder output rate
const CHANNELS = 1;
const FRAME_US = 20000; // one Opus frame = 20 ms
const MIN_LATENCY_S = 0.12; // jitter-buffer target
const MAX_AHEAD_S = 0.5; // resync threshold (UDP-like drop, §10.4.3)

export interface LiveHandlers {
	onState: (state: LiveState, detail?: string) => void;
	/** Decoded-audio RMS 0..1 per ~20 ms chunk (meter feed). */
	onLevel?: (level: number) => void;
}

/**
 * One live audio stream. Not reusable after `stop()` — construct a
 * new player per listen. The dashboard mounts one at a time.
 */
export class LiveOpusPlayer {
	private state: LiveState = 'idle';
	private ws: WSLike | undefined;
	private decoder: DecoderLike | undefined;
	private timestampUs = 0;
	private playhead = 0; // AudioContext time of the next scheduled chunk
	private readonly signalId: string;
	private readonly ctx: AudioContext;
	private readonly handlers: LiveHandlers;
	private readonly deps: LiveDeps;

	constructor(
		signalId: string,
		ctx: AudioContext,
		handlers: LiveHandlers,
		deps?: Partial<LiveDeps>
	) {
		this.signalId = signalId;
		this.ctx = ctx;
		this.handlers = handlers;
		this.deps = { ...defaultDeps(), ...deps };
	}

	/** Opens the /ws/audio stream; reports 'connecting' immediately. */
	start(): void {
		if (this.state === 'connecting' || this.state === 'live') return; // one at a time
		if (!this.deps.Decoder) {
			this.setState('unsupported');
			return;
		}
		this.setState('connecting');
		this.timestampUs = 0;
		this.playhead = 0;
		const ws = new this.deps.WS(audioWsUrl(this.signalId));
		ws.binaryType = 'arraybuffer';
		ws.onmessage = (ev) => this.onMessage(ev.data);
		ws.onerror = () => {
			if (this.state === 'connecting' || this.state === 'live') {
				this.setState('error', 'websocket error');
			}
		};
		ws.onclose = (ev) => this.onClose(ev);
		this.ws = ws;
	}

	/** Stops the stream; safe to call in any state. */
	stop(): void {
		this.teardownDecoder();
		if (this.ws) {
			const ws = this.ws;
			this.ws = undefined;
			ws.onmessage = null;
			ws.onerror = null;
			ws.onclose = null; // a user stop is not an 'ended' event
			try {
				ws.close(1000);
			} catch {
				// already closing/closed
			}
		}
		this.setState('idle');
	}

	private setState(s: LiveState, detail?: string): void {
		this.state = s;
		this.handlers.onState(s, detail);
	}

	private teardownDecoder(): void {
		if (this.decoder && this.decoder.state !== 'closed') {
			this.decoder.close();
		}
		this.decoder = undefined;
	}

	private onClose(ev: { code: number }): void {
		if (this.state !== 'connecting' && this.state !== 'live') return;
		this.teardownDecoder();
		// Close 1000 = the stream is over (§10.4): the session
		// finalized, or the signal was never demodulated past the 3 s
		// grace. Anything else is an abnormal termination.
		if (ev.code === 1000) {
			this.setState('ended');
		} else {
			this.setState('error', `closed ${ev.code}`);
		}
	}

	private onMessage(data: unknown): void {
		if (typeof data === 'string') {
			this.onHello(data);
			return;
		}
		// Binary = exactly one Opus packet (§10.4.2). Packets before the
		// hello carry no decoder — dropped, never buffered (§10.4).
		if (!this.decoder) return;
		const chunk = new this.deps.Chunk({
			type: 'key',
			timestamp: this.timestampUs,
			data: data as ArrayBuffer
		});
		this.timestampUs += FRAME_US;
		this.decoder.decode(chunk);
	}

	private onHello(text: string): void {
		let meta: LiveAudioMeta;
		try {
			meta = JSON.parse(text) as LiveAudioMeta;
		} catch {
			return; // not the hello — client-bound text is ignored (§10.4)
		}
		if (!meta || meta.type !== 'audio.meta' || !this.deps.Decoder) return;
		this.decoder = new this.deps.Decoder({
			output: (d) => this.onAudioData(d),
			error: (err) => this.setState('error', String(err))
		});
		this.decoder.configure({ codec: CODEC, sampleRate: SAMPLE_RATE, numberOfChannels: CHANNELS });
		this.setState('live', `${meta.modulation} ${meta.subType}`.trim());
	}

	private onAudioData(data: AudioDataLike): void {
		try {
			const frames = data.numberOfFrames;
			const plane = new Float32Array(frames);
			const pcm = new Float32Array(frames);
			for (let c = 0; c < data.numberOfChannels; c++) {
				data.copyTo(plane, { planeIndex: c, format: 'f32-planar' });
				for (let i = 0; i < frames; i++) pcm[i] += plane[i] / data.numberOfChannels;
			}
			this.schedule(pcm, data.sampleRate);
			this.handlers.onLevel?.(rms(pcm));
		} finally {
			data.close();
		}
	}

	/**
	 * Schedules one decoded chunk at a monotonic playhead ~120 ms
	 * ahead of real time. If playout fell behind (tab throttled), the
	 * playhead resyncs forward — missed audio is dropped, never
	 * replayed (§10.4.3 UDP-like tolerance).
	 */
	private schedule(pcm: Float32Array<ArrayBuffer>, sampleRate: number): void {
		const now = this.ctx.currentTime;
		if (this.playhead < now) this.playhead = now + MIN_LATENCY_S;
		if (this.playhead > now + MAX_AHEAD_S) return; // runaway decoder — drop
		const buf = this.ctx.createBuffer(1, pcm.length, sampleRate);
		buf.copyToChannel(pcm, 0);
		const src = this.ctx.createBufferSource();
		src.buffer = buf;
		src.connect(this.ctx.destination);
		src.start(this.playhead);
		this.playhead += pcm.length / sampleRate;
	}
}

/** RMS of a PCM chunk, clamped to [0, 1]. */
export function rms(pcm: Float32Array): number {
	if (pcm.length === 0) return 0;
	let sum = 0;
	for (let i = 0; i < pcm.length; i++) sum += pcm[i] * pcm[i];
	return Math.min(1, Math.sqrt(sum / pcm.length));
}