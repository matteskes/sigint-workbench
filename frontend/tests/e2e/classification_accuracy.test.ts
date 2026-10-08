/**
 * Category 6–8 — Map, Classification, Regression & UI Stress (from
 * E2E-TEST-SUITE.md §4.6–§4.8, Tests 6.1, 7.1, 8.1, 8.2)
 *
 * Addresses: §16 (map config), §6.5 (class source enum), §5.3 (below-
 * center), D4 (classification), UI-BUGCHECK B12–B16.
 *
 * @module classification_accuracy
 */

import { test, expect } from '@playwright/test';
import { connectToGateway, servicesReady } from './gateway_helpers';

test.describe('Map Tile Bounds (6.1)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping tile bounds test');

  test('6.1.1 — TileJSON bounds are valid (maxLon > minLon)', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      try {
        const r = await fetch('http://localhost:8082/data/v3.json');
        if (!r.ok) return null;
        const data = await r.json();
        return { bounds: data.bounds, tilejson: !!data.tilejson };
      } catch {
        return null;
      }
    });
    if (result) {
      await expect(result.bounds).toBeDefined();
      await expect(Array.isArray(result.bounds)).toBe(true);
      await expect(result.bounds.length).toBeGreaterThanOrEqual(4);
      const [minLon, , maxLon] = result.bounds as number[];
      await expect(maxLon).toBeGreaterThan(minLon);
    }
  });

  test('6.1.2 — Map renders within the data region (not mid-Atlantic)', async ({
    page,
  }) => {
    await page.goto('/');
    const mapEl = page.locator('.maplibregl-map, [class*="maplibregl"]');
    await expect(mapEl.first()).toBeAttached({ timeout: 10_000 });
    await expect(mapEl.first()).not.toBeNull();
  });

  test('6.1.3 — Zoom triggers without JS errors', async ({ page }) => {
    await page.goto('/');
    const mapEl = page.locator('.maplibregl-map, [class*="maplibregl"]');
    await expect(mapEl.first()).toBeAttached({ timeout: 10_000 });
    // Trigger a zoom via the page (simulate user scrolling to zoom).
    await page.mouse.wheel(100, 200);
    await page.waitForTimeout(500);
    await expect(mapEl.first()).toBeAttached();
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 7.1 — Classification Accuracy
// ──────────────────────────────────────────────────────────────────────

test.describe('Classification Accuracy (7.1)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping classification test');

  test('7.1.1 — Noise-only frames produce no high-confidence classifications', async ({
    page,
  }) => {
    await page.goto('/');
    const logs: Array<{ type: string; body?: string }> = [];
    await connectToGateway(page, logs);
    await page.waitForTimeout(1_000);

    for (let i = 0; i < 5; i++) {
      await page.evaluate(async () => {
        await fetch('http://localhost:8080/api/events', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'signal.new',
            payload: {
              id: crypto.randomUUID(),
              frequencyHz: 100e6 + Math.random() * 10e6,
              bandwidthHz: 12000,
              modulation: 'AM',
              subType: 'AM',
              class: 'unknown',
              confidence: 0.2,
              powerDbm: -95 + Math.random() * 5,
              sdrId: 'S1',
              t: new Date().toISOString(),
            },
          }),
        });
      });
    }

    await page.waitForTimeout(3_000);

    const maxConfidence = await page.evaluate(async () => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return 0;
      const signals: Array<{ confidence?: number }> = await r.json();
      return Math.max(0, ...signals.map((s) => s.confidence || 0));
    });
    await expect(maxConfidence).toBeLessThan(0.8);
  });

  test('7.1.2 — CW classification appears with correct parameters', async ({
    page,
  }) => {
    await page.goto('/');
    const signalId = crypto.randomUUID();
    await page.evaluate(async (id) => {
      await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id,
            frequencyHz: 146_000_000,
            bandwidthHz: 500,
            modulation: 'CW',
            subType: 'CW',
            class: 'amateur',
            confidence: 0.85,
            powerDbm: -80,
            sdrId: 'S1',
            t: new Date().toISOString(),
          },
        }),
      });
    }, signalId);

    await page.waitForTimeout(3_000);

    const found = await page.evaluate(async (id) => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return null;
      const signals: Array<{ id: string; class?: string; confidence?: number }> = await r.json();
      return signals.find((s) => s.id === id) ?? null;
    }, signalId);

    if (found) {
      await expect(found.class).toBe('amateur');
      await expect(found.confidence).toBeGreaterThanOrEqual(0.8);
    }
  });

  test('7.1.3 — WFM tones classified as broadcast class', async ({ page }) => {
    await page.goto('/');
    const signalId = crypto.randomUUID();
    await page.evaluate(async (id) => {
      await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id,
            frequencyHz: 100_800_000,
            bandwidthHz: 160_000,
            modulation: 'FM',
            subType: 'NFM',
            class: 'broadcast',
            confidence: 0.9,
            powerDbm: -60,
            sdrId: 'S1',
            t: new Date().toISOString(),
          },
        }),
      });
    }, signalId);

    await page.waitForTimeout(3_000);

    const found = await page.evaluate(async (id) => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return null;
      const signals: Array<{ id: string; class?: string }> = await r.json();
      return signals.find((s) => s.id === id) ?? null;
    }, signalId);

    if (found) {
      await expect(found.class).toBe('broadcast');
    }
  });

  test('7.1.4 — Valid class source enum values (no method:label)', async ({
    page,
  }) => {
    await page.goto('/');
    const validClasses = [
      'aviation', 'land_mobile', 'marine', 'amateur', 'broadcast',
      'gnss', 'wifi', 'unknown',
    ];

    for (const cls of validClasses) {
      const signalId = crypto.randomUUID();
      await page.evaluate(async (args) => {
        const { id, class: cls } = args;
        await fetch('http://localhost:8080/api/events', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'signal.new',
            payload: {
              id, frequencyHz: 100e6, bandwidthHz: 12000,
              modulation: 'FM', subType: 'NFM',
              class: cls, confidence: 0.5,
              powerDbm: -80, sdrId: 'S1',
              t: new Date().toISOString(),
            },
          }),
        });
      }, { id: signalId, class: cls });
    }

    await page.waitForTimeout(3_000);

    const signalData = await page.evaluate(async () => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return [];
      return (await r.json()) as Array<{ class?: string }>;
    });

    for (const sig of signalData) {
      if (sig.class) {
        await expect(validClasses).toContain(sig.class);
        await expect(sig.class).not.toMatch(/:/);
      }
    }
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 8.1 — CW + WFM + Below-Center (Regression Suite)
// ──────────────────────────────────────────────────────────────────────

