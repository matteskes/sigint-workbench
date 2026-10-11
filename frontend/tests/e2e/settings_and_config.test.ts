/**
 * Category 5 — Settings & Configuration (from E2E-TEST-SUITE.md §4.5,
 * Tests 5.1–5.2; the static 5.3 checks live in
 * `frontend/tests/config_files.test.ts` under vitest).
 *
 * Addresses: A2 (knobs parsed but never consumed), §12 (settings API).
 *
 * Settings round-trips are real service behaviour — the gateway writes
 * the YAML and restarts the target — so they run against the live stack
 * and restore whatever they change.
 *
 * @module settings_and_config
 */

import { test, expect } from '@playwright/test';
import { servicesReady } from './gateway_helpers';

test.describe('Settings API — 5.1–5.2 (from E2E-TEST-SUITE.md)', () => {
	let servicesUp = false;
	test.beforeAll(async () => {
		servicesUp = await servicesReady();
	});
	test.skip(() => !servicesUp, 'No services running — skipping settings test');

	test('5.1.1 — settings GET returns editable knobs', async ({ page }) => {
		await page.goto('/');

		const settings = await page.evaluate(async () => {
			const res = await fetch('/api/settings');
			if (!res.ok) return null;
			return res.json();
		});

		expect(settings, '/api/settings must answer 200 with JSON').not.toBeNull();
		const sections = settings.sections as Array<{
			id: string;
			file: string;
			restart: string[];
		}>;
		expect(Array.isArray(sections)).toBe(true);
		expect(sections.length).toBeGreaterThan(0);
		for (const section of sections) {
			expect(section.id).toBeTruthy();
			expect(section.file).toBeTruthy();
			expect(Array.isArray(section.restart)).toBe(true);
		}
	});

	test('5.1.2 — settings PUT saves a knob and reads back', async ({
		page,
	}) => {
		await page.goto('/');

		// Capture the current knob so the test restores it afterwards.
		const original = await page.evaluate(async () => {
			const res = await fetch('/api/settings');
			if (!res.ok) return null;
			const data = await res.json();
			const values = data.values?.['signal-processor'];
			return (values?.signal_ttl as number | undefined) ?? null;
		});

		const put = await page.evaluate(async () => {
			const res = await fetch('/api/settings/signal-processor', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ values: { signal_ttl: 15 } }),
			});
			if (!res.ok) {
				const body = await res.json().catch(() => ({}));
				return {
					ok: false,
					status: res.status,
					error: body?.error ?? 'unknown',
				};
			}
			return { ok: true };
		});
		expect(put.ok, `settings PUT failed: ${JSON.stringify(put)}`).toBe(true);

		const afterSave = await page.evaluate(async () => {
			const res = await fetch('/api/settings');
			if (!res.ok) return null;
			const data = await res.json();
			return data.values['signal-processor'] ?? null;
		});
		expect(afterSave).not.toBeNull();
		expect(afterSave.signal_ttl).toBe(15);

		// Restore the original knob so the suite leaves no trace.
		if (original !== null && original !== 15) {
			const restored = await page.evaluate(async (value) => {
				const res = await fetch('/api/settings/signal-processor', {
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ values: { signal_ttl: value } }),
				});
				return res.ok;
			}, original);
			expect(restored, 'failed to restore signal_ttl').toBe(true);
		}
	});

	test.fixme(
		'5.1.3 — signal TTL expiry (processor-side, needs iq-ingest frames)',
		async () => {
			// TTL expiry happens in the signal-processor/DB layer.  The
			// hub's ingest endpoint is broadcast-only (§2.2), so an
			// injected event never enters TTL management — a UI-level test
			// here would be theatre.  Covered by the signal-processor Go
			// tests; the iq-ingest frame-injection variant belongs in the
			// API integration suite (TEST_DATABASE_URL).
		}
	);

	test('5.1.4 — SDR retune propagates to the capture service', async ({
		page,
	}) => {
		await page.goto('/');

		const sdrs = await page.evaluate(async () => {
			const res = await fetch('/api/sdrs');
			if (!res.ok) return [];
			return res.json() as Promise<Array<{ id: string; freqHz?: number }>>;
		});
		const target = sdrs.find((s) => s.id === 'S1');
		test.skip(!target, 'no SDR S1 attached — retune needs hardware/simulator');
		const originalFreq = target!.freqHz ?? null;

		const retune = await page.evaluate(async () => {
			const res = await fetch('/api/sdrs/S1', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ freqHz: 145_500_000 }),
			});
			if (!res.ok) {
				const body = await res.json().catch(() => ({}));
				return {
					ok: false,
					status: res.status,
					error: body?.error ?? 'unknown',
				};
			}
			return { ok: true };
		});
		expect(retune.ok, `retune failed: ${JSON.stringify(retune)}`).toBe(true);

		const status = await page.evaluate(async () => {
			const res = await fetch('/api/sdrs/S1/status');
			if (!res.ok) return null;
			return res.json();
		});
		expect(status).not.toBeNull();
		expect(status.freqHz).toBe(145_500_000);

		// Restore the original tuning.
		if (originalFreq !== null && originalFreq !== 145_500_000) {
			await page.evaluate(async (hz) => {
				await fetch('/api/sdrs/S1', {
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ freqHz: hz }),
				});
			}, originalFreq);
		}
	});
});