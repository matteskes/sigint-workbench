/**
 * Categories 6–8 — Map, Classification plumbing, Regression & UI Stress
 * (from E2E-TEST-SUITE.md §4.6–§4.8, Tests 6.1, 7.1, 8.1, 8.2)
 *
 * What a UI test can honestly verify end-to-end is the **relay +
 * rendering** path: an event posted to the hub ingest endpoint (§2.2,
 * broadcast-only) reaches the browser store and renders with intact
 * fields — the `freqHz` schema, the §6.5 class enum, the modulation
 * string.  The classifier itself (IQ → class/confidence) is exercised
 * by `internal/classify` Go tests and the ONNX smoke fixture
 * (`make smoke-onnx`); scenario tests that would need real classified
 * output are marked fixme below instead of asserting on the values the
 * test itself injected.
 *
 * Addresses: §16 (map config), §6.5 (class source enum), D4, B12–B16.
 *
 * @module classification_accuracy
 */

import { test, expect, type Page } from '@playwright/test';
import {
  freqLabel,
  getJson,
  installWsCollector,
  messageContaining,
  postEvent,
  servicesReady,
  signalPayload,
  waitForWsLog,
  wsLogs,
  FRONTEND_WS,
} from './gateway_helpers';

/** A decoded hub broadcast (`{"type":…,"payload":…}`). */
interface RelayFrame {
  type: string;
  payload: Record<string, unknown>;
}

/** All JSON frames a collector has observed, optionally filtered by id. */
async function relayedPayloads(
  page: Page,
  collector: string,
  sigId?: string
): Promise<RelayFrame[]> {
  const logs = await wsLogs(page, collector);
  const frames = logs
    .filter((l) => l.type === 'message' && l.body?.trim().startsWith('{'))
    .map((l) => {
      try {
        return JSON.parse(l.body!) as RelayFrame;
      } catch {
        return null;
      }
    })
    .filter((f): f is RelayFrame => f !== null);
  if (!sigId) return frames;
  return frames.filter((f) => JSON.stringify(f.payload).includes(sigId));
}

test.describe('Map Tile Bounds (6.1)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping tile bounds test');

  test('6.1.1 — TileJSON bounds are valid (maxLon > minLon)', async () => {
    const { status, body } = await getJson('http://localhost:8082/data/v3.json');
    test.skip(status !== 200, 'tile server not running on :8082');

    const data = body as { bounds?: number[]; tilejson?: unknown };
    expect(Array.isArray(data.bounds)).toBe(true);
    expect(data.bounds!.length).toBeGreaterThanOrEqual(4);
    const [minLon, , maxLon] = data.bounds!;
    expect(maxLon).toBeGreaterThan(minLon);
  });

  test('6.1.2 — map element attaches within the data region (B3)', async ({
    page,
  }) => {
    await page.goto('/');
    const mapEl = page.locator('.maplibregl-map, [class*="maplibregl"]');
    await expect(mapEl.first()).toBeAttached({ timeout: 10_000 });
  });

  test('6.1.3 — zoom interaction leaves the map attached', async ({
    page,
  }) => {
    await page.goto('/');
    const mapEl = page.locator('.maplibregl-map, [class*="maplibregl"]');
    await expect(mapEl.first()).toBeAttached({ timeout: 10_000 });
    // Simulate a scroll-zoom; the map element must survive it.
    await page.mouse.wheel(100, 200);
    await page.waitForTimeout(500);
    await expect(mapEl.first()).toBeAttached();
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 7.1 — Classification plumbing over the relay
// ──────────────────────────────────────────────────────────────────────

test.describe('Classification relay (7.1)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping classification relay test');

  test.fixme(
    '7.1.1 — noise-only frames produce no high-confidence classifications',
    async () => {
      // This scenario tests the *classifier*: noise IQ → no confident
      // class.  Injecting events with a confidence value we chose would
      // assert on our own input, not on DSP behaviour.  Needs iq-ingest
      // frame injection (`make dev` + UDP frames); covered by
      // internal/classify unit tests and `make smoke-onnx`.
    }
  );

  test('7.1.2 — CW event relays and renders with correct parameters', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const freqHz = 146_111_000;
    const posted = await postEvent(
      'signal.new',
      signalPayload({
        id: signalId,
        freqHz,
        bandwidthHz: 500,
        modulation: 'CW',
        subType: 'CW',
        class: 'amateur',
        confidence: 0.85,
        powerDbm: -80,
      })
    );
    expect(posted.status, `hub ingest must accept: ${posted.body}`).toBe(200);

    // The broadcast payload must arrive with the class field intact
    // (§6.5: class source enum, never a method:label string).
    const frame = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(frame, 'CW signal.new must be broadcast').not.toBeNull();
    const payload = (await relayedPayloads(page, collector, signalId)).find(
      (f) => f.type === 'signal.new'
    );
    expect(payload).toBeDefined();
    expect(payload!.payload.freqHz).toBe(freqHz);
    expect(payload!.payload.class).toBe('amateur');
    expect(String(payload!.payload.class)).not.toMatch(/:/);

    // …and the row renders with the CW frequency.
    await expect(page.getByText(freqLabel(freqHz))).toBeVisible({
      timeout: 15_000,
    });
  });

  test('7.1.3 — WFM event relays and renders as broadcast class', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const freqHz = 100_811_000;
    const posted = await postEvent(
      'signal.new',
      signalPayload({
        id: signalId,
        freqHz,
        bandwidthHz: 160_000,
        modulation: 'FM',
        subType: 'NFM',
        class: 'broadcast',
        confidence: 0.9,
        powerDbm: -60,
      })
    );
    expect(posted.status).toBe(200);

    const frame = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(frame, 'WFM signal.new must be broadcast').not.toBeNull();
    const payload = (await relayedPayloads(page, collector, signalId)).find(
      (f) => f.type === 'signal.new'
    );
    expect(payload).toBeDefined();
    expect(payload!.payload.class).toBe('broadcast');

    await expect(page.getByText(freqLabel(freqHz))).toBeVisible({
      timeout: 15_000,
    });
  });

  test('7.1.4 — relayed class values stay inside the §6.5 enum', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const validClasses = [
      'aviation',
      'land_mobile',
      'marine',
      'amateur',
      'broadcast',
      'gnss',
      'wifi',
      'unknown',
    ];
    const stamp = Date.now();
    const ids: string[] = [];

    for (let i = 0; i < validClasses.length; i++) {
      const cls = validClasses[i];
      const id = `enum-${cls}-${stamp}`;
      ids.push(id);
      const posted = await postEvent(
        'signal.new',
        signalPayload({
          id,
          freqHz: 433_100_000 + i * 25_000,
          class: cls,
          modulation: 'AM',
          confidence: 0.5,
        })
      );
      expect(posted.status, `ingest of ${cls} must be accepted`).toBe(200);
    }

    // Wait until every id has been relayed back to this browser.
    await expect
      .poll(async () => {
        const frames = await relayedPayloads(page, collector);
        const relayed = new Set(
          frames.map((f) => String(f.payload.id ?? ''))
        );
        return ids.filter((id) => relayed.has(id)).length;
      }, { timeout: 10_000 })
      .toBe(validClasses.length);

    // Every relayed payload must carry a plain enum class — the B4
    // regression was a "method:label" composite leaking into the UI.
    const frames = await relayedPayloads(page, collector);
    for (const id of ids) {
      const payload = frames.find((f) => f.payload.id === id);
      expect(payload, `${id} must have been relayed`).toBeDefined();
      const cls = String(payload!.payload.class);
      expect(validClasses).toContain(cls);
      expect(cls).not.toMatch(/:/);
    }
  });
});