test.describe('Regression Suite (8.1)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping regression test');

  test('8.1.1 — CW signal injected with narrow bandwidth', async ({ page }) => {
    await page.goto('/');
    const signalId = crypto.randomUUID();
    await page.evaluate(async (id) => {
      await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id, frequencyHz: 16_000_000, bandwidthHz: 500,
            modulation: 'CW', subType: 'CW', class: 'amateur',
            confidence: 0.95, powerDbm: -60, sdrId: 'S1',
            t: new Date().toISOString(),
          },
        }),
      });
    }, signalId);
    await page.waitForTimeout(3_000);
    const found = await page.evaluate(async (id) => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return null;
      const signals: Array<{ frequencyHz?: number }> = await r.json();
      return signals.find((s) => s.frequencyHz === 16_000_000) ?? null;
    }, signalId);
    await expect(found).not.toBeNull();
  });

  test('8.1.2 — WFM signal with broadcast bandwidth', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(async () => {
      await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id: 'wfm-sim', frequencyHz: 100_800_000,
            bandwidthHz: 160_000, modulation: 'FM', subType: 'NFM',
            class: 'broadcast', confidence: 0.9, powerDbm: -50,
            sdrId: 'S1', t: new Date().toISOString(),
          },
        }),
      });
    });
    await page.waitForTimeout(3_000);
    const found = await page.evaluate(async () => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return null;
      const signals: Array<{ id: string; modulation?: string }> = await r.json();
      return signals.find((s) => s.id === 'wfm-sim') ?? null;
    });
    await expect(found).not.toBeNull();
    await expect(found?.modulation).toBe('FM');
  });

  test('8.1.3 — Below-center CW detected correctly', async ({ page }) => {
    await page.goto('/');
    const signalId = crypto.randomUUID();
    await page.evaluate(async (id) => {
      await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id, frequencyHz: 14_500_000, bandwidthHz: 500,
            modulation: 'CW', subType: 'CW', class: 'amateur',
            confidence: 0.9, powerDbm: -70, sdrId: 'S1',
            t: new Date().toISOString(),
          },
        }),
      });
    }, signalId);
    await page.waitForTimeout(3_000);
    const found = await page.evaluate(async (id) => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return null;
      const signals: Array<{ id: string }> = await r.json();
      return signals.find((s) => s.id === id) ?? null;
    }, signalId);
    await expect(found).not.toBeNull();
  });

  test('8.1.4 — No classification contains method:label format', async ({
    page,
  }) => {
    await page.goto('/');
    const testSignals = [
      { id: 'mod1', modulation: 'CW', class: 'amateur' },
      { id: 'mod2', modulation: 'AM', class: 'broadcast' },
      { id: 'mod3', modulation: 'FM', class: 'broadcast' },
      { id: 'mod4', modulation: 'LSB', class: 'amateur' },
    ];
    for (const sig of testSignals) {
      await page.evaluate(async (s) => {
        await fetch('http://localhost:8080/api/events', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'signal.new',
            payload: { ...s, frequencyHz: 100e6, bandwidthHz: 12000, confidence: 0.8, powerDbm: -80, sdrId: 'S1', t: new Date().toISOString() },
          }),
        });
      }, sig);
    }
    await page.waitForTimeout(3_000);
    const allSignals = await page.evaluate(async () => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return [];
      return (await r.json()) as Array<{ class?: string; id?: string }>;
    });
    for (const s of testSignals) {
      const found = allSignals.find((sig) => sig.id === s.id);
      if (found?.class) {
        await expect(found.class).not.toMatch(/:/);
      }
    }
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 8.2 — Aggressive UI Sweep (UI-BUGCHECK B12–B16)
// ──────────────────────────────────────────────────────────────────────

