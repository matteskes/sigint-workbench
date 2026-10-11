import { defineConfig } from 'vitest/config';

export default defineConfig({
	test: {
		environment: 'node',
		// Unit tests live next to their code (src/lib/**). `tests/*.test.ts`
		// holds static repo/config checks that need no browser; the
		// Playwright suite lives in tests/e2e/ and must stay out of the
		// vitest glob.
		include: ['src/**/*.test.ts', 'tests/*.test.ts']
	}
});