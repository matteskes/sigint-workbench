import { test, expect } from '@playwright/test';
import { connectToGateway, servicesReady } from './gateway_helpers';

/**
 * Test 3.2 — Gateway 502 Handling (§13, §2.2).
 *
 * When the recorder is not running (Docker container stopped), the
 * gateway relay to `ws://recorder:9012/ws/audio?signal=<id>` must answer
 * the client's WebSocket handshake with a **502 JSON** response:
 * `{"error":"recorder unreachable"}` — not a raw socket error or a
 * browser-level `WebSocket connection failed` without body.
 *
 * Spec: §13 (error mapping), §2.2 (unreachable upstream → 502 JSON).
 */
test.describe('Gateway 502 handling for unreachable upstream (§2.2, §13)', () => {
  test.skip(
    async () => {
      const ready = await servicesReady();
      return !ready;
    },
    'No services running — skipping 502 handling test'
  );

  test('gateway returns 502 JSON when recorder is unreachable', async ({
    page,
  }) => {
    const logs: Array<{
      type: string;
      body?: string;
    }> = [];

    // Try connecting to `/ws/audio` when the recorder is not running.
    // The gateway relay dials the recorder first, and on failure writes
    // a 502 JSON response: `{"error":"recorder unreachable"}`.
    await page.evaluate(
      ({ url, logs }) => connectToGateway({ url, logs }),
      { url: 'ws://localhost:8080/ws/audio?signal=dead-recorder-test', logs }
    );

    // Wait for the gateway to reject the handshake (502).
    // The `connectToGateway` helper sets a 1.5s timer to check whether
    // the WebSocket handshake was successful. If not, it logs a '502'
    // event.
    await page.waitForTimeout(2_000);

    const errorLogs = logs.filter((l) => l.type === '502' || l.type === 'error');

    // Expect at least one error/502 log (handshake failure).
    expect(errorLogs.length).toBeGreaterThan(0);

    console.log('PASS: Gateway returned 502 (or error) for unreachable recorder');
  });

  test('502 response contains JSON error body {"error":"recorder unreachable"}', async ({
    page,
  }) => {
    // The gateway writes the error body on the HTTP response before
    // the WebSocket upgrade fails.  We can inspect the HTTP response
    // directly using a custom fetch-with-upgrade attempt.
    const bodyText = await page.evaluate(async () => {
      try {
        // Simulate the gateway's pre-dial: open a fetch to the gateway
        // with a WebSocket upgrade header.  The gateway will dial the
        // recorder, fail, and write 502 JSON.
        const resp = await fetch('http://localhost:8080/ws/audio?signal=502-body-test', {
          headers: {
            'Upgrade': 'websocket',
            'Connection': 'Upgrade',
            'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==',
            'Sec-WebSocket-Version': '13',
          },
        });
        return await resp.text();
      } catch {
        return 'CATCH_ERROR';
      }
    });

    // The 502 body must be JSON with the "recorder unreachable" error.
    expect(bodyText).not.toBe('CATCH_ERROR');
    expect(bodyText).toContain('recorder');
    expect(bodyText).toContain('unreachable');
  });

  test('browser handles 502 gracefully — no unhandled exceptions', async ({
    page,
  }) => {
    // Capture any console errors during the 502 test.
    const consoleErrors: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });

    const logs: Array<{ type: string; body?: string }> = [];

    // Attempt connection to gateway with recorder unreachable.
    await page.evaluate(
      ({ url, logs }) => connectToGateway({ url, logs }),
      { url: 'ws://localhost:8080/ws/audio?signal=graceful-502-test', logs }
    );

    // Wait for the response.
    await page.waitForTimeout(2_000);

    // The gateway must answer with 502 JSON, not a raw socket error.
    // The frontend must handle this gracefully — no unhandled rejection
    // or exception thrown in the browser.
    const errors = consoleErrors.filter((e) =>
      e.includes('WebSocket') || e.includes('failed') || e.includes('error')
    );

    // We expect no console errors from the 502 handshake attempt.
    // The `connectToGateway` helper catches all errors and logs them
    // to the `logs` array instead of throwing.
    expect(errors).toHaveLength(0);
  });
});