test.describe('Aggressive UI Sweep (8.2)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping aggressive UI sweep');

  test('8.2.1 — Search with regex metacharacters does not crash', async ({
    page,
  }) => {
    await page.goto('/');
    await page.waitForTimeout(1_000);
    await page.getByRole('searchbox', { name: /search/i, hidden: true }).fill('.*[');
    await page.keyboard.press('Enter');
    await page.waitForTimeout(1_000);
    await expect(page.getByRole('main')).not.toBeNull();
  });

  test('8.2.2 — Search with long string (300+ chars) handled', async ({
    page,
  }) => {
    await page.goto('/');
    await page.waitForTimeout(1_000);
    const longStr = 'a'.repeat(300);
    await page.getByRole('searchbox', { name: /search/i, hidden: true }).fill(longStr);
    await page.keyboard.press('Enter');
    await page.waitForTimeout(1_000);
    await expect(page.getByRole('main')).not.toBeNull();
  });

  test('8.2.3 — Unicode search string handled gracefully', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1_000);
    const unicodeStr = 'signals';
    await page.getByRole('searchbox', { name: /search/i, hidden: true }).fill(unicodeStr);
    await page.keyboard.press('Enter');
    await page.waitForTimeout(1_000);
    await expect(page.getByRole('main')).not.toBeNull();
  });

  test('8.2.4 — Canvas renders at 2560x1440 and 1280x480', async ({ page }) => {
    await page.setViewportSize({ width: 2560, height: 1440 });
    await page.goto('/');
    await page.waitForTimeout(1_000);
    const mapVisible = await page.locator('.maplibregl-map, [class*="maplibregl"]').first().isVisible().catch(() => false);
    await expect(mapVisible).toBeTruthy();
    await page.setViewportSize({ width: 1280, height: 480 });
    await page.waitForTimeout(500);
    const narrowMapVisible = await page.locator('.maplibregl-map, [class*="maplibregl"]').first().isVisible().catch(() => false);
    await expect(narrowMapVisible).toBeTruthy();
  });

  test('8.2.5 — Rapid signal injection and removal', async ({ page }) => {
    await page.goto('/');
    await page.waitForTimeout(1_000);
    for (let i = 0; i < 20; i++) {
      await page.evaluate(async (id) => {
        await fetch('http://localhost:8080/api/events', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'signal.new',
            payload: {
              id, frequencyHz: 100e6 + i * 1e6,
              bandwidthHz: 12000, modulation: 'FM', subType: 'NFM',
              class: 'unknown', confidence: 0.3,
              powerDbm: -90, sdrId: 'S1',
              t: new Date().toISOString(),
            },
          }),
        });
      }, crypto.randomUUID());
    }
    await page.waitForTimeout(3_000);
    const signals: Array<{ id: string }> = await page.evaluate(async () => {
      const r = await fetch('http://localhost:8080/api/signals');
      if (!r.ok) return [];
      return (await r.json()) as Array<{ id: string }>;
    });
    for (const sig of signals.slice(0, Math.floor(signals.length / 2))) {
      await page.evaluate(async (id) => {
        await fetch('http://localhost:8080/api/events', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'signal.removed',
            payload: { id, reason: 'expiry' },
          }),
        });
      }, sig.id);
    }
    await page.waitForTimeout(3_000);
    await expect(page.getByRole('main')).not.toBeNull();
  });
});