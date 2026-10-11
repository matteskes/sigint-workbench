import { test, expect } from '@playwright/test';
import {
  installWsCollector,
  messageContaining,
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
test.describe('Close code forwarding through gateway relay (§10.4, B17)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping close code forwarding test');

  test('gateway forwards recorder clean 1000 close to browser (not 1006)', async ({
    page,
  }) => {
    test.skip(
      (await recorderReady()) === false,
      'recorder is down — the gateway answers 502 instead (covered by gateway_502_handling)'
    );

    await page.goto('/');
    const id = await installWsCollector(
      page,
      `${FRONTEND_WS}/ws/audio?signal=e2e-close-code-${Date.now()}`
    );

    // The recorder says hello with an audio.meta JSON frame…
    const meta = await waitForWsLog(
      page,
      id,
      messageContaining('audio.meta'),
      5_000
    );
    expect(meta, 'expected an audio.meta hello frame from the recorder').not.toBeNull();

    // …then closes cleanly (1000, "no live stream") once no session
    // materialises.  The gateway relay must forward code and reason
    // unchanged.  Allow up to 10 s for the recorder's ~3 s grace window.
    const close = await waitForWsLog(page, id, (l) => l.type === 'close', 10_000);
    expect(close, 'expected a close frame within 10 s').not.toBeNull();
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
      `${FRONTEND_WS}/ws/audio?signal=e2e-close-reason-${Date.now()}`
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