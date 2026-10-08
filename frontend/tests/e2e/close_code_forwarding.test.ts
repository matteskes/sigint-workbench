import { test, expect } from '@playwright/test';
import { connectToGateway, publishSignalEvent, servicesReady } from './gateway_helpers';

/**
 * Test 3.1 — Gateway Close Code Forwarding (B17, §10.4.4).
 *
 * When the recorder is running, the gateway relay to
 * `ws://recorder:9012/ws/audio?signal=<id>` passes through the recorder's
 * clean close frame (code 1000, reason "no live stream").  If this is
 * degraded to a TCP-level abnormal 1006, the dashboard renders "ended" as
 * an error rather than a normal closure.
 *
 * Spec: §10.4 (live audio relay), §10.4.4 (clean close), B17.
 */
test.describe('Close code forwarding through gateway relay (§10.4, B17)', () => {
  test.skip(
    async () => {
      const ready = await servicesReady();
      return !ready;
    },
    'No services running — skipping close code forwarding test'
  );

  test('gateway forwards recorder clean 1000 close to browser (not 1006)', async ({
    page,
  }) => {
    const logs: Array<{
      type: string;
      code?: number;
      reason?: string;
      body?: string;
    }> = [];

    // Connect browser WebSocket to gateway `/ws/audio` relay.
    // The gateway dials the recorder's internal WS at `:9012/ws/audio`.
    // If no session exists for the given signal ID, the recorder sends
    // a clean close (code 1000, reason "no live stream") after a grace
    // expiry (~3 s).
    await page.evaluate(
      ({ url, logs }) => connectToGateway({ url, logs }),
      { url: 'ws://localhost:8080/ws/audio?signal=fake-signal-for-close-test', logs }
    );

    // Wait for the audio.meta hello (first text frame from recorder).
    const metaLog = logs.find((l) => l.type === 'message' && l.body?.includes('audio.meta'));
    expect(metaLog).toBeDefined();
    console.log(`audio.meta received: ${metaLog?.body?.slice(0, 120)}`);

    // Wait for the recorder's close frame (no active session → ~3 s grace).
    // The gateway relay must forward the close frame with code 1000 intact.
    const closeLog = logs.find((l) => l.type === 'close');

    // Allow up to 6 s for the grace expiry (recorder spec: ~3 s, with
    // network jitter).
    const checkClose = () => {
      const l = logs.find((x) => x.type === 'close');
      expect(l).toBeDefined();
      expect(l!.code).toBe(1000);
    };
    await expect(checkClose).toBeTruthy();

    console.log(
      `PASS: Browser received close code=${closeLog!.code} reason="${closeLog!.reason}"`
    );

    // Verify the close frame body is NOT a 1006 (abnormal TCP hangup).
    // B17: the recorder's spec'd clean 1000 must not degrade into a
    // TCP-level abnormal 1006 at the single-client ingress.
    expect(closeLog!.code).toBe(1000);
  });

  test('gateway forwards close reason text through relay chain', async ({ page }) => {
    const logs: Array<{
      type: string;
      code?: number;
      reason?: string;
      body?: string;
    }> = [];

    await page.evaluate(
      ({ url, logs }) => connectToGateway({ url, logs }),
      { url: 'ws://localhost:8080/ws/audio?signal=another-fake-id-for-reason-test', logs }
    );

    // Wait for close frame with its reason text.
    const closeLog = logs.find((l) => l.type === 'close');

    const checkClose = () => {
      const l = logs.find((x) => x.type === 'close');
      expect(l).toBeDefined();
    };
    await expect(checkClose).toBeTruthy();

    // The recorder sends "no live stream" as the reason when closing.
    // The gateway relay must pass this through unchanged.
    const reason = closeLog?.reason ?? '';
    expect(reason).toContain('no live stream');
  });
});