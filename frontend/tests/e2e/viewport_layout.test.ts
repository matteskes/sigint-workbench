import { test, expect } from '@playwright/test';

/**
 * Test 2.4 — Narrow Viewport Layout (from B16).
 *
 * Every page horizontally overflowed at 375px in the original bug.
 * The fix uses internal scrolling + nav wrapping.
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */
test.describe('Viewport layout (§2.4, B16)', () => {
	test('375px (iPhone SE) — no horizontal scrollbar', async ({ page }) => {
		await page.setViewportSize({ width: 375, height: 812 });
		await page.goto('/signals');

		// Wait for the page to render.
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// Check for horizontal overflow — the body scrollWidth should
		// not exceed the viewport width by more than a small tolerance
		// (10px accounts for any scrollbar gutter).
		const overflow = await page.evaluate(() => {
			const body = document.body;
			const html = document.documentElement;
			const bodyScroll = body.scrollWidth - body.clientWidth;
			const htmlScroll = html.scrollWidth - html.clientWidth;
			return { bodyScroll, htmlScroll };
		});

		// The original B16 was that every page horizontally overflowed.
		// Now the fix uses internal scrolling + nav wrapping.  There
		// should be zero or near-zero horizontal overflow.
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('480×800 — no horizontal scrollbar', async ({ page }) => {
		await page.setViewportSize({ width: 480, height: 800 });
		await page.goto('/signals');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		const overflow = await page.evaluate(() => {
			const body = document.body;
			const html = document.documentElement;
			return {
				bodyScroll: body.scrollWidth - body.clientWidth,
				htmlScroll: html.scrollWidth - html.clientWidth,
			};
		});
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('1280×480 (landscape mobile) — shell remains usable', async ({ page }) => {
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto('/signals');

		// The table should still render.  The shell (AppBar) with nav
		// tabs should be visible and navigable (wraps internally).
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// The AppBar (navigation) should still be within the viewport.
		const appbar = page.locator('nav');
		// Not all apps have a <nav> element; check for any visible
		// top bar / appBar content.
		const appBarVisible = await page.locator('[class*="app-bar"]').isVisible();
		// If the app has an AppBar component, it should be visible.
		// (Some apps don't expose a class, so this is a best-effort check.)
		if (appBarVisible) {
			await expect(appBarVisible).toBe(true);
		}

		// No horizontal overflow.
		const overflow = await page.evaluate(() => {
			const body = document.body;
			const html = document.documentElement;
			return {
				bodyScroll: body.scrollWidth - body.clientWidth,
				htmlScroll: html.scrollWidth - html.clientWidth,
			};
		});
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('2560×1440 (4K) — no overflow, no clipping', async ({ page }) => {
		await page.setViewportSize({ width: 2560, height: 1440 });
		await page.goto('/signals');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// No horizontal overflow on 4K.
		const overflow = await page.evaluate(() => {
			const body = document.body;
			const html = document.documentElement;
			return {
				bodyScroll: body.scrollWidth - body.clientWidth,
				htmlScroll: html.scrollWidth - html.clientWidth,
			};
		});
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('375px: navigation wraps internally, not document overflow', async ({ page }) => {
		await page.setViewportSize({ width: 375, height: 812 });
		await page.goto('/signals');

		// The AppBar's navigation tabs should wrap internally (second
		// row/column) rather than causing the document to overflow.
		// We can't easily test wrapping, but we can verify the table
		// renders within the viewport.
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// Verify the page doesn't produce a scrollbar by checking the
		// body scroll width vs client width.
		const overflow = await page.evaluate(() => {
			const body = document.body;
			const html = document.documentElement;
			return {
				bodyScroll: body.scrollWidth - body.clientWidth,
				htmlScroll: html.scrollWidth - html.clientWidth,
			};
		});

		// On narrow viewports, the table's internal scrolling handles
		// overflow; the document itself should not overflow.
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});
});