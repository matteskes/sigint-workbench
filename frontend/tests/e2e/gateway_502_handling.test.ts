import { test, expect } from '@playwright/test';
import {
  installWsCollector,
  rawHandshake,
  recorderReady,
  servicesReady,
  waitForWsLog,
  wsLogs,
  FRONTEND_WS,
  GATEWAY_HTTP,
} from './gateway_helpers';

/**
 * Test 3.2 — Gateway 502 Handling (§13, §2.2).
 *
 * When the recorder is not running, the gateway relay to
 * `ws://recorder:9012/ws/audio?signal=<id>` must answer the client's
 * WebSocket handshake with a **502 JSON** response
 * (`{"error":"recorder unreachable"}`) — not a raw socket error.
 *
 * Browser `WebSocket` cannot see the HTTP status of a failed handshake,
 * so the body-level assertions use the Node-side `rawHandshake` helper,
 * and the browser-level assertions check the observable behaviour
 * (close without open, no page errors).
 *
 * Spec: §13 (error mapping), §2.2 (unreachable upstream → 502 JSON).
 */
test.describe('Gateway 502 handling for unreachable upstream (§2.2, §13)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping 502 handling test');

  test('gateway rejects the /ws/audio handshake when recorder is down', async ({
    page,
  }) => {
    test.skip(
      (await recorderReady()) === true,
      'recorder is running — the 502 path is only reachable with the recorder stopped (see close_code_forwarding for the running-recorder path)'
    );

    await page.goto('/');
    const id = await installWsCollector(
      page,
      `${FRONTEND_WS}/ws/audio?signal=dead-recorder-test`
    );

    // The handshake must fail: a close (browser failure → 1006) or an
    // error event, and never an open.
    const failure = await waitForWsLog(
      page,
      id,
      (l) => l.type === 'close' || l.type === 'error',
      5_000
    );
    expect(failure).not.toBeNull();

    const logs = await wsLogs(page, id);
    expect(logs.some((l) => l.type === 'open')).toBe(false);
    if (failure?.type === 'close') {
      // 1006 = abnormal closure, i.e. never upgraded.  A clean 1000
      // would mean the recorder actually answered.
      expect(failure.code).toBe(1006);
    }
  });

  test('gateway answers 502 with the JSON recorder-unreachable body', async () => {
    test.skip(
      (await recorderReady()) === true,
      'recorder is running — the 502 path is only reachable with the recorder stopped'
    );

    const res = await rawHandshake(
      `${GATEWAY_HTTP}/ws/audio?signal=502-body-test`
    );
    expect(res.status).toBe(502);

    const body = JSON.parse(res.body) as { error?: string };
    expect(body.error).toBeTruthy();
    expect(body.error).toContain('recorder');
  });

  test('browser survives the failed handshake without page errors', async ({
    page,
  }) => {
    test.skip(
      (await recorderReady()) === true,
      'recorder is running — the 502 path is only reachable with the recorder stopped'
    );

    // Unhandled JS exceptions only — the browser always logs a console
    // error ("WebSocket connection failed") for a rejected handshake;
    // that noise is expected and must not fail the test.
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(String(err)));

    await page.goto('/');
    const id = await installWsCollector(
      page,
      `${FRONTEND_WS}/ws/audio?signal=graceful-502-test`
    );

    const failure = await waitForWsLog(
      page,
      id,
      (l) => l.type === 'close' || l.type === 'error',
      5_000
    );
    expect(failure).not.toBeNull();
    expect(pageErrors).toEqual([]);
  });
});