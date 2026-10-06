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
	/**
	 * Registered receiver position (§12.1 sdr_devices.lat/lon) — the map
	 * receiver layer plots from this. null = unplaced device (the row
	 * stores 0/0), never plotted.
	 */
	lat: number | null;
	lon: number | null;
}

/** Maps one db.SDRDevice JSON row (§12.1) to the SDRStatus shape. */
function mapSdrDevice(d: any): SDRStatus {
	// The row stores 0/0 for "no position"; null island has no receivers.
	const hasPos = d.lat != null && d.lon != null && !(Number(d.lat) === 0 && Number(d.lon) === 0);
	return {
		id: String(d.id),
		model: String(d.model ?? ''),
		freqHz: Number(d.freqHz ?? 0),
		gainDb: Number(d.gainDb ?? 0),
		bwHz: 0,
		active: Boolean(d.active),
		lat: hasPos ? Number(d.lat) : null,
		lon: hasPos ? Number(d.lon) : null
	};
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
	| 'signal.tdoa'
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

/**
 * §14.2/§9.6 signal.tdoa payload — mirrors `tdoaEventPayload` in
 * cmd/signal-processor/tdoa.go exactly. Every solve attempt arrives:
 * an accepted fix (`fix` set), an ungated solve (`locus` set — the
 * hyperbolic arc), or a rejection (`reason` set). Coordinates are
 * WGS84 degrees; residualNs is the fix's pairwise residual in ns.
 */
export interface TDOAFix {
	lat: number;
	lng: number;
	residualNs: number;
	pairsUsed: number;
	maxBaselineM: number;
	covPosDef: boolean;
}

export interface TDOALocus {
	lat1: number;
	lng1: number;
	lat2: number;
	lng2: number;
}

export interface TDOAEvent {
	signalId: string;
	freqHz: number;
	at: string;
	accepted: boolean;
	persisted: boolean;
	reason?: string;
	reference?: string;
	receivers: string[];
	fix?: TDOAFix;
	locus?: TDOALocus;
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
	return devices.map(mapSdrDevice);
}

/**
 * Partial device update shared by retuneSdr / setGain: PUT /api/sdrs/{id}
 * (§7.4, §13.1). The gateway persists the row AND forwards the change
 * to the sdr-capture control API — capture unreachable ⇒ 502, unknown
 * device there ⇒ 404 — so success means the hardware really moved.
 * Returns the updated device mapped to the dashboard shape.
 */
async function putSdr(id: string, body: Record<string, number>): Promise<SDRStatus> {
	const res = await fetch(`${API_URL}/api/sdrs/${encodeURIComponent(id)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return mapSdrDevice(await res.json());
}

/**
 * Retunes a device (§7.4). Note: a manual tune pauses that device's
 * scan loop until restart (§7.4).
 */
export async function retuneSdr(id: string, freqHz: number): Promise<SDRStatus> {
	return putSdr(id, { freqHz });
}

/**
 * Sets a device's gain in dB (§13.1 — the PUT endpoint has always
 * accepted gainDb; this is the first client function to expose it).
 * Unlike a retune, a gain change does not park the sweep.
 */
export async function setGain(id: string, gainDb: number): Promise<SDRStatus> {
	return putSdr(id, { gainDb });
}

/**
 * Live per-device state from the capture control API, relayed by the
 * gateway GET /api/sdrs/{id}/status (§7.4). Adds the scan-loop fields
 * the §14.4.3 sdr.status WS feed doesn't carry: `scanning` (a D3 sweep
 * loop is attached) and `scanPaused` (parked — boot park via
 * scan_autostart: false, or a manual tune).
 */
export interface SDRDeviceStatus {
	id: string;
	model: string;
	active: boolean;
	freqHz: number;
	gainDb: number;
	bwHz: number;
	mode: string;
	scanning: boolean;
	scanPaused: boolean;
}

/** Maps the capture control API's snake_case status JSON (§7.4). */
function mapDeviceStatus(s: any): SDRDeviceStatus {
	return {
		id: String(s.id),
		model: String(s.model ?? ''),
		active: Boolean(s.active),
		freqHz: Number(s.freq_hz ?? 0),
		gainDb: Number(s.gain_db ?? 0),
		bwHz: Number(s.bw_hz ?? 0),
		mode: String(s.mode ?? ''),
		scanning: Boolean(s.scanning),
		scanPaused: Boolean(s.scan_paused)
	};
}

/** Fetches one device's live capture status (§7.4, §13.1). */
export async function fetchSDRStatus(id: string): Promise<SDRDeviceStatus> {
	const res = await fetch(`${API_URL}/api/sdrs/${encodeURIComponent(id)}/status`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return mapDeviceStatus(await res.json());
}

/**
 * Parks or resumes a device's sweep at runtime: POST
 * /api/sdrs/{id}/scan {"enabled"} (§7.4). The gateway forwards to
 * capture's /api/v1/scan and relays the device's live status. Resuming
 * continues the sweep from the currently tuned frequency; the park
 * resets on capture restart. Unknown device ⇒ 404, device without a
 * scan loop (mode monitor) ⇒ 409, capture unreachable ⇒ 502.
 */
export async function setScan(id: string, enabled: boolean): Promise<SDRDeviceStatus> {
	const res = await fetch(`${API_URL}/api/sdrs/${encodeURIComponent(id)}/scan`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ enabled })
	});
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return mapDeviceStatus(await res.json());
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

/**
 * Lists recordings (§12.3, §13.1). The endpoint takes an optional
 * `?signalId=` filter and `?limit=` (API cap 500); the client defaults
 * to the latest 50. Returns newest first.
 */
export async function fetchRecordings(
	opts: { signalId?: string; limit?: number } = {}
): Promise<Recording[]> {
	const params = new URLSearchParams();
	if (opts.signalId) params.set('signalId', opts.signalId);
	params.set('limit', String(opts.limit ?? 50));
	const res = await fetch(`${API_URL}/api/recordings?${params}`);
	if (!res.ok) throw new Error(`API error: ${res.status}`);
	return res.json();
}

/**
 * WAV/IQ audio stream URL for one recording (§11): the playback and
 * download actions share this gateway-routed endpoint (A3).
 */
export function recordingAudioUrl(recordingId: string): string {
	return `${API_URL}/api/recordings/${encodeURIComponent(recordingId)}/audio`;
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
