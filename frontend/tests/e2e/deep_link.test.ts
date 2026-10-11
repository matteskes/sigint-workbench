import { test, expect } from '@playwright/test';

/**
 * Test 2.3 — Deep-Link URL Lifecycle (from B14, B15, B19).
 *
 * When the Inspector unmounts (selection cleared), the stale `?signal=`
 * must not remain in the URL, causing errors on subsequent navigation
 * (B14/B15).  On hard load, `replaceState` must not throw (B19).
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */
test.describe('Deep-link URL lifecycle (§2.3, B14–B15, B19)', () => {
	test('clean URL when no signal is selected', async ({ page }) => {
		// `/signals` without `?signal=` should have a clean URL.
		await page.goto('/signals');
		expect(page.url()).not.toContain('?signal=');
	});

	test('signal selection adds ?signal=<id> to URL', async ({ page }) => {
		await page.goto('/signals');

		// Wait for the signal table to render (it always mounts, even
		// with no data — that's fine).
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// Click a signal row.  The row is a `<button role="row">`
		// (SignalTable.svelte).  If no signals exist, we can't click
		// one, so skip.
		const rows = page.locator('[role="row"]');
		const rowCount = await rows.count();
		test.skip(rowCount === 0, 'no signals to click');
		if (rowCount === 0) return;

		// Click the first row to open the inspector.
		await rows.first().click();

		// The URL should now contain ?signal=<id> — polled, because the
		// layout mirror writes it via replaceState after the click.
		await expect
			.poll(() => page.url(), { timeout: 5_000 })
			.toContain('?signal=');
	});

	test('clear selection removes ?signal= from URL (B14)', async ({ page }) => {
		await page.goto('/signals');

		// Open the inspector via a row click.
		const rows = page.locator('[role="row"]');
		const rowCount = await rows.count();
		test.skip(rowCount === 0, 'no signals to click');
		if (rowCount === 0) return;
		await rows.first().click();

		// Wait for the URL to update (the deep-link mirror in
		// +layout.svelte writes `?signal=<id>` via replaceState).
		await expect
			.poll(() => page.url(), { timeout: 5_000 })
			.toContain('?signal=');

		// Clear the selection — hit Escape (the layout handler's
		// `handleKeydown()` case 'Escape').
		await page.keyboard.press('Escape');

		// Wait for the URL to update back (the mirror effect in
		// +layout.svelte: `if (id) url.searchParams.set('signal', id)
		// else url.searchParams.delete('signal')`).
		await expect
			.poll(() => page.url(), { timeout: 5_000 })
			.not.toContain('?signal=');
	});

	test('hard load with ?signal=<garbage> does not crash (B15, B19)', async ({
		page,
	}) => {
		// Hard-load with a garbage signal ID.  The signals route's
		// `onMount` tries `fetchSignal(id)`, catches errors, and leaves
		// the selection empty.  The `replaceState` call must not throw
		// (B19 fix: `routerReady` guard) — uncaught exceptions surface
		// as pageerror events.
		const pageErrors: string[] = [];
		page.on('pageerror', (err) => pageErrors.push(String(err)));

		await page.goto('/signals?signal=<garbage-injection-string>');

		// The page should still render — the signal table exists.
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// No unhandled exceptions from the garbage ID (B15 fix: it
		// soft-fails and no fake selection is created).
		expect(pageErrors, 'garbage deep link must not throw').toEqual([]);
	});

	test('hard load with a well-formed ?signal= does not crash', async ({
		page,
	}) => {
		// Without live backend data a valid ID can't open the Inspector;
		// the point is that an unknown-but-well-formed ID soft-fails
		// without breaking the page (B15/B19).
		const pageErrors: string[] = [];
		page.on('pageerror', (err) => pageErrors.push(String(err)));

		await page.goto('/?signal=fake-id-for-structure-test');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });
		expect(pageErrors, 'unknown signal id must not throw').toEqual([]);
	});
});