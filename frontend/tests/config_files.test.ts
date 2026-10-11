/// <reference types="node" />
/**
 * Category 5.3 — Dead Config Cleanup Verification (from E2E-TEST-SUITE.md
 * §4.5, Test 5.3; from STACK-AUDIT A3, A4, A8, A13).
 *
 * These are static repo checks, not browser tests — they run under
 * vitest (`npm run test`) instead of Playwright.  Each one pins a
 * configuration regression the audit found and fixed, so the dead
 * config stays dead:
 *
 * - A3: the `/ws` proxy must target the api-gateway (the single client
 *   ingress, §2.2), with WebSocket upgrade headers — not the hub.
 * - A8: the processor YAML must not carry the unread `scan:` block.
 * - §2.2/§3.2: ws-hub stays loopback-only in compose (internal service).
 * - §17.2: `.env.example` documents the browser origin allowlist.
 * - A13: no dead exports in `lib/map/config.ts`.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const FRONTEND_ROOT = resolve(__dirname, '..');
const REPO_ROOT = resolve(FRONTEND_ROOT, '..');

const read = (...parts: string[]): string =>
	readFileSync(join(REPO_ROOT, ...parts), 'utf8');

describe('dead config cleanup (5.3, A3/A4/A8/A13)', () => {
	it('signal-processor.yaml has no dead top-level scan: block (A8)', () => {
		const yaml = read('config', 'signal-processor.yaml');
		// The original audit found a `scan:` block that the processor
		// never read (A8).  Only a comment about its removal may remain.
		const activeScanKey = yaml
			.split('\n')
			.filter((line) => !line.trim().startsWith('#'))
			.some((line) => /^scan:\s*(\S|$)/.test(line));
		expect(activeScanKey, 'dead `scan:` key resurfaced').toBe(false);
	});

	it('nginx /ws proxies to the api-gateway with upgrade headers (A3)', () => {
		const nginx = read('docker', 'nginx.conf');
		const wsBlock = nginx.slice(
			nginx.indexOf('location /ws'),
			nginx.indexOf('location /_app/')
		);
		expect(wsBlock).toContain('proxy_pass http://api-gateway:8080;');
		expect(wsBlock).toContain('proxy_set_header Upgrade');
		expect(wsBlock).toContain('Connection "upgrade"');
		// The hub is internal-only (§2.2): no production proxy may point
		// browsers straight at :8081.
		expect(nginx).not.toContain('proxy_pass http://ws-hub:8081');
	});

	it('nginx /api/ proxies to the api-gateway (A3)', () => {
		const nginx = read('docker', 'nginx.conf');
		expect(nginx).toContain('location /api/');
		expect(nginx).toContain('proxy_pass http://api-gateway:8080;');
	});

	it('docker-compose keeps ws-hub loopback-only (§2.2, §3.2)', () => {
		const compose = read('docker-compose.yml');
		const hubStart = compose.indexOf('  ws-hub:');
		expect(hubStart).toBeGreaterThan(-1);
		// The service block runs to the next top-level key (a line that
		// starts at column 0) or EOF.
		const afterKey = compose.indexOf('\n', hubStart);
		const nextTop = /\n\S/.exec(compose.slice(afterKey));
		const hubEnd = nextTop ? afterKey + nextTop.index + 1 : compose.length;
		const hubBlock = compose.slice(hubStart, hubEnd);
		expect(hubBlock).toContain('127.0.0.1:8081:8081');
		// Not reachable from other hosts / the LAN:
		expect(hubBlock).not.toMatch(/^\s*- "?8081:8081/m);
	});

	it('.env.example documents the browser origin allowlist (§17.2)', () => {
		const envExample = read('.env.example');
		expect(envExample).toContain('ALLOWED_ORIGINS');
		expect(envExample).toContain('http://localhost:5173');
	});

	it('lib/map/config.ts has no dead exports (A13)', () => {
		const configSource = read('frontend', 'src', 'lib', 'map', 'config.ts');
		const exported = new Set<string>();
		for (const m of configSource.matchAll(/export const (\w+)/g)) {
			exported.add(m[1]);
		}
		for (const m of configSource.matchAll(/export \{([^}]+)\}/g)) {
			for (const name of m[1].split(',')) {
				const clean = name.trim().split(/\s+as\s+/).pop()?.trim();
				if (clean) exported.add(clean);
			}
		}
		expect(exported.size, 'expected at least one export to check').toBeGreaterThan(0);

		// Every exported name must be imported somewhere else in src/.
		const srcFiles = readDirRecursive(join(FRONTEND_ROOT, 'src'));
		const usage = new Map<string, number>(
			[...exported].map((name) => [name, 0])
		);
		const importRe = new RegExp(
			`import\\s*\\{[^}]*\\b(${[...exported].join('|')})\\b[^}]*\\}`,
			'g'
		);
		for (const file of srcFiles) {
			const content = readFileSync(file, 'utf8');
			for (const m of content.matchAll(importRe)) {
				usage.set(m[1], (usage.get(m[1]) ?? 0) + 1);
			}
		}
		for (const [name, count] of usage) {
			expect(
				count,
				`\`${name}\` is exported from lib/map/config.ts but never imported (A13 dead export)`
			).toBeGreaterThan(0);
		}
	});
});

/** Recursively list files under `dir` (skipping node_modules). */
function readDirRecursive(dir: string): string[] {
	const out: string[] = [];
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		if (entry.name === 'node_modules') continue;
		const full = join(dir, entry.name);
		if (statSync(full).isDirectory()) out.push(...readDirRecursive(full));
		else out.push(full);
	}
	return out;
}