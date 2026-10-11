/**
 * Category 9 — API Contract Validation (from E2E-TEST-SUITE.md §4.9,
 * Tests 9.1–9.2)
 *
 * Addresses: §13 (API specification), §17.2 (origin policy).
 *
 * The REST probes (9.1) run same-origin through the Vite proxy
 * (`/api` → gateway :8080), so CORS mechanics can't interfere with the
 * status assertions.  The CORS section (9.2) deliberately runs Node-side
 * via Playwright's `request` fixture and `rawHandshake`: browser `fetch`
 * cannot forge Origin headers, go-chi/cors expresses a REST denial by
 * *omitting* the ACAO headers (not by 403ing), and only the hub's
 * WebSocket upgrade path answers a hard 403 (§17.2).
 *
 * @module api_contract
 */

import { test, expect } from '@playwright/test';
import {
  postEvent,
  rawHandshake,
  servicesReady,
  HUB_HTTP,
  GATEWAY_HTTP,
} from './gateway_helpers';

test.describe('REST API Contract (9.1)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping REST contract test');

  test('9.1.1 — GET /api/health returns 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/health');
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    expect(result.status).toBe(200);
  });

  test('9.1.2 — GET /api/signals returns array (may be empty)', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/signals');
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    expect(result.status).toBe(200);
    expect(Array.isArray(result.body)).toBe(true);
  });

  test('9.1.3 — GET /api/signals/:id 404 for uuid', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        '/api/signals/00000000-0000-0000-0000-000000000000'
      );
      return { status: r.status };
    });
    expect(result.status).toBe(404);
  });

  test('9.1.4 — GET /api/sdrs 200 returns array', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/sdrs');
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    expect(result.status).toBe(200);
    expect(Array.isArray(result.body)).toBe(true);
  });

  test('9.1.5 — GET /api/settings 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/settings');
      return { status: r.status };
    });
    expect(result.status).toBe(200);
  });

  test('9.1.6 — GET /api/setup/status 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/setup/status');
      return { status: r.status };
    });
    expect(result.status).toBe(200);
  });

  test('9.1.7 — GET /api/recordings 200', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/recordings');
      return { status: r.status };
    });
    expect(result.status).toBe(200);
  });

  test('9.1.8 — GET /api/recordings/:id 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        '/api/recordings/00000000-0000-0000-0000-000000000000'
      );
      return { status: r.status };
    });
    expect(result.status).toBe(404);
  });

  // ── Invalid input handling ──────────────────────────────────────

  test('9.1.9 — POST /api/signals with invalid JSON → 400', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/signals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{ invalid json }',
      });
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    expect(result.status).toBe(400);
    expect(
      (result.body as { error?: string } | null)?.error
    ).toBeDefined();
  });

  test('9.1.10 — hub ingest POST /api/events with invalid JSON → 400', async () => {
    // /api/events lives on the ws-hub (:8081), not the gateway (§2.2).
    const resp = await fetch(`${HUB_HTTP}/api/events`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: 'not json at all',
    });
    expect(resp.status).toBe(400);
  });

  test('9.1.11 — hub ingest POST with unknown event type → 400', async () => {
    const resp = await fetch(`${HUB_HTTP}/api/events`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 'bogus.event', payload: {} }),
    });
    expect(resp.status).toBe(400);
  });

  test('9.1.12 — hub ingest POST with valid event → 200 accepted', async () => {
    const posted = await postEvent('sdr.status', {
      id: 'S1',
      online: true,
      t: new Date().toISOString(),
    });
    expect(posted.status).toBe(200);
  });

  // ── Method & route validation ───────────────────────────────────

  test('9.1.13 — POST to GET-only /api/signals → 400/404/405', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/signals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'signal.new', payload: { id: 'test' } }),
      });
      return { status: r.status };
    });
    // We validate the error contract, not a specific code.
    expect([400, 404, 405]).toContain(result.status);
  });

  test('9.1.14 — GET /api/signals/:id/track 404 for missing signal', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        '/api/signals/00000000-0000-0000-0000-000000000000/track'
      );
      return { status: r.status };
    });
    // SPEC §13: missing signal → 404 (console error handled gracefully).
    expect(result.status).toBe(404);
  });

  // ── Unknown SDR operations ──────────────────────────────────────

  test('9.1.15 — PUT /api/sdrs/unknown → 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/sdrs/unknown', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ freqHz: 100e6 }),
      });
      return { status: r.status };
    });
    expect(result.status).toBe(404);
  });

  test('9.1.16 — GET /api/sdrs/unknown/status → 404', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch('/api/sdrs/unknown/status');
      return { status: r.status };
    });
    expect(result.status).toBe(404);
  });

  test('9.1.17 — GET /api/recordings/:id/file 404 for missing', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const r = await fetch(
        '/api/recordings/00000000-0000-0000-0000-000000000000/file'
      );
      return { status: r.status };
    });
    expect(result.status).toBe(404);
  });

  test('9.1.18 — gateway has no /api/events route (§2.2)', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async (url) => {
      // Browser fetch to a cross-origin absolute URL is fine for a
      // status probe: the gateway's default ALLOWED_ORIGINS covers the
      // dev origin, and even a CORS failure surfaces as a network
      // error rather than a silent false-pass.
      try {
        const r = await fetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ type: 'signal.new', payload: {} }),
        });
        return { status: r.status, reachable: true };
      } catch {
        return { status: 0, reachable: false };
      }
    }, `${GATEWAY_HTTP}/api/events`);
    // The gateway must not accept event ingestion — that is the hub's
    // job.  (This pins the P0 found in the original suite, which posted
    // here and silently got 404.)
    if (result.reachable) {
      expect([404, 405]).toContain(result.status);
    } else {
      // CORS-blocked browser request: verify Node-side instead.
      const posted = await postEvent('signal.new', { id: 'x' }, 8080);
      expect([404, 405, 0]).toContain(posted.status);
    }
  });
});

