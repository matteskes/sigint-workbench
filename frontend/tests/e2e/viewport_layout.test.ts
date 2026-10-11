import { test, expect } from '@playwright/test';

/**
 * Test 2.4 — Narrow Viewport Layout (from B16).
 *
 * Every page horizontally overflowed at 375px in the original bug.
 * The fix uses internal scrolling + nav wrapping.
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */

/** Horizontal overflow of the document, in CSS pixels. */
async function docOverflow(page: import('@playwright/test').Page): Promise<{
	bodyScroll: number;
	htmlScroll: number;
}> {
	return (await page.evaluate(() => {
		const body = document.body;
		const html = document.documentElement;
		return {
			bodyScroll: body.scrollWidth - body.clientWidth,
			htmlScroll: html.scrollWidth - html.clientWidth,
		};
	})) as { bodyScroll: number; htmlScroll: number };
}

test.describe('Viewport layout (§2.4, B16)', () => {
	test('375px (iPhone SE) — no horizontal scrollbar', async ({ page }) => {
		await page.setViewportSize({ width: 375, height: 812 });
		await page.goto('/signals');

		// Wait for the page to render.
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// B16 was that every page horizontally overflowed.  The fix
		// scrolls internally; the document itself must not overflow by
		// more than a scrollbar-gutter tolerance (10px).
		const overflow = await docOverflow(page);
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('480×800 — no horizontal scrollbar', async ({ page }) => {
		await page.setViewportSize({ width: 480, height: 800 });
		await page.goto('/signals');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		const overflow = await docOverflow(page);
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('1280×480 (landscape mobile) — shell nav stays in view', async ({
		page,
	}) => {
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto('/signals');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		// The AppBar navigation must remain visible inside the 480px-
		// tall viewport (it wraps internally instead of pushing content
		// off-screen or overflowing the document).
		const nav = page.locator('nav[aria-label="Views"]');
		await expect(nav).toBeVisible();
		const box = await nav.boundingBox();
		expect(box).not.toBeNull();
		expect(box!.y).toBeGreaterThanOrEqual(0);
		expect(box!.y + box!.height).toBeLessThanOrEqual(480);

		// No horizontal overflow.
		const overflow = await docOverflow(page);
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('2560×1440 (4K) — no overflow, no clipping', async ({ page }) => {
		await page.setViewportSize({ width: 2560, height: 1440 });
		await page.goto('/signals');

		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		const overflow = await docOverflow(page);
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});

	test('375px: navigation wraps internally, not document overflow', async ({
		page,
	}) => {
		await page.setViewportSize({ width: 375, height: 812 });
		await page.goto('/signals');

		// The AppBar's navigation tabs should wrap/scroll internally
		// rather than causing the document to overflow.
		const table = page.locator('[role="table"]');
		await expect(table.first()).toBeVisible({ timeout: 5_000 });

		const nav = page.locator('nav[aria-label="Views"]');
		await expect(nav).toBeVisible();

		const overflow = await docOverflow(page);
		expect(overflow.bodyScroll).toBeLessThanOrEqual(10);
		expect(overflow.htmlScroll).toBeLessThanOrEqual(10);
	});
});