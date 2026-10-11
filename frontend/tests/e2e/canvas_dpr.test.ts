import { test, expect } from '@playwright/test';

/**
 * Test 2.1 — Canvas Text Rendering at High DPR (from B9).
 *
 * Validates that the spectrum waterfall renders text labels at native
 * device resolution (devicePixelRatio 2 on Retina MacBooks).  A fixed-
 * size bitmap stretched by CSS smears every label into unreadable mush.
 *
 * SpectrumView (§18.3) mounts three canvases: the spectrum **line**, the
 * **waterfall** heatmap, and the **tick** strip.  Line + tick carry text
 * and are sized by `fitCanvas()` to CSS box × devicePixelRatio at draw
 * time (the rAF loop draws with a null frame before any data arrives, so
 * no live spectrum feed is needed).  The waterfall intentionally keeps
 * its native bins × rows bitmap — stretching that one is the point.
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */
test.describe('Canvas mounting (§2.1)', () => {
	const LINE = 'canvas[aria-label^="Spectrum line"]';
	const WATERFALL = 'canvas[aria-label^="Waterfall"]';
	// The tick canvas is the only one without an aria-label.
	const TICK = 'canvas:not([aria-label])';

	test('waterfall, line and tick canvases mount with valid bitmaps', async ({
		page,
	}) => {
		await page.goto('/spectrum');

		// All three canvases exist…
		for (const sel of [LINE, WATERFALL, TICK]) {
			await expect(page.locator(sel)).toBeAttached({ timeout: 10_000 });
		}

		// …the waterfall keeps a native (bins × rows) bitmap, and the
		// line canvas matches the h-32 (128px) CSS height so labels
		// aren't vertically stretched.
		const waterfallWidth = await page
			.locator(WATERFALL)
			.getAttribute('width');
		expect(Number(waterfallWidth)).toBeGreaterThan(0);

		const lineCssHeight = await page.locator(LINE).evaluate((el) =>
			parseFloat(window.getComputedStyle(el as HTMLCanvasElement).height)
		);
		expect(lineCssHeight).toBeGreaterThanOrEqual(125);
		expect(lineCssHeight).toBeLessThanOrEqual(131);

		const tickCssHeight = await page.locator(TICK).evaluate((el) =>
			parseFloat(window.getComputedStyle(el as HTMLCanvasElement).height)
		);
		expect(tickCssHeight).toBeGreaterThanOrEqual(12);
		expect(tickCssHeight).toBeLessThanOrEqual(16);
	});
});

test.describe('Canvas DPR 2 — B9 regression', () => {
	// Forced at the describe level: test.use() is only valid there.
	// devicePixelRatio 2 makes the regression observable on any machine —
	// on a DPR-1 runner a smeared, CSS-stretched bitmap (ratio ≈ 1) is
	// indistinguishable from a correct one.
	test.use({ deviceScaleFactor: 2 });

	const LINE = 'canvas[aria-label^="Spectrum line"]';
	// The tick canvas is the only one without an aria-label.
	const TICK = 'canvas:not([aria-label])';

	test('text canvases are DPR-scaled, not CSS-stretched', async ({
		page,
	}) => {
		await page.goto('/spectrum');

		const dpr = await page.evaluate(() => window.devicePixelRatio);
		expect(dpr).toBe(2);

		for (const sel of [LINE, TICK]) {
			const canvas = page.locator(sel);
			await expect(canvas).toBeAttached({ timeout: 10_000 });

			// Give the render loop a frame to fitCanvas() the bitmap,
			// then require the bitmap to track the CSS box × DPR.  A
			// fixed bitmap stretched by CSS would pin the ratio at 1.
			await expect
				.poll(
					() =>
						canvas.evaluate((el) => {
							const c = el as HTMLCanvasElement;
							const cssW = parseFloat(
								window.getComputedStyle(c).width
							);
							return cssW > 0 ? c.width / cssW : -1;
						}),
					{ timeout: 5_000 }
				)
				.toBeGreaterThanOrEqual(1.9);

			const ratio = await canvas.evaluate((el) => {
				const c = el as HTMLCanvasElement;
				const cssW = parseFloat(window.getComputedStyle(c).width);
				return cssW > 0 ? c.width / cssW : -1;
			});
			expect(ratio, `${sel} bitmap must track devicePixelRatio`)
				.toBeGreaterThanOrEqual(1.9);
			expect(ratio).toBeLessThanOrEqual(2.1);
		}
	});
});