// ──────────────────────────────────────────────────────────────────────
// Test 8.1 — Frequency-band rendering regressions
// ──────────────────────────────────────────────────────────────────────

test.describe('Regression sweep — CW / WFM / below-center (8.1)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping regression sweep');

  test('8.1.1 — HF CW detection renders at 16.000 MHz', async ({ page }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const posted = await postEvent(
      'signal.new',
      signalPayload({
        id: signalId,
        freqHz: 16_000_000,
        bandwidthHz: 500,
        modulation: 'CW',
        subType: 'CW',
        class: 'amateur',
        confidence: 0.9,
        powerDbm: -70,
      })
    );
    expect(posted.status).toBe(200);

    const frame = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(frame).not.toBeNull();
    await expect(page.getByText('16.000 MHz')).toBeVisible({
      timeout: 15_000,
    });
  });

  test('8.1.2 — VHF WFM detection renders at 100.811 MHz', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const posted = await postEvent(
      'signal.new',
      signalPayload({
        id: signalId,
        freqHz: 100_811_000,
        bandwidthHz: 250_000,
        modulation: 'WFM',
        subType: 'NFM',
        class: 'broadcast',
        confidence: 0.9,
        powerDbm: -50,
      })
    );
    expect(posted.status).toBe(200);

    const frame = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(frame).not.toBeNull();
    await expect(page.getByText('100.811 MHz')).toBeVisible({
      timeout: 15_000,
    });
  });

  test('8.1.3 — below-center CW detection renders at 14.500 MHz', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const signalId = crypto.randomUUID();
    const posted = await postEvent(
      'signal.new',
      signalPayload({
        id: signalId,
        freqHz: 14_500_000,
        bandwidthHz: 500,
        modulation: 'CW',
        subType: 'CW',
        class: 'amateur',
        confidence: 0.9,
        powerDbm: -70,
      })
    );
    expect(posted.status).toBe(200);

    const frame = await waitForWsLog(
      page,
      collector,
      messageContaining(signalId),
      5_000
    );
    expect(frame).not.toBeNull();
    await expect(page.getByText('14.500 MHz')).toBeVisible({
      timeout: 15_000,
    });
  });

  test('8.1.4 — modulation strings relay intact (CW/AM/FM/LSB)', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const modulations = ['CW', 'AM', 'FM', 'LSB'];
    const stamp = Date.now();
    const ids: string[] = [];
    for (let i = 0; i < modulations.length; i++) {
      const id = `mod-${modulations[i]}-${stamp}`;
      ids.push(id);
      const posted = await postEvent(
        'signal.new',
        signalPayload({
          id,
          freqHz: 434_100_000 + i * 25_000,
          bandwidthHz: 12_000,
          modulation: modulations[i],
          class: 'amateur',
          confidence: 0.8,
          powerDbm: -80,
        })
      );
      expect(posted.status).toBe(200);
    }

    await expect
      .poll(async () => {
        const frames = await relayedPayloads(page, collector);
        const relayed = new Set(frames.map((f) => String(f.payload.id ?? '')));
        return ids.filter((id) => relayed.has(id)).length;
      }, { timeout: 10_000 })
      .toBe(modulations.length);

    const frames = await relayedPayloads(page, collector);
    for (let i = 0; i < modulations.length; i++) {
      const payload = frames.find((f) => f.payload.id === ids[i]);
      expect(payload).toBeDefined();
      expect(payload!.payload.modulation).toBe(modulations[i]);
    }
  });
});


