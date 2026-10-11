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
 * Whether any capture device is actively streaming.  A live RF pipeline
 * floods the hub at roughly 1600 events/s on this bench, and the hub's
 * §2.2 firehose contract sheds broadcast frames once its fan-out buffer
 * fills — so tests that inject known events and expect each one to be
 * relayed are unrunnable while dongles stream.  Gated tests skip with
 * that reason on an active bench and run on a quiet stack (CI).
 */
export async function pipelineActive(): Promise<boolean> {
  try {
    const resp = await fetch('http://127.0.0.1:9090/api/v1/status');
    if (!resp.ok) return false;
    const devices = (await resp.json()) as Array<{ active?: boolean }>;
    return Array.isArray(devices) && devices.some((d) => d.active === true);
  } catch {
    return false; // capture down → pipeline not flooding
  }
}

/**
 * Whether the recorder service is answering behind the gateway.
 *
 * `GET /api/recordings` cannot be the probe: the gateway serves that
 * list from its own database, so it answers 200 with the recorder
 * stopped — the first e2e-ui CI run stopped the recorder container for
 * phase 2 and every 502 test still skipped (the tests had never
 * actually run anywhere).  Probe the proxied `/ws/audio` upgrade
 * instead: §13 maps an unreachable recorder to **502** on that route,
 * while a live recorder answers the handshake itself (4xx for a bad
 * signal id, 101 for a good one).  A down gateway surfaces as a thrown
 * handshake — the describe-level servicesReady() gate already covers
 * that case for callers inside a gated file.
 */
export async function recorderReady(): Promise<boolean> {
  try {
    const { status } = await rawHandshake(
      `${GATEWAY_HTTP}/ws/audio?signal=recorder-probe`
    );
    return status !== 502;
  } catch {
    return false; // gateway itself unreachable → recorder not reachable
  }
}

/**
 * Guarantee at least one clickable signal row in the SignalTable.
 *
 * On an RF-active bench the table fills by itself.  On a quiet stack
 * (CI: compose without capture) there is nothing to click, so this
 * injects one `signal.new` through the hub ingest endpoint and waits
 * for the row to render via the /ws broadcast.  Injection is skipped
 * while the pipeline is flooding — §2.2 shedding would make the wait a
 * coin flip — and the caller then skips, as before.
 *
 * Returns true when a row exists (pre-existing or injected).
 */
export async function ensureSignalRow(page: Page): Promise<boolean> {
  const rows = page.locator('[role="row"]');
  if ((await rows.count()) > 0) return true;
  if (await pipelineActive()) return false;
  await hubSocketOpen(page); // §2.2: a CONNECTING socket loses the event
  const injected = await postEvent('signal.new', signalPayload());
  if (injected.status !== 200) return false;
  try {
    await rows.first().waitFor({ state: 'visible', timeout: 5_000 });
    return true;
  } catch {
    return false; // hub relayed but the UI never rendered a row
  }
}

// ─────────────────────────────────────────────────────────────────────
// App-socket sync — §2.2 broadcast is fire-and-forget
// ─────────────────────────────────────────────────────────────────────

/**
 * Install an in-page watcher that records the app's own `/ws` socket
 * (opened during hydration, `frontend/src/lib/api/client.ts`).  Must be
 * called *before* the `page.goto()` it should cover: it hooks the
 * `WebSocket` constructor via `addInitScript`, which only runs for
 * later navigations.
 */
export async function watchHubSocket(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { __hubSockets?: WebSocket[] };
    w.__hubSockets = [];
    const Orig = window.WebSocket;
    function Patched(url: string | URL, protocols?: string | string[]) {
      const ws = protocols === undefined ? new Orig(url) : new Orig(url, protocols);
      if (String(url).includes('/ws')) w.__hubSockets!.push(ws);
      return ws;
    }
    Patched.prototype = Orig.prototype;
    window.WebSocket = Patched as unknown as typeof WebSocket;
  });
}

/**
 * Resolve once the page's app-level `/ws` socket is open.  §2.2 relay
 * is broadcast-only: an event published while the socket is still
 * CONNECTING is dropped on the floor and never healed — the §14.3
 * re-bootstrap covers only events missed after a disconnect, and
 * injected events are not persisted anyway.  Under live RF this race
 * hid behind the processor constantly refreshing the table; on a quiet
 * stack (CI) it lost the event 5 times out of 5.  No-op when
 * `watchHubSocket` was not installed.
 */
export async function hubSocketOpen(page: Page): Promise<void> {
  await page.waitForFunction(
    () => {
      const w = window as unknown as { __hubSockets?: WebSocket[] };
      if (!Array.isArray(w.__hubSockets)) return true; // watcher absent
      return w.__hubSockets.some((s) => s.readyState === 1);
    },
    undefined,
    { timeout: 10_000 }
  );
}

/**
 * `page.goto(path)` synchronised with the app's WebSocket handshake —
 * navigation followed by `hubSocketOpen()`.  Injection tests must
 * navigate through this instead of a bare `goto`; see the doc comment
 * on `hubSocketOpen` for why.
 */
export async function gotoSynced(page: Page, path: string): Promise<void> {
  await watchHubSocket(page);
  await page.goto(path);
  await hubSocketOpen(page);
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
  // The collector socket itself must be open before the caller
  // publishes — a §2.2 broadcast fired while it is still CONNECTING
  // would be silently missed and logged as a vacuous timeout.
  await waitForWsLog(page, id, (entry) => entry.type === 'open', 5_000);
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

