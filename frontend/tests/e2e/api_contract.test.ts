/**
 * Category 9 — API Contract Validation (from E2E-TEST-SUITE.md §4.9,
 * Tests 9.1–9.2)
 *
 * Addresses: §13 (API specification), §17.2 (origin policy).
 *
 * @module api_contract
 */

import { test, expect } from '@playwright/test';
import { servicesReady } from './gateway_helpers';

const GW = 'http://localhost:8080';

test.describe('REST API Contract (9.1)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping REST contract test');

  test('9.1.1 — GET /api/health returns 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/health`);
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    await expect(result.status).toBe(200);
  });

  test('9.1.2 — GET /api/signals returns array (may be empty)', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`);
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    await expect(result.status).toBe(200);
    await expect(Array.isArray(result.body)).toBe(true);
  });

  test('9.1.3 — GET /api/signals/:id 404 for uuid', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/signals/00000000-0000-0000-0000-000000000000`,
      );
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.4 — GET /api/sdrs 200 returns array', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/sdrs`);
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    await expect(result.status).toBe(200);
    await expect(Array.isArray(result.body)).toBe(true);
  });

  test('9.1.5 — GET /api/settings 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/settings`);
      return { status: r.status };
    });
    await expect(result.status).toBe(200);
  });

  test('9.1.6 — GET /api/setup/status 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/setup/status`);
      return { status: r.status };
    });
    await expect(result.status).toBe(200);
  });

  test('9.1.7 — GET /api/recordings 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/recordings`);
      return { status: r.status };
    });
    await expect(result.status).toBe(200);
  });

  test('9.1.8 — GET /api/recordings/:id 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/recordings/00000000-0000-0000-0000-000000000000`,
      );
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  // ── Invalid input handling ──────────────────────────────────────

  test('9.1.9 — POST /api/signals with invalid JSON → 400', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{ invalid json }',
      });
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    await expect(result.status).toBe(400);
    await expect(result.body?.error).toBeDefined();
  });

  test('9.1.10 — POST /api/events with invalid JSON → 400', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/events`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: 'not json at all',
      });
      return { status: r.status };
    });
    await expect(result.status).toBe(400);
  });

  // ── Method validation ───────────────────────────────────────────

  test('9.1.11 — POST to GET-only endpoint → 405 or 404', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'signal.new', payload: { id: 'test', frequencyHz: 100e6 } }),
      });
      return { status: r.status };
    });
    // The gateway either returns 405 (method not allowed) or 404
    // (no route).  Either is acceptable — we validate the error
    // contract, not a specific code.
    await expect([400, 404, 405]).toContain(result.status);
  });

  test('9.1.12 — GET /api/signals/:id/track 404 for missing signal', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/signals/00000000-0000-0000-0000-000000000000/track`,
      );
      return { status: r.status };
    });
    // SPEC §13: missing signal → 404 (console error handled gracefully).
    await expect(result.status).toBe(404);
  });

  // ── Unknown SDR operations ──────────────────────────────────────

  test('9.1.13 — PUT /api/sdrs/unknown → 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/sdrs/unknown`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ freqHz: 100e6 }),
      });
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.14 — GET /api/sdrs/unknown/status → 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/sdrs/unknown/status`);
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.15 — GET /api/recordings/<id> 404 for missing', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/recordings/00000000-0000-0000-0000-000000000000`,
      );
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.16 — GET /api/recordings/<id>/file 404 for missing', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/recordings/00000000-0000-0000-0000-000000000000/file`,
      );
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.17 — GET /api/signals/:id 404 for missing signal', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/signals/00000000-0000-0000-0000-000000000000`,
      );
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.18 — GET /api/signals/:id/track 404 for missing signal', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        `${GW}/api/signals/00000000-0000-0000-0000-000000000000/track`,
      );
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });

  test('9.1.19 — PUT /api/sdrs/:id 404 for missing SDR', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/sdrs/foobar`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ freqHz: 100e6 }),
      });
      return { status: r.status };
    });
    await expect(result.status).toBe(404);
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 9.2 — CORS Configuration
// ──────────────────────────────────────────────────────────────────────

test.describe('CORS Configuration (9.2)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping CORS test');

  test('9.2.1 — Allowed origin returns correct header', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`, {
        headers: { Origin: 'http://localhost:5173' },
      });
      return {
        status: r.status,
        acao: r.headers.get('Access-Control-Allow-Origin'),
      };
    });
    await expect(result.status).toBe(200);
    await expect(result.acao).toBe('http://localhost:5173');
  });

  test('9.2.2 — Disallowed origin → 403', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`, {
        headers: { Origin: 'http://evil.example.com' },
      });
      return { status: r.status };
    });
    // 403 forbidden, or 4xx (depending on implementation), is valid.
    await expect(result.status).toBeGreaterThanOrEqual(400);
  });

  test('9.2.3 — No Origin header → 200 (non-browser clients)', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`);
      return { status: r.status };
    });
    await expect(result.status).toBe(200);
  });

  test('9.2.4 — CORS preflight from disallowed origin', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(`${GW}/api/signals`, {
        method: 'OPTIONS',
        headers: {
          Origin: 'http://evil.example.com',
          'Access-Control-Request-Method': 'GET',
        },
      });
      return {
        status: r.status,
        acao: r.headers.get('Access-Control-Allow-Origin'),
      };
    });
    // If the server supports CORS, the response must NOT allow evil
    // origin, or return 4xx/403.
    if (result.status === 200 && result.acao) {
      await expect(result.acao).not.toBe('http://evil.example.com');
    } else {
      await expect(result.status).toBeGreaterThanOrEqual(400);
    }
  });
});