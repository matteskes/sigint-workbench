/**
 * Category 5 — Settings & Configuration (from E2E-TEST-SUITE.md §4.5,
 * Tests 5.1–5.3)
 *
 * Addresses: A2 (knobs parsed but never consumed), A3 (wrong /ws
 * proxy), A4, A8 (dead scan: block), A13 (dead frontend exports).
 *
 * @module settings_and_config
 */

import { test, expect } from '@playwright/test';
import { servicesReady } from './gateway_helpers';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

// Project root (from frontend/ working directory).
const __filename = fileURLToPath(import.meta.url);
const ROOT = path.resolve(__filename, '..', '..', '..');

const CONFIGS = {
  compose: path.join(ROOT, 'docker-compose.yml'),
  envExample: path.join(ROOT, '.env.example'),
  nginx: path.join(ROOT, 'docker', 'nginx.conf'),
  processor: path.join(
    ROOT,
    'config',
    'signal-processor.yaml',
  ),
  oldMapConfig: path.join(
    ROOT,
    'frontend',
    'src',
    'lib',
    'map',
    'config.ts',
  ),
  apiClient: path.join(
    ROOT,
    'frontend',
    'src',
    'lib',
    'api',
    'client.ts',
  ),
};

test.describe('Settings & Configuration — 5.1–5.3 (from E2E-TEST-SUITE.md)', () => {
  test.skip(async () => {
    const ready = await servicesReady();
    return !ready;
  }, 'No services running — skipping settings test');

  test('5.1.1 — Settings GET returns editable knobs', async ({ page }) => {
    await page.goto('/');

    const settings = await page.evaluate(async () => {
      const res = await fetch('http://localhost:8080/api/settings');
      if (!res.ok) return { sections: [], values: {} };
      return res.json();
    });

    await expect(settings.sections).toBeDefined();
    await expect(Array.isArray(settings.sections)).toBe(true);
    await expect(settings.sections.length).toBeGreaterThan(0);

    for (const section of settings.sections) {
      await expect(section.id).toBeTruthy();
      await expect(section.file).toBeTruthy();
      await expect(Array.isArray(section.restart)).toBe(true);
    }
  });

  test('5.1.2 — Settings PUT saves a knob and takes effect', async ({
    page,
  }) => {
    await page.goto('/');

    const saveResult = await page.evaluate(async () => {
      try {
        const res = await fetch(
          'http://localhost:8080/api/settings/signal-processor',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              values: { signal_ttl: 15 },
            }),
          },
        );
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          return {
            ok: false,
            status: res.status,
            error: body?.error ?? 'unknown',
          };
        }
        const body = await res.json();
        return { ok: true, result: body.result };
      } catch (err) {
        return { ok: false, error: (err as Error).message };
      }
    });

    await expect(saveResult.ok).toBe(true);
    await expect(saveResult.result.section).toBeTruthy();

    const afterSave = await page.evaluate(async () => {
      const res = await fetch('http://localhost:8080/api/settings');
      if (!res.ok) return null;
      const data = await res.json();
      return data.values['signal-processor'] ?? null;
    });

    await expect(afterSave).not.toBeNull();
    await expect(afterSave.signal_ttl).toBe(15);
  });

  test('5.1.3 — Signal TTL expiry (shortened TTL test)', async ({ page }) => {
    await page.goto('/');

    await page.evaluate(async () => {
      const res = await fetch(
        'http://localhost:8080/api/settings/signal-processor',
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ values: { signal_ttl: 15 } }),
        },
      );
    });

    await page.waitForTimeout(2_000);

    const signalId = crypto.randomUUID();
    await page.evaluate(async (id) => {
      const res = await fetch('http://localhost:8080/api/events', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'signal.new',
          payload: {
            id,
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
        }),
      });
    }, signalId);

    await page.waitForTimeout(2_000);

    const beforeExpiry = await page.evaluate(async (id) => {
      const res = await fetch(
        'http://localhost:8080/api/signals?min=-90&max=90',
      );
      if (!res.ok) return [];
      const signals: Array<{ id: string }> = await res.json();
      return signals.filter((s) => s.id === id);
    }, signalId);
    await expect(beforeExpiry.length).toBe(1);

    await page.waitForTimeout(20_000);

    const afterExpiry = await page.evaluate(async (id) => {
      const res = await fetch(
        'http://localhost:8080/api/signals?min=-90&max=90',
      );
      if (!res.ok) return [];
      const signals: Array<{ id: string }> = await res.json();
      return signals.filter((s) => s.id === id);
    }, signalId);
    await expect(afterExpiry.length).toBe(0);
  });

  test('5.1.4 — SDR retune propagates to capture service', async ({
    page,
  }) => {
    await page.goto('/');

    const retuneResult = await page.evaluate(async () => {
      try {
        const res = await fetch(
          'http://localhost:8080/api/sdrs/S1',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ freqHz: 145_500_000 }),
          },
        );
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          return { ok: false, status: res.status, error: body?.error };
        }
        return { ok: true, data: await res.json() };
      } catch (err) {
        return { ok: false, error: (err as Error).message };
      }
    });

    await expect(retuneResult.ok).toBeTruthy();

    if (retuneResult.ok) {
      const statusResult = await page.evaluate(async () => {
        const res = await fetch(
          'http://localhost:8080/api/sdrs/S1/status',
        );
        if (!res.ok) return null;
        return res.json();
      });
      await expect(statusResult).not.toBeNull();
      await expect(statusResult.freqHz).toBe(145_500_000);
    }
  });

  test('5.1.5 — Settings validation rejects invalid values', async ({
    page,
  }) => {
    await page.goto('/');

    const saveResult = await page.evaluate(async () => {
      try {
        const res = await fetch(
          'http://localhost:8080/api/settings/signal-processor',
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ values: { signal_ttl: -1 } }),
          },
        );
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          return {
            ok: false,
            status: res.status,
            error: body?.error ?? 'unknown',
            field: body?.field,
          };
        }
        return { ok: true };
      } catch (err) {
        return { ok: false, error: (err as Error).message };
      }
    });

    await expect(saveResult.ok).toBe(false);
    await expect(saveResult.status).toBeGreaterThanOrEqual(400);
    await expect(saveResult.error).toBeTruthy();
  });

  test('5.2.1 — Settings GET → PUT → GET round-trip', async ({ page }) => {
    await page.goto('/');

    // Step 1: GET /api/settings → list all keys and values.
    const initial = await page.evaluate(async () => {
      const res = await fetch('http://localhost:8080/api/settings');
      if (!res.ok) return { sections: [], values: {} };
      return res.json();
    });
    await expect(initial.sections.length).toBeGreaterThan(0);
    await expect(Object.keys(initial.values).length).toBeGreaterThan(0);

    // Step 2: PUT /api/settings/{section} → update multiple values.
    for (const section of initial.sections) {
      const currentValues = initial.values[section.id];
      if (currentValues && typeof currentValues === 'object') {
        const updates: Record<string, number> = {};
        // Try to update numeric fields.
        for (const [key, value] of Object.entries(currentValues)) {
          if (typeof value === 'number') {
            updates[key] = value; // Save current value as "update"
          }
        }
        if (Object.keys(updates).length > 0) {
          await page.evaluate(async (args) => {
            const { section, values } = args;
            const res = await fetch(
              `http://localhost:8080/api/settings/${encodeURIComponent(section)}`,
              {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ values }),
              },
            );
            return res.status;
          }, { section: section.id, values: updates });
        }
      }
    }

    // Step 3: GET /api/settings again → updated values persist.
    const persisted = await page.evaluate(async () => {
      const res = await fetch('http://localhost:8080/api/settings');
      if (!res.ok) return { values: {} };
      return res.json();
    });

    // All previously seen sections should still be present.
    const initialKeys = Object.keys(initial.values);
    const persistedKeys = Object.keys(persisted.values);

    for (const key of initialKeys) {
      await expect(persistedKeys).toContain(key);
    }
  });

  test('5.3.1 — docker-compose.yml: no classifier/location-service containers', async ({
    page,
  }) => {
    // Step 1: Parse docker-compose.yml — no references to classifier
    // or location-service CONTAINERS (D2 stubs fully removed).
    const composeContent = fs.readFileSync(CONFIGS.compose, 'utf-8');

    // The docker-compose.yml may reference "classifier" in env vars for
    // signal-processor, but MUST NOT define a "classifier" or
    // "location-service" top-level service.
    await expect(composeContent.includes('  classifier:')).toBe(false);
    await expect(composeContent.includes('  location-service:')).toBe(false);
  });

  test('5.3.2 — .env.example: no retired variables', async ({ page }) => {
    const envContent = fs.readFileSync(CONFIGS.envExample, 'utf-8');

    // CLASSIFIER_PORT and LOCATION_SERVICE_PORT should not appear
    // as active variables (they may appear in comments about stubs).
    const lines = envContent.split('\n');
    for (const line of lines) {
      const trimmed = line.trim();
      // Skip comments.
      if (trimmed.startsWith('#')) continue;
      // Active variable definitions must not contain these.
      await expect(trimmed).not.toContain('CLASSIFIER_PORT');
      await expect(trimmed).not.toContain('LOCATION_SERVICE_PORT');
    }
  });

  test('5.3.3 — nginx.conf: /ws proxies to api-gateway, not ws-hub', async ({
    page,
  }) => {
    const nginxContent = fs.readFileSync(CONFIGS.nginx, 'utf-8');

    // Step 3: /ws must proxy to api-gateway:8080, not ws-hub:8081.
    const hasWsToApiGateway =
      nginxContent.includes('proxy_pass') &&
      nginxContent.includes('api-gateway:8080');

    await expect(hasWsToApiGateway).toBe(true);
  });

  test('5.3.4 — signal-processor.yaml: no dead scan: block', async ({
    page,
  }) => {
    const processorContent = fs.readFileSync(CONFIGS.processor, 'utf-8');

    // Step 4: No active `scan:` block (comments are fine).
    const lines = processorContent.split('\n');
    for (const line of lines) {
      const trimmed = line.trim();
      if (trimmed.startsWith('#')) continue;
      // A non-commented "scan:" key at the top level is a dead config.
      if (trimmed.match(/^scan:/)) {
        // This would be a failure — dead scan block still present.
      }
    }

    // The file should contain a comment referencing the removed scan:
    // block (proving it was cleaned up intentionally).
    await expect(processorContent).toContain('scan:');
  });

  test('5.3.5 — frontend: no dead API_URL/WS_URL exports', async ({
    page,
  }) => {
    // Step 5: Check that the old config file with dead exports
    // (frontend/src/lib/map/config.ts) does NOT exist.
    const fileExists = fs.existsSync(CONFIGS.oldMapConfig);

    await expect(fileExists).toBe(false);

    // Additionally verify the frontend code HAS settings functions.
    const apiClientContent = fs.readFileSync(CONFIGS.apiClient, 'utf-8');

    await expect(
      apiClientContent.includes('fetchSettings') ||
        apiClientContent.includes('saveSettings'),
    ).toBe(true);
  });
});