// ──────────────────────────────────────────────────────────────────────
// Test 9.2 — CORS Configuration (§17.2)
// ──────────────────────────────────────────────────────────────────────

test.describe('CORS Configuration (9.2)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping CORS test');

  test('9.2.1 — allowed origin gets the ACAO echo on REST GET', async ({
    request,
  }) => {
    const r = await request.get(`${GATEWAY_HTTP}/api/signals`, {
      headers: { Origin: 'http://localhost:5173' },
    });
    expect(r.status()).toBe(200);
    expect(r.headers()['access-control-allow-origin']).toBe(
      'http://localhost:5173'
    );
  });

  test('9.2.2 — disallowed origin gets no ACAO grant (REST stays 200)', async ({
    request,
  }) => {
    const r = await request.get(`${GATEWAY_HTTP}/api/signals`, {
      headers: { Origin: 'http://evil.example.com' },
    });
    // go-chi/cors denies by omitting the grant — the request itself
    // proceeds (the browser would then block reading the response).
    expect(r.status()).toBe(200);
    const acao = r.headers()['access-control-allow-origin'];
    expect(acao === undefined || acao !== 'http://evil.example.com').toBe(
      true
    );
  });

  test('9.2.3 — no Origin header → 200 (non-browser clients, §17.2)', async ({
    request,
  }) => {
    const r = await request.get(`${GATEWAY_HTTP}/api/signals`);
    expect(r.status()).toBe(200);
  });

  test('9.2.4 — CORS preflight from a disallowed origin is refused', async ({
    request,
  }) => {
    const r = await request.fetch(`${GATEWAY_HTTP}/api/signals`, {
      method: 'OPTIONS',
      headers: {
        Origin: 'http://evil.example.com',
        'Access-Control-Request-Method': 'GET',
      },
    });
    // Whatever the exact status, the evil origin must never be granted.
    const acao = r.headers()['access-control-allow-origin'];
    expect(acao).not.toBe('http://evil.example.com');
  });

  test('9.2.5 — ws-hub WebSocket upgrade with disallowed origin → 403', async () => {
    // §17.2: the hub's upgrade path (gorilla CheckOrigin) hard-rejects
    // browsers from unlisted origins — this is where the spec's 403
    // lives, not on REST calls.
    const res = await rawHandshake(`${HUB_HTTP}/ws`, 'http://evil.example.com');
    expect(res.status).toBe(403);
  });

  test('9.2.6 — ws-hub WebSocket upgrade with allowed origin → 101', async () => {
    const res = await rawHandshake(`${HUB_HTTP}/ws`, 'http://localhost:5173');
    expect(res.status).toBe(101);
  });
});

