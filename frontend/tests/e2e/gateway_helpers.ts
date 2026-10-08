/**
 * Gateway test helpers for Playwright e2e tests.
 *
 * These utilities create raw WebSocket connections from the browser to the
 * api-gateway (`localhost:8080`) — bypassing the SvelteKit frontend layer — so
 * tests can validate the relay chain: client → api-gateway (relay) →
 * ws-hub / recorder (backend).
 *
 * @module gateway_helpers
 */

/** Internal state communicated back to the test via console messages. */
interface WsLog {
  type: 'message' | 'close' | 'error' | 'open' | '502';
  code?: number;
  reason?: string;
  body?: string;
}

/**
 * Connect to a WebSocket URL from the browser context and log all frames
 * back to the caller's `logs` array.  Returns a unique handle so the test
 * can identify which connection the log entries belong to.
 *
 * ```ts
 * const logs: WsLog[] = [];
 * const { id } = await page.evaluate(connectToGateway, [{ url, logs }]);
 * ```
 */
export function connectToGateway(
  opts: { url: string; logs: WsLog[] }
): { id: string } {
  const id = 'ws-' + Math.random().toString(36).slice(2, 8);
  const { url, logs } = opts;

  const log = (entry: Omit<WsLog, 'url'>) => {
    logs.push(entry);
  };

  try {
    const ws = new WebSocket(url);

    ws.addEventListener('open', () => {
      log({ type: 'open', code: ws.readyState });
    });

    ws.addEventListener('message', (event: MessageEvent) => {
      if (typeof event.data === 'string') {
        log({ type: 'message', body: event.data });
      } else {
        // Binary frame (e.g. Opus packets from /ws/audio)
        const buf = new Uint8Array(event.data);
        log({ type: 'message', body: `<binary ${buf.length} bytes>` });
      }
    });

    ws.addEventListener('close', (event: CloseEvent) => {
      log({
        type: 'close',
        code: event.code,
        reason: event.reason,
      });
    });

    ws.addEventListener('error', () => {
      log({ type: 'error' });
    });

    // Detect 502 non-101 HTTP response by checking readyState after a
    // short delay — a 502 response means WebSocket() never upgraded and
    // the readyState stays CLOSED (0).  The browser doesn't fire a
    // `message` event for failed handshakes, so we use a heuristic: if
    // after 1s we still haven't seen an 'open' log, check readyState.
    setTimeout(() => {
      if (ws.readyState === WebSocket.CLOSED) {
        const isOpened = logs.some((l) => l.type === 'open');
        if (!isOpened) {
          log({ type: '502' });
        }
      }
    }, 1_500);
  } catch (_err: unknown) {
    log({ type: 'error' });
  }

  return { id };
}

/**
 * Publish a signal event to the gateway's event relay, which forwards it
 * to the ws-hub.  This is how tests simulate the signal-processor posting
 * detections (`signal.new`, `signal.update`, etc.).
 *
 * ```ts
 * await page.evaluate(publishSignalEvent, [{
 *   type: 'signal.new', payload: { id: 'sig-1', freqHz: 146_520_000 },
 * }]);
 * ```
 */
export async function publishSignalEvent(
  payload: Record<string, unknown>
): Promise<string> {
  try {
    const resp = await fetch('http://localhost:8080/api/events', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const body = await resp.text();
    return `${resp.status}:${body}`;
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : 'unknown';
    return `error:${message}`;
  }
}

/**
 * Check whether the gateway and backend services are reachable.
 * Returns `true` if the gateway's health endpoint responds with 200.
 */
export async function servicesReady(): Promise<boolean> {
  try {
    const resp = await fetch('http://localhost:8080/health');
    return resp.ok;
  } catch {
    return false;
  }
}