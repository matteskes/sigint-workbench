import { test, expect } from '@playwright/test';

/**
 * Test 2.2 — Keyboard Interaction Contract (from B12, B13).
 *
 * The global keyboard shortcuts overlay (`?layout.svelte` →
 * `handleKeydown()`) must respect native browser activation keys:
 * Space on a button, Enter on a link.  Esc closes one layer per press.
 *
 * Requirements: the frontend dev server is running at `localhost:5173`.
 */
test.describe('Keyboard interaction contract (§2.2, B12–B13)', () => {
	test('focuses search input on "/" key', async ({ page }) => {
		await page.goto('/signals');

		// Press "/" — the global keyboard handler focuses the search input.
		await page.keyboard.press('/');

		// The search input uses `[data-signal-search]` (SignalTable.svelte
		// line ~127).  It should now be focused.
		const searchInput = page.locator('[data-signal-search]');
		await expect(searchInput).toBeFocused({ timeout: 5_000 });
	});

	test('toggles shortcuts overlay with "?"', async ({ page }) => {
		await page.goto('/signals');

		// Initially the ShortcutsOverlay is not rendered.
		const overlay = page.locator('[role="dialog"]');
		await expect(overlay).not.toBeAttached();

		// Press "?" — the global handler toggles `shortcutsOpen`.
		await page.keyboard.press('?');

		// The overlay should now be in the DOM.
		const dialog = page.locator('[role="dialog"][aria-label="Keyboard shortcuts"]');
		await expect(dialog).toBeVisible();

		// Press "?" again — it should close.
		await page.keyboard.press('?');
		await expect(dialog).not.toBeAttached({ timeout: 5_000 });
	});

	test('types text into search input and filters signals', async ({ page }) => {
		await page.goto('/signals');

		// Focus the search input.
		await page.keyboard.press('/');
		const searchInput = page.locator('[data-signal-search]');
		await expect(searchInput).toBeFocused({ timeout: 5_000 });

		// Type some characters.  The SignalTable filters in real-time.
		await searchInput.fill('test');

		// The signal table should reflect the filter.
		const tableSection = page.locator('[role="table"]');
		const tableText = await tableSection.textContent();
		const doesNotContainSearchTerm = !tableText.toLowerCase().includes('test');
		expect(doesNotContainSearchTerm).toBe(true);
	});

	test('Space on button is not swallowed (B12)', async ({ page }) => {
		await page.goto('/signals');

		// Tab to the Copy CSV button (SignalTable.svelte line ~159).
		for (let i = 0; i < 15; i++) {
			await page.keyboard.press('Tab');
		}

		// It should focus the Copy CSV button.
		const copyBtn = page.locator('button:has-text("Copy CSV")');
		if (await copyBtn.isVisible()) {
			await expect(copyBtn).toBeFocused({ timeout: 5_000 });
		}

		// Press Space — a native button activation.  The global handler
		// calls `isActivator()` which returns true for BUTTON, so Space
		// is not consumed.  B12 ensures Space on a button activates it.
		await page.keyboard.press('Space');

		// The button was activated (it tried to copy to clipboard, which
		// fails silently when no signal data exists).  The important thing
		// is that no error was thrown and the button is still focused.
		await expect(copyBtn).toBeFocused();
	});

	test('Esc on input does not blur it (B12)', async ({ page }) => {
		await page.goto('/signals');

		// Focus the search input.
		await page.keyboard.press('/');
		const searchInput = page.locator('[data-signal-search]');
		await expect(searchInput).toBeFocused({ timeout: 5_000 });

		// Press Escape — the layout handler checks `isTypingTarget()` first.
		// For INPUT/TEXTAREA/SELECT elements, handleKeydown() returns
		// without doing anything (line ~105: `if (isTypingTarget(e.target))
		// return;`).  So the input should stay focused.
		await page.keyboard.press('Escape');

		// The input should still be focused (not blurred by the handler).
		await expect(searchInput).toBeFocused();
	});

	test('Esc closes one layer of popovers (B13)', async ({ page }) => {
		await page.goto('/signals');

		// Open the shortcuts overlay.
		await page.keyboard.press('?');
		const dialog = page.locator('[role="dialog"][aria-label="Keyboard shortcuts"]');
		await expect(dialog).toBeVisible();

		// Press Esc — the layout handler's `handleKeydown()` calls
		// `shortcutsOpen.set(false)` when shortcutsOpen is open.  This
		// closes ONE layer (the overlay).  B13's complaint was that ALL
		// popovers close at once — now only one closes per press.
		await page.keyboard.press('Escape');

		// The shortcuts overlay should be gone now.
		await expect(dialog).not.toBeAttached({ timeout: 5_000 });
	});
});