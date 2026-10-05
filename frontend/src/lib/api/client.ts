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
	/**
	 * §5.6: true only when powerDbm is a calibrated absolute level.
	 * false (uncalibrated SDR) means powerDbm is relative dB — the UI
	 * must label it "dB (rel.)", never "dBm".
	 */
	powerCalibrated: boolean;
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

export type WSEventType =
	| 'signal.new'
	| 'signal.update'
	| 'signal.removed'
	| 'sdr.status'
	| 'audio.level'
	| 'track.update'
	| 'spectrum.frame';

export interface WSEvent {
	type: WSEventType;
	payload: any;
}

/**
 * §18.2 spectrum.frame payload (camelCase per §12.7). db[0] is the
 * lowest-frequency bin of the span (freqHz − sampleRate/2); db values
 * are §5.6 uncalibrated relative dB — the axis is "dB (rel.)", never
 * "dBm".
 */
export interface SpectrumFrame {
	sdrId: string;
	freqHz: number;
	sampleRate: number;
	t: string;
	bins: number;
	df: number;
	db: number[];
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
 * Retunes a device: PUT /api/sdrs/{id} with {freqHz} (§7.4, §13.1).
 * The gateway persists the row AND forwards the tune to the
 * sdr-capture control API — capture unreachable ⇒ 502, unknown
 * device there ⇒ 404 — so success means the hardware really moved.
 * Returns the updated device mapped to the dashboard shape. Note:
 * a manual tune pauses that device's scan loop until restart (§7.4).
 */
export async function retuneSdr(id: string, freqHz: number): Promise<SDRStatus> {
	const res = await fetch(`${API_URL}/api/sdrs/${encodeURIComponent(id)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ freqHz })
	});
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	const d = await res.json();
	return {
		id: String(d.id),
		model: String(d.model),
		freqHz: Number(d.freqHz ?? 0),
		gainDb: Number(d.gainDb ?? 0),
		bwHz: 0,
		active: Boolean(d.active)
	};
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

/**
 * §10.4 live audio WebSocket URL: /ws/audio?signal=<id>, relayed by
 * the same api-gateway origin as the events stream (A3). Derived from
 * the events WS base so VITE_WS_URL keeps working (…/ws → …/ws/audio).
 */
export function audioWsUrl(signalId: string): string {
	const base = WS_URL.replace(/\/ws\/?$/, '/ws/audio');
	return `${base}?signal=${encodeURIComponent(signalId)}`;
}

export interface Annotation {
	id: string;
	signalId: string;
	userNote: string;
	createdAt: string;
}

/** Lists a signal's user notes, newest first (§12.5). */
export async function fetchAnnotations(signalId: string): Promise<Annotation[]> {
	const res = await fetch(`${API_URL}/api/signals/${signalId}/annotations`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/** Appends a user note to a signal; returns the created row (§12.5). */
export async function addAnnotation(signalId: string, userNote: string): Promise<Annotation> {
	const res = await fetch(`${API_URL}/api/signals/${signalId}/annotations`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ userNote })
	});
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

export interface TrackPoint {
	lat: number;
	lon: number;
}

/** Persisted track: GET /api/signals/{id}/track (§9.4, §12.4). */
export interface SignalTrack {
	signalId: string;
	path: TrackPoint[];
	speedKmh: number;
	headingDeg: number;
	updatedAt: string;
}

/** Fetches a signal's current track (path + movement state). */
export async function fetchTrack(signalId: string): Promise<SignalTrack> {
	const res = await fetch(`${API_URL}/api/signals/${signalId}/track`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/** One `recordings` row (§12.3). */
export interface Recording {
	id: string;
	signalId: string;
	startTime: string;
	endTime: string;
	durationS: number;
	sampleRate: number;
	centerFreq: number;
	bandwidthHz: number;
	filePath: string;
	fileFormat: string;
	sizeBytes: number;
}

/** Lists recordings, optionally for one signal, newest first (§12.3). */
export async function fetchRecordings(signalId: string): Promise<Recording[]> {
	const res = await fetch(
		`${API_URL}/api/recordings?${new URLSearchParams({ signalId, limit: '20' })}`
	);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/**
 * §19.3 TFR request body. t0/t1 are seconds within the recording;
 * freqSpan is absolute Hz (recording center ± sampleRate/2).
 */
export interface TFRRequest {
	method: 'stft' | 'reassigned' | 'spwvd' | 'cwt-morlet';
	t0?: number;
	t1?: number;
	nfft: number;
	overlap?: number;
	window?: string;
	freqSpan?: [number, number];
}

/**
 * §19.3 response: numeric tiles (row-major, row 0 = freqLoHz, col 0 =
 * t0; int8 dB relative to dbRef at 1 dB/LSB, floor −128) plus metadata
 * naming the §19.2 dominant artifact.
 */
export interface TFRResult {
	recordingId: string;
	method: TFRRequest['method'];
	window?: string;
	t0: number;
	t1: number;
	nfft: number;
	hop: number;
	overlap: number;
	sampleRate: number;
	centerFreq: number;
	freqLoHz: number;
	freqHiHz: number;
	rows: number;
	cols: number;
	dbRef: number;
	dbStep: number;
	tile: number[];
	artifact: string;
	elapsedMs: number;
	note?: string;
}

/**
 * §19.4: requests a render through the gateway proxy (§19.3). Errors
 * carry the server's message — 404 covers both `tfr.enabled: false`
 * (feature absent) and unknown/purged recordings; 413 is the span
 * cap; 400 is a parameter the picker should correct.
 */
export async function requestTFR(recordingId: string, req: TFRRequest): Promise<TFRResult> {
	const res = await fetch(
		`${API_URL}/api/recordings/${encodeURIComponent(recordingId)}/tfr`,
		{
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(req)
		}
	);
	if (!res.ok) {
		let msg = `API error: ${res.status}`;
		try {
			const body = await res.json();
			if (body?.error) msg = body.error;
		} catch {
			// non-JSON error body — keep the status line
		}
		throw new Error(msg);
	}
	return res.json();
}
// ─── §20 setup screen ───

/** One editable scalar in a §20 section schema (server-authoritative). */
export interface SettingsField {
	key: string;
	label: string;
	type: 'number' | 'string' | 'bool' | 'select';
	group: string;
	unit?: string;
	step?: number;
	min?: number;
	max?: number;
	options?: string[];
	default?: unknown;
	integer?: boolean;
	optional?: boolean;
	help?: string;
	warning?: string;
}

/** One editable YAML list (the SDR devices). */
export interface SettingsListField {
	key: string;
	label: string;
	itemLabelKey: string;
	help?: string;
	itemFields: SettingsField[];
}

/** One editable config file (§20 section). */
export interface SettingsSection {
	id: string;
	label: string;
	file: string;
	description: string;
	restart: string[];
	fields: SettingsField[];
	lists?: SettingsListField[];
}

export type SettingsValues = Record<string, unknown>;

/** GET /api/settings: schema plus current per-section values. */
export interface SettingsIndex {
	sections: SettingsSection[];
	values: Record<string, SettingsValues | null>;
}

/** SaveResult from PUT /api/settings/{section}. */
export interface SaveResult {
	section: string;
	file: string;
	restart: string[];
}

/** GET /api/setup/status component probes (§13 exception). */
export interface SetupStatus {
	components: Record<string, { ok: boolean; detail?: string }>;
	all_ok: boolean;
	config_dir_writable: boolean;
}

/** 400 from a section save: carries the offending dotted key. */
export class SettingsValidationError extends Error {
	field: string;
	constructor(message: string, field: string) {
		super(message);
		this.field = field;
	}
}

/** Fetches the whole editable surface: schema + current values. */
export async function fetchSettings(): Promise<SettingsIndex> {
	const res = await fetch(`${API_URL}/api/settings`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/** Fetches one section: schema plus current values (§20). */
export async function fetchSettingsSection(section: string): Promise<{ section: SettingsSection; values: SettingsValues | null }> {
	const res = await fetch(`${API_URL}/api/settings/${encodeURIComponent(section)}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/**
 * Saves one section (§20). A 400 answer is a validation rejection:
 * throws SettingsValidationError carrying the server error text and
 * the offending dotted field key for inline highlighting.
 */
export async function saveSettings(section: string, values: SettingsValues): Promise<SaveResult> {
	const res = await fetch(`${API_URL}/api/settings/${encodeURIComponent(section)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ values })
	});
	if (!res.ok) {
		let msg = `API error: ${res.status}`;
		let field = '';
		try {
			const body = await res.json();
			if (body?.error) msg = body.error;
			if (body?.field) field = body.field;
		} catch {
			// non-JSON error body — keep the status line
		}
		throw new SettingsValidationError(msg, field);
	}
	// §20 body shape: {result, values} — callers want the result.
	const body = (await res.json()) as { result: SaveResult };
	return body.result;
}

/** Probes db/ws-hub/recorder/capture concurrently (2 s cap each). */
export async function fetchSetupStatus(): Promise<SetupStatus> {
	const res = await fetch(`${API_URL}/api/setup/status`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/** Reads the §20 first-run flag (503 when the DB is down). */
export async function fetchSetupState(): Promise<{ first_run: boolean }> {
	const res = await fetch(`${API_URL}/api/setup/state`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/** Completes (firstRun=false) or re-arms (firstRun=true) the wizard. */
export async function completeSetup(firstRun: boolean): Promise<{ first_run: boolean }> {
	const res = await fetch(`${API_URL}/api/setup/state`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ first_run: firstRun })
	});
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}
