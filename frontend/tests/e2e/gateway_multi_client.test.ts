import { test, expect } from '@playwright/test';
import {
  installWsCollector,
  messageContaining,
  postEvent,
  servicesReady,
  signalPayload,
  waitForWsLog,
  FRONTEND_WS,
} from './gateway_helpers';

/**
 * Test 3.3 — Multi-Client WebSocket Fan-Out (§14.3).
 *
 * Open 3 browser pages, each connecting to the gateway `/ws` (same-origin
 * through the Vite proxy).  Publish one event to the ws-hub ingest
 * endpoint (the only ingestion route, §2.2) and assert every page's
 * collector observed the broadcast.  Closing one client must not affect
 * the others (§14.3, §14.4.3).
 *
 * Spec: §14.3 (fan-out contract), §14.4.3 (N-client hub).
 */
test.describe('Multi-client WebSocket fan-out through gateway (§14.3, §14.4.3)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping multi-client fan-out test');

  test('all 3 pages receive the broadcast event from the hub', async ({
    browser,
  }) => {
    const pages = await Promise.all([
      browser.newPage(),
      browser.newPage(),
      browser.newPage(),
    ]);

    try {
      for (const p of pages) {
        await p.goto('/');
      }

      // Each page opens a collector WebSocket through the gateway relay.
      const collectorIds = await Promise.all(
        pages.map((p) => installWsCollector(p, `${FRONTEND_WS}/ws`))
      );

      // Publish exactly one uniquely-identifiable event to the hub
      // ingest endpoint and require the 200 acceptance — a non-200 here
      // used to hide as a silent 404.
      const sigId = `fanout-new-${Date.now()}`;
      const posted = await postEvent(
        'signal.new',
        signalPayload({ id: sigId, freqHz: 146_520_000 })
      );
      expect(posted.status, `hub ingest must accept: ${posted.body}`).toBe(200);

      // Every page's collector must have seen the broadcast.
      for (let i = 0; i < pages.length; i++) {
        const hit = await waitForWsLog(
          pages[i],
          collectorIds[i],
          messageContaining(sigId),
          5_000
        );
        expect(
          hit,
          `page ${i + 1} must receive the signal.new broadcast`
        ).not.toBeNull();
      }
    } finally {
      for (const p of pages) {
        await p.close();
      }
    }
  });

  test('closing one client does not affect the remaining 2', async ({
    browser,
  }) => {
    const pages = await Promise.all([
      browser.newPage(),
      browser.newPage(),
      browser.newPage(),
    ]);

    try {
      for (const p of pages) {
        await p.goto('/');
      }
      const collectorIds = await Promise.all(
        pages.map((p) => installWsCollector(p, `${FRONTEND_WS}/ws`))
      );

      // Close page 2 and let the hub process the disconnect.
      await pages[1].close();
      await pages[0].waitForTimeout(500);

      // The remaining clients must still receive broadcasts.
      const sigId = `fanout-update-${Date.now()}`;
      const posted = await postEvent(
        'signal.update',
        signalPayload({ id: sigId, freqHz: 146_520_001 })
      );
      expect(posted.status, `hub ingest must accept: ${posted.body}`).toBe(200);

      for (const i of [0, 2]) {
        const hit = await waitForWsLog(
          pages[i],
          collectorIds[i],
          messageContaining(sigId),
          5_000
        );
        expect(
          hit,
          `surviving page ${i + 1} must still receive broadcasts`
        ).not.toBeNull();
      }
    } finally {
      for (const p of pages) {
        if (!p.isClosed()) {
          await p.close();
        }
      }
    }
  });
});