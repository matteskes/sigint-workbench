import { test, expect } from '@playwright/test';
import {
  installWsCollector,
  recorderReady,
  servicesReady,
  waitForWsLog,
  wsLogs,
  FRONTEND_WS,
} from './gateway_helpers';

/**
 * Test 3.1 — Gateway Close Code Forwarding (B17, §10.4.4).
 *
 * With the recorder running, the gateway relay to
 * `ws://recorder:9012/ws/audio?signal=<id>` passes through the
 * recorder's clean close frame (code 1000, reason "no live stream").
 * If this degraded to a TCP-level abnormal 1006, the dashboard would
 * render "ended" as an error rather than a normal closure.
 *
 * Requires the recorder UP (otherwise the gateway answers 502 — that
 * path is covered by gateway_502_handling.test.ts).
 *
 * Spec: §10.4 (live audio relay), §10.4.4 (clean close), B17.
 */
// Computed once per worker when the file loads. Playwright evaluates
// test.skip() predicates while the file is collected - before any
// beforeAll can run - so a beforeAll-assigned flag was always false at
// evaluation time and the gated tests skipped forever (found live:
// every gated test skipped against a fully healthy stack). Top-level
// await gives the predicates the real health answer.
const servicesUp = await servicesReady();

test.describe('Close code forwarding through gateway relay (§10.4, B17)', () => {
  test.skip(!servicesUp, 'No services running — skipping close code forwarding test');

  test('gateway forwards recorder clean 1000 close to browser (not 1006)', async ({
    page,
  }) => {
    test.skip(
      (await recorderReady()) === false,
      'recorder is down — the gateway answers 502 instead (covered by gateway_502_handling)'
    );

    await page.goto('/');
    // The recorder requires a UUID signal id (`signal must be a UUID`,
    // internal/record/ws_server.go) — a non-UUID 400s the upgrade before
    // the close semantics under test can run.
    const id = await installWsCollector(
      page,
      `${FRONTEND_WS}/ws/audio?signal=${crypto.randomUUID()}`
    );

    // A UUID with no live in-band stream: after the recorder's ~3 s
    // grace window the streamer answers ErrNoStream and the server
    // closes cleanly — no hello frame is sent on this path (the
    // audio.meta hello only precedes a live stream).
    const close = await waitForWsLog(page, id, (l) => l.type === 'close', 15_000);
    expect(close, 'expected a close frame within 15 s').not.toBeNull();
    expect(close!.code).toBe(1000);
    expect(close!.reason).toContain('no live stream');
  });

  test('close reason text survives the relay chain', async ({ page }) => {
    test.skip(
      (await recorderReady()) === false,
      'recorder is down — the gateway answers 502 instead (covered by gateway_502_handling)'
    );

    await page.goto('/');
    const id = await installWsCollector(
      page,
      `${FRONTEND_WS}/ws/audio?signal=${crypto.randomUUID()}`
    );

    const close = await waitForWsLog(page, id, (l) => l.type === 'close', 10_000);
    expect(close, 'expected a close frame within 10 s').not.toBeNull();
    expect(close!.reason).toContain('no live stream');

    // Sanity: the connection did open before the clean close — a 1006
    // would show up as close-without-open on a failed handshake.
    const logs = await wsLogs(page, id);
    expect(logs.some((l) => l.type === 'open')).toBe(true);
  });
});