import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fetchSignals, fetchSignal, fetchSDRs, retuneSdr, setGain, fetchAnnotations, addAnnotation, connectWebSocket, fetchRecordings, requestTFR, fetchSettings, saveSettings, fetchSetupState, completeSetup, fetchSetupStatus } from './client';

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

describe('annotations (§12.5)', () => {
	it('fetches notes from the annotations endpoint', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON([{ id: 'a-1', signalId: 'sig-1', userNote: 'part 90 traffic', createdAt: '2026-10-04T12:00:00Z' }])
		);
		vi.stubGlobal('fetch', fetchMock);

		const notes = await fetchAnnotations('sig-1');

		expect(notes).toHaveLength(1);
		expect(notes[0].userNote).toBe('part 90 traffic');
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/signals/sig-1/annotations');
	});

	it('posts a note with a JSON body and returns the created row', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({ id: 'a-2', signalId: 'sig-1', userNote: 'monitor', createdAt: '2026-10-04T12:01:00Z' })
		);
		vi.stubGlobal('fetch', fetchMock);

		const created = await addAnnotation('sig-1', 'monitor');

		expect(created.id).toBe('a-2');
		const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
		expect(url).toContain('/api/signals/sig-1/annotations');
		expect(init.method).toBe('POST');
		expect(init.headers).toEqual({ 'Content-Type': 'application/json' });
		expect(JSON.parse(init.body as string)).toEqual({ userNote: 'monitor' });
	});

	it('throws on non-OK response', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({ ok: false, status: 404, json: async () => ({}) } as Response)
		);
		await expect(fetchAnnotations('sig-1')).rejects.toThrow('API error: 404');
		await expect(addAnnotation('sig-1', 'x')).rejects.toThrow('API error: 404');
	});
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

	it('passes through the §5.6 calibrated power fields', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON([
				{ id: 'calibrated', powerDbm: -82.5, powerCalibrated: true },
				{ id: 'relative', powerDbm: -50, powerCalibrated: false }
			])
		);
		vi.stubGlobal('fetch', fetchMock);

		const signals = await fetchSignals();

		expect(signals[0].powerCalibrated).toBe(true);
		expect(signals[1].powerCalibrated).toBe(false);
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
			{ id: 'rtlsdr-0', model: 'RTL2832U', freqHz: 146_520_000, gainDb: 40, bwHz: 0, active: true, lat: 40.7, lon: -74.0 },
			{ id: 'simulator-0', model: 'Simulator', freqHz: 0, gainDb: 40, bwHz: 0, active: false, lat: null, lon: null }
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

describe('retuneSdr (§7.4, §13.1)', () => {
	it('PUTs {freqHz} to the sdr endpoint and returns the updated device', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({ id: 'rtlsdr-0', model: 'RTL2832U', serial: '0001', gainDb: 40, freqHz: 121_500_000, active: true })
		);
		vi.stubGlobal('fetch', fetchMock);

		const sdr = await retuneSdr('rtlsdr-0', 121_500_000);

		expect(sdr).toEqual({
			id: 'rtlsdr-0',
			model: 'RTL2832U',
			freqHz: 121_500_000,
			gainDb: 40,
			bwHz: 0,
			active: true,
			lat: null,
			lon: null
		});
		const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
		expect(url).toContain('/api/sdrs/rtlsdr-0');
		expect(init.method).toBe('PUT');
		expect(init.headers).toEqual({ 'Content-Type': 'application/json' });
		expect(JSON.parse(init.body as string)).toEqual({ freqHz: 121_500_000 });
	});

	it('encodes the device id into the path', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({ id: 'a b/c', model: 'm', freqHz: 1, gainDb: 0, active: false })
		);
		vi.stubGlobal('fetch', fetchMock);

		await retuneSdr('a b/c', 100_000_000);

		const url = fetchMock.mock.calls[0][0] as string;
		expect(url).toContain('/api/sdrs/a%20b%2Fc');
	});

	it('throws on non-OK response (capture unreachable ⇒ 502)', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({ ok: false, status: 502, json: async () => ({}) } as Response)
		);
		await expect(retuneSdr('rtlsdr-0', 100_000_000)).rejects.toThrow('API error: 502');
	});
});

