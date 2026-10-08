import { test, expect } from '@playwright/test';
import { connectToGateway, publishSignalEvent, servicesReady } from './gateway_helpers';

/**
 * Test 3.3 — Multi-Client WebSocket Fan-Out (§14.3).
 *
 * Open 3 browser pages, each connecting to the gateway `/ws`.  The hub
 * broadcasts to all connected clients.  One client closing must not
 * affect the others (spec §14.3, §14.4.3).
 *
 * The gateway relay dials the hub before upgrading the client — so
 * `GET /ws` is transparently forwarded to `ws-hub:8081/ws`.
 *
 * Spec: §14.3 (fan-out contract), §14.4.3 (N-client hub).
 */
test.describe('Multi-client WebSocket fan-out through gateway (§14.3, §14.4.3)', () => {
  test.skip(
    async () => {
      const ready = await servicesReady();
      return !ready;
    },
    'No services running — skipping multi-client fan-out test'
  );

  test('all 3 pages receive broadcast events from hub', async ({ browser }) => {
    const pages = await Promise.all([
      browser.newPage(),
      browser.newPage(),
      browser.newPage(),
    ]);

    try {
      // Each page connects to the gateway `/ws` relay.
      const pageLogs = pages.map(() =>
        [] as Array<{ type: string; body?: string }>
      );

      // Connect all pages to the gateway /ws relay.
      await Promise.all(
        pages.map((p, i) =>
          p.evaluate(
            ({ url, logs }) => connectToGateway({ url, logs }),
            { url: 'ws://localhost:8080/ws', logs: pageLogs[i] }
          )
        )
      );

      // Wait a moment for connections to establish.
      await pages[0].waitForTimeout(1_500);

      // Verify all 3 pages opened their connections.
      for (let i = 0; i < pages.length; i++) {
        const openLogs = pageLogs[i].filter((l) => l.type === 'open');
        expect(openLogs.length).toBeGreaterThan(0);
      }

      // Publish a signal detection event via the gateway.
      // The signal-processor posts to `POST /api/events`, which the
      // hub broadcasts to all `/ws` clients.
      for (const p of pages) {
        await p.evaluate(publishSignalEvent, [{
          type: 'signal.new',
          payload: { id: 'fan-out-sig-' + Date.now(), freqHz: 146_520_000 },
        }]);
      }

      // Wait for all pages to receive the broadcast event.
      await pages[0].waitForTimeout(3_000);

      // Verify all 3 pages received the signal.new event.
      for (let i = 0; i < pages.length; i++) {
        const messageLogs = pageLogs[i].filter(
          (l) => l.type === 'message' && l.body?.includes('signal.new')
        );
        expect(messageLogs.length).toBeGreaterThan(0);
        console.log(`Page ${i + 1} received signal.new`);
      }
    } finally {
      for (const p of pages) {
        await p.close();
      }
    }
  });

  test('closing one client does not affect remaining 2', async ({
    browser,
  }) => {
    const pages = await Promise.all([
      browser.newPage(),
      browser.newPage(),
      browser.newPage(),
    ]);

    try {
      const pageLogs = pages.map(() =>
        [] as Array<{ type: string; body?: string }>
      );

      // Connect all pages.
      await Promise.all(
        pages.map((p, i) =>
          p.evaluate(
            ({ url, logs }) => connectToGateway({ url, logs }),
            { url: 'ws://localhost:8080/ws', logs: pageLogs[i] }
          )
        )
      );

      await pages[0].waitForTimeout(1_500);

      // Close one page (the "closer").
      await pages[1].close();

      // Wait for the hub to process the closure.
      await pages[0].waitForTimeout(2_000);

      // Publish a new event via the gateway.
      for (const p of pages) {
        if (p.isClosed()) continue;
        await p.evaluate(publishSignalEvent, [{
          type: 'signal.update',
          payload: { id: 'fan-out-update-' + Date.now(), freqHz: 146_520_001 },
        }]);
      }

      // Wait for remaining pages to receive the event.
      await pages[0].waitForTimeout(3_000);

      // Verify pages 0 and 2 still receive events (page 1 is closed).
      for (let i = 0; i < pages.length; i++) {
        if (pages[i].isClosed()) {
          console.log(`Page ${i + 1} was closed — skipping`);
          continue;
        }

        const messageLogs = pageLogs[i].filter(
          (l) => l.type === 'message' && l.body?.includes('signal.update')
        );
        expect(messageLogs.length).toBeGreaterThan(0);
        console.log(`Remaining page ${i + 1} still receives events`);
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