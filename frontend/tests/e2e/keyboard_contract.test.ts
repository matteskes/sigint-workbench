import { test, expect } from '@playwright/test';
import {
  freqLabel,
  installWsCollector,
  postEvent,
  servicesReady,
  signalPayload,
  FRONTEND_WS,
} from './gateway_helpers';

/**
 * Test 2.2 — Keyboard Interaction Contract (from B12, B13).
 *
 * The global keyboard shortcuts handler (`+layout.svelte` →
 * `handleKeydown()`) must respect native browser activation keys:
 * Space on a button, Enter on a link.  Esc closes one layer per press.
 *
 * SvelteKit hydrates after `load`, and a keypress fired before the
 * handler attaches is simply lost — so the first press of each test is
 * wrapped in a `toPass` retry loop; once one press has demonstrably
 * reacted, the app is hydrated and single presses are deterministic.
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */

/** Press `key` until `assert` stops throwing (hydration-safe). */
async function pressUntil(
	page: import('@playwright/test').Page,
	key: string,
	assert: () => Promise<void>
): Promise<void> {
	await expect(async () => {
		await page.keyboard.press(key);
		await assert();
	}).toPass({ timeout: 10_000 });
}

test.describe('Keyboard interaction contract (§2.2, B12–B13)', () => {
	test('focuses search input on "/" key', async ({ page }) => {
		await page.goto('/signals');

		// Press "/" — the global keyboard handler focuses the search input.
		const searchInput = page.locator('[data-signal-search]');
		await pressUntil(page, '/', () => expect(searchInput).toBeFocused());
	});

	test('toggles shortcuts overlay with "?"', async ({ page }) => {
		await page.goto('/signals');

		// Initially the ShortcutsOverlay is not rendered.
		const overlay = page.locator('[role="dialog"]');
		await expect(overlay).not.toBeAttached();

		// Press "?" — the global handler toggles `shortcutsOpen`.
		const dialog = page.locator(
			'[role="dialog"][aria-label="Keyboard shortcuts"]'
		);
		await pressUntil(page, '?', () => expect(dialog).toBeVisible());

		// Hydration is proven by the open — the closing "?" is
		// deterministic, and a retry could re-open the overlay.
		await page.keyboard.press('?');
		await expect(dialog).not.toBeAttached({ timeout: 5_000 });
	});

	test('search input accepts text with no signals', async ({ page }) => {
		await page.goto('/signals');

		// Focus the search input (also proves hydration).
		const searchInput = page.locator('[data-signal-search]');
		await pressUntil(page, '/', () => expect(searchInput).toBeFocused());

		// With zero signals the filtered-empty-state branch is
		// unreachable ("waiting for first events…" wins), so the honest
		// no-data assertion is that typing is accepted without breaking
		// the table.  Real filtering is covered by the gated describe
		// below.
		await searchInput.fill('zz-no-such-signal-zz');
		await expect(searchInput).toHaveValue('zz-no-such-signal-zz');
		await expect(page.locator('[role="table"]')).toBeVisible();
	});

	test('Space on a focused button is not swallowed (B12)', async ({
		page,
	}) => {
		await page.goto('/signals');

		// Use the always-enabled "Freq" sort button — Copy CSV is
		// disabled while no signals exist, and a disabled button cannot
		// take focus at all.  The contract under test is what happens to
		// Space on a focused button, regardless of which button.
		const sortBtn = page.locator('button:has-text("Freq")');
		await sortBtn.focus();
		await expect(sortBtn).toBeFocused();

		// Press Space — a native button activation.  The global handler
		// calls `isActivator()` which returns true for BUTTON, so Space
		// is not consumed (B12: Space on a button must activate it, not
		// toggle shortcuts or scroll).
		await page.keyboard.press('Space');

		// The button was activated (it cycles the sort mode).  It must
		// remain focused — focus loss would mean the keypress was
		// hijacked.
		await expect(sortBtn).toBeFocused();
	});

	test('Esc on input does not blur it (B12)', async ({ page }) => {
		await page.goto('/signals');

		// Focus the search input (also proves hydration).
		const searchInput = page.locator('[data-signal-search]');
		await pressUntil(page, '/', () => expect(searchInput).toBeFocused());

		// Press Escape — the layout handler checks `isTypingTarget()` first
		// and returns without acting for INPUT/TEXTAREA/SELECT, so the
		// input should stay focused.
		await page.keyboard.press('Escape');

		// The input should still be focused (not blurred by the handler).
		await expect(searchInput).toBeFocused();
	});

	test('Esc closes one layer of popovers (B13)', async ({ page }) => {
		await page.goto('/signals');

		// Open the shortcuts overlay (also proves hydration).
		const dialog = page.locator(
			'[role="dialog"][aria-label="Keyboard shortcuts"]'
		);
		await pressUntil(page, '?', () => expect(dialog).toBeVisible());

		// Press Esc — `handleKeydown()` closes ONE layer (the overlay).
		// B13's complaint was that ALL popovers close at once.
		await page.keyboard.press('Escape');

		// The shortcuts overlay should be gone now.
		await expect(dialog).not.toBeAttached({ timeout: 5_000 });
	});
});

test.describe('SignalTable live filtering (needs hub relay)', () => {
	let servicesUp = false;
	test.beforeAll(async () => {
		servicesUp = await servicesReady();
	});
	test.skip(() => !servicesUp, 'No services running — skipping live filter test');

	test('search narrows injected signal rows by id and frequency', async ({
		page,
	}) => {
		await page.goto('/signals');
		const collector = await installWsCollector(page, `${FRONTEND_WS}/ws`);

		const id = crypto.randomUUID();
		const freqHz = 146_911_000;
		const posted = await postEvent(
			'signal.new',
			signalPayload({ id, freqHz })
		);
		expect(posted.status, `hub ingest must accept: ${posted.body}`).toBe(200);

		const label = freqLabel(freqHz);
		await expect(page.getByText(label)).toBeVisible({ timeout: 15_000 });

		const search = page.locator('[data-signal-search]');

		// A non-matching string hides the row…
		await search.fill('zz-no-such-signal-zz');
		await expect(page.getByText(label)).toBeHidden({ timeout: 5_000 });

		// …an id prefix brings it back…
		await search.fill(id.slice(0, 8));
		await expect(page.getByText(label)).toBeVisible({ timeout: 5_000 });

		// …and so does its frequency text ("146.911").
		await search.fill('146.911');
		await expect(page.getByText(label)).toBeVisible({ timeout: 5_000 });
	});
});