describe('recordings + TFR (§19)', () => {
	it('fetchRecordings queries by signalId and defaults the limit', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON([
				{
					id: 'rec-1',
					signalId: 'sig-1',
					startTime: '2026-10-05T12:00:00Z',
					endTime: '2026-10-05T12:01:00Z',
					durationS: 60,
					sampleRate: 1_024_000,
					centerFreq: 146_000_000,
					bandwidthHz: 12_500,
					filePath: '/recordings/r.iq',
					fileFormat: 'iq',
					sizeBytes: 245_760_000
				}
			])
		);
		vi.stubGlobal('fetch', fetchMock);

		const recs = await fetchRecordings({ signalId: 'sig-1' });

		expect(recs).toHaveLength(1);
		expect(recs[0].fileFormat).toBe('iq');
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/recordings');
		expect(url.searchParams.get('signalId')).toBe('sig-1');
		expect(url.searchParams.get('limit')).toBe('50');
	});

	it('fetchRecordings sends a raised limit and omits signalId when unset', async () => {
		const fetchMock = vi.fn().mockResolvedValue(okJSON([]));
		vi.stubGlobal('fetch', fetchMock);

		await fetchRecordings({ limit: 500 });

		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.searchParams.get('signalId')).toBeNull();
		expect(url.searchParams.get('limit')).toBe('500');
	});

	it('setGain PUTs the gainDb field and maps the updated device', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({ id: 'rtlsdr-0', model: 'RTL-SDR', freqHz: 145_500_000, gainDb: 32.8, active: true, lat: 37.77, lon: -122.42 })
		);
		vi.stubGlobal('fetch', fetchMock);

		const sdr = await setGain('rtlsdr-0', 32.8);

		expect(sdr.gainDb).toBeCloseTo(32.8);
		const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
		expect(url).toContain('/api/sdrs/rtlsdr-0');
		expect(init.method).toBe('PUT');
		expect(JSON.parse(init.body as string)).toEqual({ gainDb: 32.8 });
	});

	it('requestTFR posts the picker parameters and returns tiles', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({
				recordingId: 'rec-1',
				method: 'stft',
				window: 'hamming',
				t0: 0,
				t1: 4,
				nfft: 1024,
				hop: 256,
				overlap: 0.75,
				sampleRate: 1_024_000,
				centerFreq: 146_000_000,
				freqLoHz: 145_488_000,
				freqHiHz: 146_512_000,
				rows: 64,
				cols: 32,
				dbRef: -12.5,
				dbStep: 1,
				tile: new Array(64 * 32).fill(-40),
				artifact: 'window resolution trade-off',
				elapsedMs: 812
			})
		);
		vi.stubGlobal('fetch', fetchMock);

		const res = await requestTFR('rec-1', {
			method: 'stft',
			t0: 0,
			t1: 4,
			nfft: 1024,
			overlap: 0.75,
			window: 'hamming'
		});

		expect(res.rows * res.cols).toBe(res.tile.length);
		expect(res.artifact).toContain('trade-off');
		const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
		expect(url).toContain('/api/recordings/rec-1/tfr');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body as string)).toMatchObject({ method: 'stft', nfft: 1024 });
	});

	it('surfaces the server error message (404 disabled / 413 span)', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({
				ok: false,
				status: 404,
				json: async () => ({ error: 'tfr feature disabled' })
			} as Response)
		);
		await expect(requestTFR('rec-1', { method: 'stft', nfft: 256 })).rejects.toThrow(
			'tfr feature disabled'
		);

		vi.unstubAllGlobals();
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({
				ok: false,
				status: 413,
				json: async () => ({ error: 'span 31 s exceeds tfr.max_span_s 30' })
			} as Response)
		);
		await expect(
			requestTFR('rec-1', { method: 'stft', nfft: 256, t0: 0, t1: 31 })
		).rejects.toThrow('tfr.max_span_s');
	});

	it('falls back to the status line for non-JSON errors (502)', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({
				ok: false,
				status: 502,
				json: async () => {
					throw new Error('not json');
				}
			} as unknown as Response)
		);
		await expect(requestTFR('rec-1', { method: 'stft', nfft: 256 })).rejects.toThrow('API error: 502');
	});
});
describe('setup screen (§20)', () => {
	it('fetches the settings index', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({
				sections: [
					{
						id: 'ingest',
						label: 'IQ Ingest',
						file: 'iq-ingest.yaml',
						description: '',
						restart: ['iq-ingest'],
						fields: []
					}
				],
				values: { ingest: { 'stats.interval_s': 5 } }
			})
		);
		vi.stubGlobal('fetch', fetchMock);

		const idx = await fetchSettings();

		expect(idx.sections).toHaveLength(1);
		expect((idx.values.ingest as Record<string, unknown>)['stats.interval_s']).toBe(5);
		const url = new URL(fetchMock.mock.calls[0][0] as string);
		expect(url.pathname).toBe('/api/settings');
	});

	it('saves a section and surfaces 400 field errors', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(
				okJSON({
					result: { section: 'processing', file: 'signal-processor.yaml', restart: ['signal-processor'] },
					values: {}
				})
			)
			.mockResolvedValueOnce({
				ok: false,
				status: 400,
				json: async () => ({ error: 'does not divide fft.size', field: 'spectrum.bins' })
			} as Response);
		vi.stubGlobal('fetch', fetchMock);

		const res = await saveSettings('processing', { 'fft.window': 'hann' });
		expect(res.section).toBe('processing');
		const call = fetchMock.mock.calls[0];
		expect((call[1] as RequestInit).method).toBe('PUT');
		expect(JSON.parse((call[1] as RequestInit).body as string).values).toEqual({
			'fft.window': 'hann'
		});

		await expect(saveSettings('processing', { 'spectrum.bins': 300 })).rejects.toMatchObject({
			field: 'spectrum.bins'
		});
		const second = fetchMock.mock.calls[1];
		expect(new URL(second[0] as string).pathname).toBe('/api/settings/processing');
	});

	it('reads and updates the first-run flag', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(okJSON({ first_run: true }))
			.mockResolvedValueOnce(okJSON({ first_run: false }));
		vi.stubGlobal('fetch', fetchMock);
		expect((await fetchSetupState()).first_run).toBe(true);
		await completeSetup(false);
		const put = fetchMock.mock.calls[1];
		expect((put[1] as RequestInit).method).toBe('PUT');
		expect(JSON.parse((put[1] as RequestInit).body as string)).toEqual({ first_run: false });
		expect(new URL(put[0] as string).pathname).toBe('/api/setup/state');
	});

	it('fetches the setup status probe', async () => {
		const fetchMock = vi.fn().mockResolvedValue(
			okJSON({
				components: { db: { ok: true }, 'ws-hub': { ok: false, detail: 'unreachable' } },
				all_ok: false,
				config_dir_writable: true
			})
		);
		vi.stubGlobal('fetch', fetchMock);
		const st = await fetchSetupStatus();
		expect(st.all_ok).toBe(false);
		expect(st.components.db.ok).toBe(true);
	});
});
