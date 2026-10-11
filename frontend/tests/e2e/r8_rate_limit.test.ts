import { test, expect } from '@playwright/test';
import { getJson, servicesReady, GATEWAY_HTTP } from './gateway_helpers';

/**
 * Test R8 — API Rate Limit Behavior (§4.10.10, §13).
 *
 * The gateway enforces a per-client token bucket on `/api/*`
 * (`RATE_LIMIT_RPS` / `RATE_LIMIT_BURST`, stock defaults 50 req/s with
 * burst 100 — high enough that every other suite's polling never trips
 * it). Abusive clients get `429` + integer `Retry-After`, and service
 * resumes once the client respects the header.
 *
 * Coverage split (§7.2 precedent): this file runs contract steps 1–3.
 * Step 4 (a *second* client stays unaffected) is impossible from
 * Playwright — every request this suite can originate, Node-side fetch
 * and browser alike, leaves from 127.0.0.1 and lands in one gateway
 * bucket — so it is covered hermetically in Go instead
 * (`internal/api/ratelimit_test.go`, `TestRateLimiterPerClientIsolation`
 * with a fake clock). Because of that same shared bucket, this file
 * deliberately runs last-in-mind: the volley below drains the bucket
 * for ~2 s; with `workers: 1` no sibling test can run inside that
 * window, and the recovery step leaves ≥1 token in the bucket.
 *
 * The within-rate steps assert the *limiter's* promise — admission, not
 * handler success — by first reading a baseline status from
 * `/api/signals` (200 with a live database, 503 when the DB is down:
 * requireDB answers after the limiter passed the request through) and
 * requiring the volley to reproduce exactly that baseline.
 *
 * Spec: §4.10.10 (contract), §13 (gateway general contract).
 */
const servicesUp = await servicesReady();

/** Baseline status of a limited route with the limiter out of the way. */
async function baselineStatus(): Promise<number> {
  const res = await getJson(`${GATEWAY_HTTP}/api/signals`);
  expect([200, 503]).toContain(res.status);
  return res.status;
}

test.describe('Gateway rate limiting (R8, §4.10.10)', () => {
  test.skip(!servicesUp, 'No services running — skipping R8 rate limit test');

  test('within-rate polling keeps answering (contract step 1)', async () => {
    const baseline = await baselineStatus();
    // Well inside 50 req/s sustained: must never be limited.
    for (let i = 0; i < 10; i++) {
      const res = await getJson(`${GATEWAY_HTTP}/api/signals`);
      expect(res.status).toBe(baseline);
    }
  });

  test('burst beyond the bucket answers 429 + Retry-After (step 2)', async () => {
    const baseline = await baselineStatus();
    // 130 rapid sequential GETs: past the stock burst of 100 inside the
    // 50 req/s refill window, so the tail must hit the limiter.
    const statuses: number[] = [];
    let retryAfter: string | null = null;
    let limitedBody = '';
    for (let i = 0; i < 130 && !retryAfter; i++) {
      const resp = await fetch(`${GATEWAY_HTTP}/api/signals`);
      statuses.push(resp.status);
      retryAfter = resp.headers.get('retry-after');
      if (retryAfter) limitedBody = await resp.text();
      else await resp.text(); // drain so the connection returns to the pool
    }
    expect(retryAfter).not.toBeNull();
    // Everything before the limiter kicked in reproduced the baseline.
    for (const s of statuses.slice(0, -1)) {
      expect(s).toBe(baseline);
    }
    expect(statuses[statuses.length - 1]).toBe(429);
    // Integer seconds, at least 1 (RFC 9110 delay-seconds).
    const n = Number(retryAfter);
    expect(Number.isInteger(n)).toBe(true);
    expect(n).toBeGreaterThanOrEqual(1);
    expect(limitedBody).toContain('rate limit');
  });

  test('respecting Retry-After restores service (step 3)', async () => {
    const baseline = await baselineStatus();
    // Re-drain the bucket to get a fresh Retry-After promise.
    let retryAfter: string | null = null;
    for (let i = 0; i < 130 && !retryAfter; i++) {
      const resp = await fetch(`${GATEWAY_HTTP}/api/signals`);
      retryAfter = resp.headers.get('retry-after');
      await resp.text();
    }
    expect(retryAfter).not.toBeNull();
    const n = Number(retryAfter);

    // Respect the header — wait what it advertises, plus a small
    // scheduler margin — then the client is admitted again.
    await new Promise((resolve) => setTimeout(resolve, n * 1000 + 150));
    const res = await getJson(`${GATEWAY_HTTP}/api/signals`);
    expect(res.status).toBe(baseline);
  });
});
