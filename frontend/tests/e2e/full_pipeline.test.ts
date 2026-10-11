/**
 * Category 4 — Relay + UI Pipeline (from E2E-TEST-SUITE.md §4.4, Test 4.1)
 *
 * Validates the relay slice of the signal chain: ws-hub ingest → hub
 * broadcast → gateway relay → browser store → SignalTable rendering.
 *
 * Events are published to the hub's ingest endpoint
 * (`POST :8081/api/events`), which is broadcast-only (§2.2) — nothing is
 * persisted, so assertions target live WS delivery and the rendered UI,
 * never `/api/signals`.  The classifier/DB slice of the pipeline (IQ →
 * classify → persist) is covered by the Go suites (`make test`, the
 * TEST_DATABASE_URL integration suite).
 *
 * @module full_pipeline
 */

import { test, expect } from '@playwright/test';
import {
  freqLabel,
  installWsCollector,
  messageContaining,
  postEvent,
  servicesReady,
  signalPayload,
  waitForWsLog,
  wsLogs,
  FRONTEND_WS,
} from './gateway_helpers';

async function waitForServices(timeoutMs = 30_000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await servicesReady()) return true;
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  return false;
}

test.describe('Relay + UI pipeline — 4.1 (from E2E-TEST-SUITE.md)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping relay pipeline test');

  test('4.1.1 — event injection → hub broadcast → store → UI row', async ({
    page,
  }) => {
    test.skip(!(await waitForServices(5_000)), 'services did not become ready');

    await page.goto('/');

    // Observe the same ingress the app itself uses (same-origin via the
    // Vite proxy → gateway → hub).
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const freqHz = 146_520_000;
    const posted = await postEvent(
      'signal.new',
      signalPayload({ id: signalId, freqHz })
    );
    expect(posted.status, `hub ingest must accept: ${posted.body}`).toBe(200);

    // The relay chain must deliver the event to this browser…
    const relayed = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(relayed, 'signal.new must be broadcast to the browser').not.toBeNull();

    // …and the store must render it as a table row.
    await expect(page.getByText(freqLabel(freqHz))).toBeVisible({
      timeout: 15_000,
    });
  });

  test('4.1.2 — multi-class relay (CW + WFM rows render)', async ({
    page,
  }) => {
    test.skip(!(await waitForServices(5_000)), 'services did not become ready');

    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const cwId = crypto.randomUUID();
    const cwFreq = 16_000_000; // 16.000 MHz — CW test frequency
    const wfmId = crypto.randomUUID();
    const wfmFreq = 100_800_000; // 100.800 MHz — WFM broadcast

    const cwPosted = await postEvent(
      'signal.new',
      signalPayload({
        id: cwId,
        freqHz: cwFreq,
        bandwidthHz: 500,
        modulation: 'CW',
        subType: 'CW',
        class: 'amateur',
        confidence: 0.92,
        powerDbm: -60,
      })
    );
    expect(cwPosted.status).toBe(200);

    const wfmPosted = await postEvent(
      'signal.new',
      signalPayload({
        id: wfmId,
        freqHz: wfmFreq,
        bandwidthHz: 250_000,
        modulation: 'WFM',
        subType: 'NFM',
        class: 'broadcast',
        confidence: 0.88,
        powerDbm: -55,
      })
    );
    expect(wfmPosted.status).toBe(200);

    // Both events must reach this browser…
    for (const id of [cwId, wfmId]) {
      const relayed = await waitForWsLog(
        page,
        collector,
        messageContaining(id),
        5_000
      );
      expect(relayed, `broadcast of ${id} must arrive`).not.toBeNull();
    }

    // …and both rows must render.
    await expect(page.getByText(freqLabel(cwFreq))).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(freqLabel(wfmFreq))).toBeVisible({
      timeout: 15_000,
    });
  });

  test('4.1.3 — signal.update replaces the row (no duplicate)', async ({
    page,
  }) => {
    test.skip(!(await waitForServices(5_000)), 'services did not become ready');

    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const baseFreq = 146_111_000; // 146.111 MHz — unlikely to collide
    const updatedFreq = 146_222_000; // 146.222 MHz

    const newPosted = await postEvent(
      'signal.new',
      signalPayload({ id: signalId, freqHz: baseFreq })
    );
    expect(newPosted.status).toBe(200);

    // The row renders with the initial frequency first…
    await expect(page.getByText(freqLabel(baseFreq))).toBeVisible({
      timeout: 15_000,
    });

    // …then the update supersedes it in place.
    const updPosted = await postEvent(
      'signal.update',
      signalPayload({ id: signalId, freqHz: updatedFreq })
    );
    expect(updPosted.status).toBe(200);

    // The store upserts by id: the new frequency must appear…
    await expect(page.getByText(freqLabel(updatedFreq))).toBeVisible({
      timeout: 15_000,
    });
    // …and the stale frequency must be gone (no duplicate row).
    await expect(page.getByText(freqLabel(baseFreq))).toBeHidden({
      timeout: 15_000,
    });

    // Exactly two events for this id must have been relayed: the
    // signal.new and the signal.update.
    await expect
      .poll(async () => {
        const logs = await wsLogs(page, collector);
        return logs.filter(
          messageContaining(signalId)
        ).length;
      })
      .toBe(2);
  });

  test('4.1.4 — signal.removed drops the row', async ({ page }) => {
    test.skip(!(await waitForServices(5_000)), 'services did not become ready');

    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const freqHz = 146_333_000; // 146.333 MHz — unlikely to collide

    const newPosted = await postEvent(
      'signal.new',
      signalPayload({ id: signalId, freqHz })
    );
    expect(newPosted.status).toBe(200);

    // The signal appears…
    await expect(page.getByText(freqLabel(freqHz))).toBeVisible({
      timeout: 15_000,
    });

    // …and `signal.removed` drops it from the active list.
    const rmPosted = await postEvent('signal.removed', {
      id: signalId,
      reason: 'e2e-removal',
    });
    expect(rmPosted.status).toBe(200);

    await expect(page.getByText(freqLabel(freqHz))).toBeHidden({
      timeout: 15_000,
    });
  });
});

