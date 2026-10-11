import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for SIGINT Workbench e2e frontend tests.
 *
 * The dev web server (SvelteKit/Vite on :5173) is started automatically;
 * the Go stack (api-gateway :8080, ws-hub :8081, recorder, …) is *not* —
 * tests that need it gate themselves via `servicesReady()` in
 * `tests/e2e/gateway_helpers.ts` and skip with a clear reason.
 *
 * WebKit is the single project on purpose: it runs at devicePixelRatio 2
 * on macOS dev machines, which is what the canvas DPR regression (B9)
 * needs to stay observable.
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
			name: 'webkit',
			use: { ...devices['Desktop WebKit'], viewport: { width: 1280, height: 800 } },
		},
	],
});