// §3.2/§13: the WS lifecycle moves here from +page.svelte so every
// view shares one connection, one ingest pipeline, and one truthful
// state for the connection pill (§1: a dead /ws must never look like
// an empty band). Reconnect + REST re-bootstrap semantics are exactly
// the ones the dashboard shipped (exponential backoff, 15 s cap).

import { writable } from 'svelte/store';
import { connectWebSocket, fetchSignals, fetchSDRs, type WSEvent } from '$lib/api/client';
import { signals, upsertSignals, removeSignal, type Signal } from './signals';
import { sdrs, applySDRStatus } from './sdrs';
import { applyAudioLevel, pruneSignalLevels, clearSignalLevel } from './audio';
import { applyTrackUpdate, clearTrack } from './tracks';
import { applySpectrumFrame } from './spectrum';
import { applyTDOAEvent, clearTDOA } from './tdoa';

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline';

export const connectionState = writable<ConnectionState>('connecting');
export const connectionAttempt = writable(0);
/** Date.now() of the last accepted event — powers "last event Xs ago". */
export const lastEventAt = writable(0);
/**
 * Set when a dropped connection is re-established (not on first
 * connect): the shell shows the one-line "connection restored" banner
 * (F5), self-dismissing.
 */
export const restoredAt = writable(0);

/** Full (re)sync from REST — on boot and after every reconnect, so
 * events missed while disconnected are healed (§14.3). */
export async function bootstrap(): Promise<void> {
	try {
		const [sigs, devices] = await Promise.all([fetchSignals(), fetchSDRs()]);
		signals.set(sigs);
		sdrs.set(devices);
	} catch (err) {
		console.error('bootstrap failed', err);
	}
}

// §14.3 lossy-by-design ingest for signal.new/update: a receiver on a
// busy band emits thousands of detector events per second, and each
// store write re-renders every $signals consumer. Coalesce last-wins
// per id and flush on a fixed UI-rate cadence; missed intermediate
// states self-heal via the next event or the reconnect bootstrap.
const SIGNAL_FLUSH_MS = 200;
let pendingSignals = new Map<string, Signal>();
let signalFlush: ReturnType<typeof setTimeout> | null = null;

function flushPendingSignals(): void {
	signalFlush = null;
	if (pendingSignals.size === 0) return;
	const batch = Array.from(pendingSignals.values());
	pendingSignals.clear();
	upsertSignals(batch);
}

function handleEvent(ev: WSEvent): void {
	lastEventAt.set(Date.now());
	switch (ev.type) {
		case 'signal.new':
		case 'signal.update': {
			const sig = ev.payload as Signal;
			pendingSignals.set(sig.id, sig);
			if (signalFlush === null) {
				signalFlush = setTimeout(flushPendingSignals, SIGNAL_FLUSH_MS);
			}
			break;
		}
		case 'signal.removed':
			if (ev.payload?.id) {
				removeSignal(ev.payload.id);
				clearSignalLevel(ev.payload.id);
				clearTrack(ev.payload.id);
				clearTDOA(ev.payload.id);
			}
			break;
		case 'signal.tdoa':
			// §9.6/§14.2: one multilateration attempt per event (fix,
			// locus, or rejection). Last attempt wins (§14.3).
			applyTDOAEvent(ev.payload);
			break;
		case 'sdr.status':
			applySDRStatus(ev.payload);
			break;
		case 'audio.level':
			// §10.6: coarse level per actively demodulated signal.
			if (ev.payload?.signalId) applyAudioLevel(ev.payload.signalId, ev.payload.level ?? 0);
			break;
		case 'track.update':
			// §9.4: movement summary per located signal.
			if (ev.payload?.signalId) applyTrackUpdate(ev.payload);
			break;
		case 'spectrum.frame':
			// §18: decimated per-SDR spectrum/waterfall feed; malformed
			// frames are no-ops in the store (§14.3).
			applySpectrumFrame(ev.payload);
			break;
	}
}

// ─── Singleton connection lifecycle ──────────────────────────────────

let started = false;
let stopped = false;

/**
 * Opens the event WebSocket (once per app lifetime) and drives the
 * connection store from its lifecycle. Idempotent: later calls are
 * no-ops. Returns a stop function (used by tests; the app never
 * stops the connection across route changes — the layout owns it).
 */
export function startConnection(): () => void {
	if (started) return () => {};
	started = true;
	stopped = false;

	// audio.level entries expire when their feed stops (§10.6).
	const levelPrune = setInterval(() => pruneSignalLevels(), 500);

	let ws: WebSocket | undefined;
	let retries = 0;
	let everConnected = false;

	const connect = () => {
		if (stopped) return;
		ws = connectWebSocket(handleEvent, () => {
			retries = 0;
			connectionAttempt.set(0);
			connectionState.set('live');
			if (everConnected) restoredAt.set(Date.now());
			everConnected = true;
			bootstrap();
		});
		ws.onclose = () => {
			if (stopped) return;
			connectionState.set('reconnecting');
			const delay = Math.min(1000 * 2 ** retries++, 15_000);
			connectionAttempt.set(retries);
			setTimeout(connect, delay);
		};
	};
	connectionState.set('connecting');
	connect();

	return () => {
		stopped = true;
		clearInterval(levelPrune);
		if (signalFlush !== null) clearTimeout(signalFlush);
		ws?.close();
		started = false;
	};
}

/** Test hook: resets the singleton between tests. */
export function resetConnectionForTests(): void {
	started = false;
	stopped = true;
	pendingSignals = new Map();
	if (signalFlush !== null) {
		clearTimeout(signalFlush);
		signalFlush = null;
	}
	connectionState.set('connecting');
	connectionAttempt.set(0);
	lastEventAt.set(0);
	restoredAt.set(0);
}
