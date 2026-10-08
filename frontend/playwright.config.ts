import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for SIGINT Workbench e2e frontend tests.
 *
 * All tests target a live Vite dev server at `http://localhost:5173`.
 * Start it first (`cd frontend && npm run dev`) before running.
 */
export default defineConfig({
	testDir: './tests/e2e',
	testMatch: /.*\.test\.ts/,
	timeout: 30_000,
	use: {
		baseURL: 'http://localhost:5173',
		trace: 'on-first-retry',
	},
	retries: process.env.CI ? 1 : 0,
	workers: 1,
	reporter: 'html',
	projects: [
		{
			name: 'webkit',
			use: { ...devices['Desktop WebKit'], viewport: { width: 1280, height: 800 } },
		},
	],
});