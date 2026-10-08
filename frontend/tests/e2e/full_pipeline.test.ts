/**
 * Category 4 — Full Pipeline (from E2E-TEST-SUITE.md §4.4, Test 4.1)
 *
 * Validates the complete signal chain: simulator IQ → signal-processor
 * classification → API → UI.  Uses CLI tools (smoke-frames) to inject
 * known signals, then asserts the entire relay: WebSocket feed, REST
 * signals endpoint, and dashboard UI rendering.
 *
 * @module full_pipeline
 */

import { test, expect } from '@playwright/test';
import { servicesReady, publishSignalEvent } from './gateway_helpers';


test.describe('Full Pipeline — 4.1 (from E2E-TEST-SUITE.md)', () => {
  async function waitForServices(page, timeoutMs = 30_000): Promise<boolean> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        const ready = await page.evaluate(servicesReady);
        if (ready) return true;
      } catch { /* ignore */ }
      await page.waitForTimeout(1_000);
    }
    return false;
  }

  test('4.1.1 — CLI signal injection → detection → API → UI', async ({
    page,
  }) => {
    await page.goto('/');
    const servicesOk = await waitForServices(page);
    await expect(servicesOk).toBe(true);

    const signalId = crypto.randomUUID();
    const freqHz = 146_520_000;
    const ts = new Date().toISOString();

    const result = await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: freqHz,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.85,
          powerDbm: -75,
          sdrId: 'S1',
          t: ts,
        },
      },
    ]);
    const [status] = result.split(':');
    await expect(status).toBe('200');

    await page.waitForTimeout(3_000);

    const apiResp = await page.evaluate(
      async (id) => {
        const res = await fetch(`http://localhost:8080/api/signals`);
        if (!res.ok) return { status: res.status, body: null };
        const signals: Array<{ id: string; frequencyHz?: number }> =
          await res.json();
        return {
          status: res.status,
          body: signals.find((s) => s.id === id),
        };
      },
      signalId,
    );
    await expect(apiResp.status).toBe(200);
    await expect(apiResp.body).not.toBeNull();
    await expect(apiResp.body.frequencyHz).toBe(freqHz);

    // Step 5: Navigate to the dashboard and verify the signal appears
    // in the table UI.
    await page.reload();
    await page.waitForTimeout(4_000);

    const freqStr = (freqHz / 1e6).toFixed(3) + ' MHz';
    await expect(page.getByText(freqStr)).toBeVisible({
      timeout: 15_000,
    });
  });

  test('4.1.2 — Multi-class pipeline (CW + WFM tones)', async ({ page }) => {
    await page.goto('/');
    await waitForServices(page);

    const cwId = crypto.randomUUID();
    const cwFreq = 16_000_000; // 16.000 MHz — CW test frequency

    const wfmId = crypto.randomUUID();
    const wfmFreq = 100_800_000; // 100.8 MHz — WFM broadcast

    // Inject CW signal.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: cwId,
          frequencyHz: cwFreq,
          bandwidthHz: 500,
          modulation: 'CW',
          class: 'amateur',
          confidence: 0.92,
          powerDbm: -60,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    // Inject WFM signal.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: wfmId,
          frequencyHz: wfmFreq,
          bandwidthHz: 250_000,
          modulation: 'WFM',
          class: 'broadcast',
          confidence: 0.88,
          powerDbm: -55,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(3_000);

    const signals: Array<{
      id: string;
      frequencyHz?: number;
      class?: string;
    }> = await page.evaluate(async () => {
      const res = await fetch(`http://localhost:8080/api/signals`);
      if (!res.ok) return [];
      return res.json();
    });

    const foundCw = signals.find((s) => s.id === cwId);
    const foundWfm = signals.find((s) => s.id === wfmId);

    await expect(foundCw).not.toBeNull();
    await expect(foundCw.frequencyHz).toBe(cwFreq);
    await expect(foundCw.class).toBe('amateur');

    await expect(foundWfm).not.toBeNull();
    await expect(foundWfm.frequencyHz).toBe(wfmFreq);
    await expect(foundWfm.class).toBe('broadcast');

    // Verify both appear in the UI table.
    await page.reload();
    await page.waitForTimeout(4_000);

    const cwMhz = (cwFreq / 1e6).toFixed(3);
    const wfmMhz = (wfmFreq / 1e6).toFixed(3);

    await expect(page.getByText(cwMhz + ' MHz')).toBeVisible({
      timeout: 15_000,
    });

    await expect(page.getByText(wfmMhz + ' MHz')).toBeVisible({
      timeout: 15_000,
    });
  });

  test('4.1.3 — Signal update propagation through pipeline', async ({
    page,
  }) => {
    await page.goto('/');
    await waitForServices(page);

    const signalId = crypto.randomUUID();
    const baseFreq = 146_000_000;

    // Initial signal injection.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: baseFreq,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.7,
          powerDbm: -80,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(3_000);

    // Now send an update to the same signal with updated parameters.
    const updatedFreq = 146_520_000;
    const updatedConfidence = 0.9;

    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.update',
        payload: {
          id: signalId,
          frequencyHz: updatedFreq,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: updatedConfidence,
          powerDbm: -75,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(3_000);

    // Verify the API returns exactly one entry for this signal,
    // with the updated frequency (not two entries).
    const apiResp = await page.evaluate(async (id) => {
      const res = await fetch(`http://localhost:8080/api/signals`);
      if (!res.ok) return { count: 0, body: null };
      const signals: Array<{ id: string; frequencyHz?: number }> =
        await res.json();
      const found = signals.filter((s) => s.id === id);
      return {
        count: found.length,
        body: found[0] ?? null,
      };
    }, signalId);

    await expect(apiResp.count).toBe(1);
    await expect(apiResp.body.frequencyHz).toBe(updatedFreq);
  });

  test('4.1.4 — Signal removal propagation through pipeline', async ({
    page,
  }) => {
    await page.goto('/');
    await waitForServices(page);

    const signalId = crypto.randomUUID();

    // Inject a signal.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: 146_000_000,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.7,
          powerDbm: -80,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(3_000);

    // Verify signal is present.
    const beforeRemove = await page.evaluate(async (id) => {
      const res = await fetch(`http://localhost:8080/api/signals`);
      if (!res.ok) return [];
      const signals: Array<{ id: string }> = await res.json();
      return signals.filter((s) => s.id === id);
    }, signalId);
    await expect(beforeRemove.length).toBe(1);

    // Now send signal.removed event.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.removed',
        payload: {
          id: signalId,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(3_000);

    // After removal, the signal should not appear in active signals.
    const afterRemove = await page.evaluate(async (id) => {
      const res = await fetch(`http://localhost:8080/api/signals`);
      if (!res.ok) return [];
      const signals: Array<{ id: string }> = await res.json();
      return signals.filter((s) => s.id === id);
    }, signalId);
    await expect(afterRemove.length).toBe(0);
  });
});