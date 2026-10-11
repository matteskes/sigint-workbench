/// <reference types="node" />
/**
 * Gateway & ws-hub test helpers for the Playwright e2e suite.
 *
 * Architecture notes (SPEC §2.2, §13, §14):
 * - The api-gateway (:8080) is the single client ingress.  REST lives at
 *   `/api/*`, WebSockets at `/ws` and `/ws/audio`.  It has **no**
 *   `/api/events` route — event ingestion is a ws-hub (:8081) endpoint.
 * - `POST :8081/api/events` is **broadcast-only**: the hub relays the
 *   event to connected WebSocket clients and never persists it.  Tests
 *   that need persisted signals must drive the real pipeline
 *   (`iq-ingest` + `signal-processor`); anything asserted against
 *   `/api/signals` after a `postEvent` would be testing nothing.
 * - `postEvent` therefore runs Node-side (the hub has no CORS
 *   middleware, so browser-side POSTs would die in preflight).
 *
 * WebSocket frames are observed with an in-page collector installed via
 * `page.evaluate` — every helper passes its data in through arguments,
 * never through closures (closures referencing Node scope throw
 * ReferenceError inside the browser).
 *
 * @module gateway_helpers
 */
import http from 'node:http';
import type { Page } from '@playwright/test';

export const GATEWAY_HTTP = 'http://localhost:8080';
export const HUB_HTTP = 'http://localhost:8081';
/** Same-origin WS ingress — goes through the Vite proxy to the gateway. */
export const FRONTEND_WS = 'ws://localhost:5173';

/** One observed WebSocket lifecycle event inside the browser. */
export interface WsLog {
  type: 'open' | 'message' | 'close' | 'error';
  code?: number;
  reason?: string;
  body?: string;
}

/** Event types the ws-hub ingest endpoint accepts (cmd/ws-hub/main.go). */
export type HubEventType =
  | 'signal.new'
  | 'signal.update'
  | 'signal.removed'
  | 'signal.tdoa'
  | 'sdr.status'
  | 'audio.level'
  | 'track.update'
  | 'spectrum.frame';

// ─────────────────────────────────────────────────────────────────────
// Node-side HTTP helpers
// ─────────────────────────────────────────────────────────────────────

/** GET a JSON URL Node-side (no CORS constraints apply). */
export async function getJson(
  url: string
): Promise<{ status: number; body: unknown }> {
  try {
    const resp = await fetch(url);
    const body = await resp.json().catch(() => null);
    return { status: resp.status, body };
  } catch {
    return { status: 0, body: null };
  }
}

/**
 * Publish an event to the ws-hub ingest endpoint (`POST :8081/api/events`).
 * Node-side on purpose: the hub speaks to pipeline services, not
 * browsers, and has no CORS middleware.  Returns the HTTP status so the
 * caller can assert `200` — the old suite silently swallowed 404s here.
 *
 * Broadcast-only (§2.2): the event is relayed to `/ws` clients, never
 * written to the database.
 */
export async function postEvent(
  type: HubEventType,
  payload: unknown,
  port = 8081
): Promise<{ status: number; body: string }> {
  try {
    const resp = await fetch(`http://localhost:${port}/api/events`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type, payload }),
    });
    const body = await resp.text();
    return { status: resp.status, body };
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : 'unknown';
    return { status: 0, body: `error:${message}` };
  }
}

/** Gateway (`:8080/health`) + ws-hub (`:8081/health`) both reachable. */
export async function servicesReady(): Promise<boolean> {
  const [gw, hub] = await Promise.all([
    getJson(`${GATEWAY_HTTP}/health`),
    getJson(`${HUB_HTTP}/health`),
  ]);
  return gw.status === 200 && hub.status === 200;
}


/**
 * Whether the recorder service is answering behind the gateway.  The
 * gateway maps an unreachable recorder to 502 on proxied REST routes
 * (§13), so `GET /api/recordings` is a cheap recorder health probe.
 */
export async function recorderReady(): Promise<boolean> {
  const { status } = await getJson(`${GATEWAY_HTTP}/api/recordings`);
  return status === 200;
}

/**
 * Issue a raw HTTP request with WebSocket upgrade headers from Node.
 * Unlike a browser `WebSocket`, this exposes the real HTTP response for
 * failed handshakes — 502 with the JSON error body (§13) or the hub's
 * 403 origin rejection (§17.2) — which the browser API hides behind a
 * synthetic 1006 close.
 */
export function rawHandshake(
  urlStr: string,
  origin?: string
): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    const u = new URL(urlStr);
    const headers: Record<string, string> = {
      Connection: 'Upgrade',
      Upgrade: 'websocket',
      'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==',
      'Sec-WebSocket-Version': '13',
    };
    if (origin) headers.Origin = origin;
    const req = http.request(
      {
        hostname: u.hostname,
        port: u.port,
        path: u.pathname + u.search,
        headers,
        timeout: 5_000,
      },
      (res) => {
        let body = '';
        res.on('data', (chunk: Buffer) => (body += chunk.toString()));
        res.on('end', () => resolve({ status: res.statusCode ?? 0, body }));
      }
    );
    // A successful upgrade answers 101 via the 'upgrade' event — the
    // 'response' handler above never fires for it.
    req.on('upgrade', (res, socket) => {
      socket.destroy();
      resolve({ status: res.statusCode ?? 0, body: '' });
    });
    req.on('timeout', () => req.destroy(new Error('handshake timeout')));
    req.on('error', reject);
    req.end();
  });
}

