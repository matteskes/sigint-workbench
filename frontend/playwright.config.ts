import { defineConfig } from '@playwright/test';

/**
 * Playwright configuration for SIGINT Workbench e2e frontend tests.
 *
 * The dev web server (SvelteKit/Vite on :5173) is started automatically;
 * the Go stack (api-gateway :8080, ws-hub :8081, recorder, …) is *not* —
 * tests that need it gate themselves via `servicesReady()` in
 * `tests/e2e/gateway_helpers.ts` and skip with a clear reason.
 *
 * Chromium is the single project, pinned with an explicit browserName.
 *
 * History: the project used to be named "webkit" and spread
 * devices['Desktop WebKit'] — a name that exists in no Playwright
 * device registry (the real entry is "Desktop Safari"), so the spread
 * was a silent no-op and browserName fell back to Playwright's
 * default engine, Chromium. The first GitHub Actions run of e2e-ui
 * (2026-10-10) exposed this: CI installed only webkit, so all 60
 * browser launches failed on a missing chromium_headless_shell. No
 * global deviceScaleFactor is set — every prior validation pass ran
 * at DPR 1, and the canvas DPR regression (B9) forces
 * deviceScaleFactor: 2 itself in canvas_dpr.test.ts.
 */
export default defineConfig({
	testDir: './tests/e2e',
	testMatch: /.*\.test\.ts/,
	timeout: 30_000,
	use: {
		baseURL: 'http://localhost:5173',
		trace: 'on-first-retry',
	},
	webServer: {
		command: 'npm run dev -- --port 5173',
		url: 'http://localhost:5173',
		reuseExistingServer: !process.env.CI,
		timeout: 60_000,
	},
	retries: process.env.CI ? 1 : 0,
	workers: 1,
	reporter: [['list'], ['html', { open: 'never' }]],
	projects: [
		{
			name: 'chromium',
			use: { browserName: 'chromium', viewport: { width: 1280, height: 800 } },
		},
	],
});