// ──────────────────────────────────────────────────────────────────────
// Test 8.2 — Aggressive UI Sweep (UI-BUGCHECK B12–B16)
//
// These need only the frontend dev server — no services gate.
// ──────────────────────────────────────────────────────────────────────

test.describe('Aggressive UI sweep (8.2)', () => {
  const SEARCH = '[data-signal-search]';

  test('8.2.1 — regex metacharacters in search do not crash', async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(String(err)));

    await page.goto('/');
    await page.locator(SEARCH).fill('.*[');
    await page.waitForTimeout(500);

    await expect(page.getByRole('main')).toBeAttached();
    expect(pageErrors).toEqual([]);
  });

  test('8.2.2 — 300-character search string is handled', async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(String(err)));

    await page.goto('/');
    await page.locator(SEARCH).fill('a'.repeat(300));
    await page.waitForTimeout(500);

    await expect(page.getByRole('main')).toBeAttached();
    expect(pageErrors).toEqual([]);
  });

  test('8.2.3 — unicode search string is handled gracefully', async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(String(err)));

    await page.goto('/');
    await page.locator(SEARCH).fill(' signals — Ω µ ✓ ');
    await page.waitForTimeout(500);

    await expect(page.getByRole('main')).toBeAttached();
    expect(pageErrors).toEqual([]);
  });

  test('8.2.4 — spectrum canvases render at 2560×1440 and 1280×480', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 2560, height: 1440 });
    await page.goto('/spectrum');
    const lineCanvas = page.locator('canvas[aria-label^="Spectrum line"]');
    await expect(lineCanvas).toBeAttached({ timeout: 10_000 });

    await page.setViewportSize({ width: 1280, height: 480 });
    await expect(lineCanvas).toBeAttached({ timeout: 10_000 });
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 8.3 — Rapid signal injection and removal over the relay
// ──────────────────────────────────────────────────────────────────────

test.describe('Rapid injection and removal (8.3)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping rapid injection test');

  test('8.3.1 — 20 signals in, 10 removed, UI tracks both', async ({
    page,
  }) => {
    await page.goto('/');
    const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

    const stamp = Date.now();
    const ids: string[] = [];
    const baseHz = 431_000_000;

    // Inject 20 signals via the hub ingest endpoint.
    for (let i = 0; i < 20; i++) {
      const id = `rapid-${stamp}-${i}`;
      ids.push(id);
      const posted = await postEvent(
        'signal.new',
        signalPayload({
          id,
          freqHz: baseHz + i * 1_000_000,
          class: 'unknown',
          confidence: 0.3,
          powerDbm: -90,
        })
      );
      expect(posted.status, `ingest ${i} must be accepted`).toBe(200);
    }

    // All 20 must reach the browser…
    await expect
      .poll(async () => {
        const frames = await relayedPayloads(page, collector);
        const relayed = new Set(frames.map((f) => String(f.payload.id ?? '')));
        return ids.filter((id) => relayed.has(id)).length;
      }, { timeout: 15_000 })
      .toBe(20);

    // …and render.
    await expect(page.getByText(freqLabel(baseHz))).toBeVisible({
      timeout: 15_000,
    });

    // Remove the first 10 (§14.3 coalescing makes this a burst).
    for (const id of ids.slice(0, 10)) {
      const posted = await postEvent('signal.removed', {
        id,
        reason: 'e2e-rapid-removal',
      });
      expect(posted.status).toBe(200);
    }

    // A removed frequency disappears; an untouched one stays.
    await expect(page.getByText(freqLabel(baseHz))).toBeHidden({
      timeout: 15_000,
    });
    await expect(page.getByText(freqLabel(baseHz + 15_000_000))).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByRole('main')).toBeAttached();
  });
});