// ─────────────────────────────────────────────────────────────────────
// In-page WebSocket collector
// ─────────────────────────────────────────────────────────────────────

/**
 * Open a WebSocket inside the browser page and record every lifecycle
 * event (open/message/close/error) into `window.__wsLogs[id]`.  Returns
 * the collector id used to read the log back.
 *
 * The collector is installed with `page.evaluate` and receives `id` and
 * `url` as arguments — the function body runs in the browser and cannot
 * close over Node scope.
 */
export async function installWsCollector(
  page: Page,
  url: string
): Promise<string> {
  const id = 'ws-' + Math.random().toString(36).slice(2, 10);
  await page.evaluate(({ collectorId, wsUrl }) => {
    const w = window as unknown as {
      __wsLogs?: Record<
        string,
        Array<{ type: string; code?: number; reason?: string; body?: string }>
      >;
    };
    const logs: Array<{
      type: string;
      code?: number;
      reason?: string;
      body?: string;
    }> = [];
    (w.__wsLogs ??= {})[collectorId] = logs;
    const log = (entry: {
      type: string;
      code?: number;
      reason?: string;
      body?: string;
    }) => logs.push(entry);
    try {
      const ws = new WebSocket(wsUrl);
      ws.addEventListener('open', () => {
        log({ type: 'open' });
      });
      ws.addEventListener('message', (event: MessageEvent) => {
        if (typeof event.data === 'string') {
          log({ type: 'message', body: event.data });
        } else {
          // Binary frame (e.g. Opus packets on /ws/audio).
          const size =
            event.data instanceof Blob
              ? event.data.size
              : (event.data as ArrayBuffer).byteLength;
          log({ type: 'message', body: `<binary ${size} bytes>` });
        }
      });
      ws.addEventListener('close', (event: CloseEvent) => {
        log({ type: 'close', code: event.code, reason: event.reason });
      });
      ws.addEventListener('error', () => {
        log({ type: 'error', reason: 'handshake failed' });
      });
    } catch (err: unknown) {
      log({ type: 'error', reason: String(err) });
    }
  }, { collectorId: id, wsUrl: url });
  return id;
}

/** Read a collector's log back into Node. */
export async function wsLogs(page: Page, id: string): Promise<WsLog[]> {
  return page.evaluate((collectorId) => {
    const w = window as unknown as { __wsLogs?: Record<string, WsLog[]> };
    return w.__wsLogs?.[collectorId] ?? [];
  }, id);
}

// ─────────────────────────────────────────────────────────────────────
// Payload factory
// ─────────────────────────────────────────────────────────────────────

export interface SignalOverrides {
  id?: string;
  freqHz?: number;
  bandwidthHz?: number;
  modulation?: string;
  subType?: string;
  class?: string;
  confidence?: number;
  powerDbm?: number;
  powerCalibrated?: boolean;
  lat?: number | null;
  lon?: number | null;
  sdrId?: string;
  verified?: boolean;
}

/**
 * Build a `signal.new` / `signal.update` payload with the exact schema
 * the frontend store consumes (`frontend/src/lib/stores/signals.ts`):
 * frequency is **`freqHz`** — not `frequencyHz`, which the store would
 * silently render as NaN.
 */
export function signalPayload(
  o: SignalOverrides = {}
): Record<string, unknown> {
  const now = new Date().toISOString();
  return {
    id: o.id ?? crypto.randomUUID(),
    freqHz: o.freqHz ?? 146_520_000,
    bandwidthHz: o.bandwidthHz ?? 12_000,
    modulation: o.modulation ?? 'FM',
    subType: o.subType ?? 'NFM',
    class: o.class ?? 'land_mobile',
    confidence: o.confidence ?? 0.85,
    powerDbm: o.powerDbm ?? -75,
    powerCalibrated: o.powerCalibrated ?? false,
    lat: o.lat ?? null,
    lon: o.lon ?? null,
    accuracyM: o.lat != null && o.lon != null ? 50 : 0,
    firstSeen: now,
    lastSeen: now,
    sdrId: o.sdrId ?? 'S1',
    verified: o.verified ?? false,
  };
}

/** Format a frequency exactly like the UI's `freqHz()` helper (§4). */
export function freqLabel(hz: number): string {
  if (hz >= 1e9) return `${(hz / 1e9).toFixed(3)} GHz`;
  if (hz >= 1e6) return `${(hz / 1e6).toFixed(3)} MHz`;
  return `${(hz / 1e3).toFixed(1)} kHz`;
}

/**
 * Poll a collector until an entry matching `predicate` shows up.
 * Returns the entry, or `null` on timeout — callers must assert on the
 * result (`expect(hit).not.toBeNull()`), never on a derived value that
 * would pass vacuously when the frame never arrived.
 */
export async function waitForWsLog(
  page: Page,
  id: string,
  predicate: (entry: WsLog) => boolean,
  timeoutMs = 5_000
): Promise<WsLog | null> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const hit = (await wsLogs(page, id)).find(predicate);
    if (hit) return hit;
    if (Date.now() >= deadline) return null;
    await page.waitForTimeout(250);
  }
}

/**
 * Predicate for `waitForWsLog` / `wsLogs` filters: a text frame whose
 * body contains `needle` (e.g. a signal id).  The `?? false` keeps the
 * return type strictly `boolean` — `body?.includes()` alone widens to
 * `boolean | undefined` and fails to type-check.
 */
export function messageContaining(needle: string): (entry: WsLog) => boolean {
  return (entry) =>
    entry.type === 'message' && (entry.body?.includes(needle) ?? false);
}

