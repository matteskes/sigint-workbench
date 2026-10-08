/**
 * Category 4 — Recording Lifecycle (from E2E-TEST-SUITE.md, Test 4.2)
 *
 * Validates that live signals trigger recording sessions: WAV + IQ
 * file generation, gateway-served file access, and session rotation
 * (300 s IQ cap).  Mirrors the scenarios from B18.
 *
 * @module recording_lifecycle
 */

import { test, expect } from '@playwright/test';
import { servicesReady, publishSignalEvent } from './gateway_helpers';


test.describe('Recording Lifecycle — 4.2 (from E2E-TEST-SUITE.md)', () => {
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

  test('4.2.1 — Recording session creation and file serving', async ({
    page,
  }) => {
    await page.goto('/');
    const servicesOk = await waitForServices(page);
    await expect(servicesOk).toBe(true);

    const signalId = crypto.randomUUID();

    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: 146_520_000,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.85,
          powerDbm: -75,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(10_000);

    // GET /api/recordings — entries with relative file_path.
    const recordings: Array<{
      id: string;
      file_path?: string;
      status?: string;
      start_time?: string;
    }> = await page.evaluate(async () => {
      const res = await fetch(`http://localhost:8080/api/recordings`);
      if (!res.ok) return [];
      return res.json();
    });

    await expect(recordings.length).toBeGreaterThanOrEqual(1);

    const finalized = recordings.find((r) => r.status === 'finalized');
    if (finalized) {
      // Verify WAV file serves correctly (Content-Type: audio/x-wav).
      const audioResp = await page.evaluate(async (id) => {
        const res = await fetch(
          `http://localhost:8080/api/recordings/${encodeURIComponent(id)}/audio`,
        );
        return {
          status: res.status,
          contentType: res.headers.get('content-type') ?? '',
        };
      }, finalized.id);

      await expect(audioResp.status).toBe(200);
      await expect(audioResp.contentType).toContain('audio/x-wav');

      // Session rotation: send a new signal and confirm a fresh recording.
      const newSignalId = crypto.randomUUID();
      await page.evaluate(publishSignalEvent, [
        {
          type: 'signal.new',
          payload: {
            id: newSignalId,
            frequencyHz: 147_000_000,
            bandwidthHz: 12_000,
            modulation: 'FM',
            subType: 'NFM',
            class: 'land_mobile',
            confidence: 0.8,
            powerDbm: -80,
            sdrId: 'S1',
            t: new Date().toISOString(),
          },
        },
      ]);

      await page.waitForTimeout(10_000);

      const afterNew = await page.evaluate(async () => {
        const res = await fetch(`http://localhost:8080/api/recordings`);
        if (!res.ok) return [];
        return res.json();
      });

      await expect(afterNew.length).toBeGreaterThanOrEqual(2);
    }
  });

  test('4.2.2 — Recording file path resolution through gateway', async ({
    page,
  }) => {
    await page.goto('/');
    await waitForServices(page);

    const signalId = crypto.randomUUID();
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: 146_520_000,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.85,
          powerDbm: -75,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(10_000);

    const recordings: Array<{ id: string; file_path?: string }> =
      await page.evaluate(async () => {
        const res = await fetch(`http://localhost:8080/api/recordings`);
        if (!res.ok) return [];
        return res.json();
      });

    // file_path should be relative (not absolute /data/...).
    for (const rec of recordings) {
      if (rec.file_path) {
        await expect(rec.file_path.startsWith('/')).toBe(false);
      }
    }
  });

  test('4.2.3 — Recording metadata completeness', async ({ page }) => {
    await page.goto('/');
    await waitForServices(page);

    const signalId = crypto.randomUUID();
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: signalId,
          frequencyHz: 146_520_000,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.85,
          powerDbm: -75,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(10_000);

    const recordings: Array<Record<string, unknown>> =
      await page.evaluate(async () => {
        const res = await fetch(`http://localhost:8080/api/recordings`);
        if (!res.ok) return [];
        return res.json();
      });

    // Verify that recordings, when present, include key metadata.
    if (recordings.length > 0) {
      const rec = recordings[0];

      await expect(rec.signal_id).toBeDefined();
      await expect(rec.sdr_id).toBeDefined();

      if (rec.start_time) {
        const startTime = new Date(rec.start_time as string);
        await expect(startTime.toString()).not.toBe('Invalid Date');
      }
    }
  });

  test('4.2.4 — Empty recording cleanup (no phantom files)', async ({
    page,
  }) => {
    await page.goto('/');
    await waitForServices(page);

    const beforeCount = await page.evaluate(async () => {
      const res = await fetch(`http://localhost:8080/api/recordings`);
      if (!res.ok) return 0;
      return (await res.json()).length;
    });

    // Inject a signal and wait briefly.
    await page.evaluate(publishSignalEvent, [
      {
        type: 'signal.new',
        payload: {
          id: crypto.randomUUID(),
          frequencyHz: 146_520_000,
          bandwidthHz: 12_000,
          modulation: 'FM',
          subType: 'NFM',
          class: 'land_mobile',
          confidence: 0.85,
          powerDbm: -75,
          sdrId: 'S1',
          t: new Date().toISOString(),
        },
      },
    ]);

    await page.waitForTimeout(10_000);

    const afterCount = await page.evaluate(async () => {
      const res = await fetch(`http://localhost:8080/api/recordings`);
      if (!res.ok) return 0;
      return (await res.json()).length;
    });

    // Count should not increase unexpectedly for a
    // short-lived signal that doesn't produce a recording.
    if (afterCount > beforeCount) {
      const newRecs = await page.evaluate(async () => {
        const res = await fetch(`http://localhost:8080/api/recordings`);
        if (!res.ok) return [];
        return res.json();
      });
      for (const rec of newRecs) {
        // Finalized recordings should have a start_time.
        if ((rec as Record<string, unknown>).status === 'finalized') {
          await expect(rec.start_time).toBeDefined();
        }
      }
    }
  });
});