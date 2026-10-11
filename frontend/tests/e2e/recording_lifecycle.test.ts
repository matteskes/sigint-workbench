/**
 * Category 4 — Recording Lifecycle (from E2E-TEST-SUITE.md, Test 4.2)
 *
 * The recorder creates sessions from pipeline state (signal-processor →
 * recorder, §10.3), and the ws-hub ingest endpoint is broadcast-only
 * (§2.2) — event injection cannot create a session.  Scenario tests
 * that need a *created* recording (4.2.1, 4.2.4) are therefore fixme'd
 * here and covered by the recorder Go suite (`make test`); what the UI
 * suite can honestly assert is the gateway's `/api/recordings`
 * contract: relative file paths (§10.4) and complete metadata.
 *
 * @module recording_lifecycle
 */

import { test, expect } from '@playwright/test';
import { getJson, servicesReady, GATEWAY_HTTP } from './gateway_helpers';

interface RecordingEntry {
  id?: string;
  file_path?: string;
  status?: string;
  start_time?: string;
  signal_id?: string;
  sdr_id?: string;
}

async function recordings(): Promise<{
  status: number;
  body: RecordingEntry[];
}> {
  const { status, body } = await getJson(`${GATEWAY_HTTP}/api/recordings`);
  return {
    status,
    body: Array.isArray(body) ? (body as RecordingEntry[]) : [],
  };
}

test.describe('Recording REST contract — 4.2 (from E2E-TEST-SUITE.md)', () => {
  let servicesUp = false;
  test.beforeAll(async () => {
  	servicesUp = await servicesReady();
  });
  test.skip(() => !servicesUp, 'No services running — skipping recording contract test');

  test.fixme('4.2.1 — recording session creation and file serving', async () => {
    // Needs the real pipeline: a live signal must reach the recorder to
    // open a session (signal-processor → recorder, §10.3).  Hub ingest
    // is broadcast-only, so UI-level injection cannot create one — the
    // original suite posted events and then waited 10 s for a recording
    // that could never appear.  Covered by internal/record Go tests;
    // the iq-ingest frame-injection variant belongs in the API
    // integration suite (TEST_DATABASE_URL).
  });

  test('4.2.2 — served file paths are relative, never absolute', async () => {
    const { status, body } = await recordings();
    expect(status).toBe(200);
    for (const rec of body) {
      // file_path is served relative to the recordings root (§10.4);
      // an absolute path would leak the container filesystem layout.
      if (rec.file_path) {
        expect(
          rec.file_path.startsWith('/'),
          `${rec.file_path} must be relative`
        ).toBe(false);
      }
      if (rec.status === 'finalized') {
        expect(
          rec.file_path,
          'finalized recordings must have a file path'
        ).toBeTruthy();
      }
    }
  });

  test('4.2.3 — recording metadata is complete', async () => {
    const { status, body } = await recordings();
    expect(status).toBe(200);
    for (const rec of body) {
      expect(
        rec.signal_id,
        `recording ${rec.id} must reference a signal`
      ).toBeTruthy();
      expect(
        rec.sdr_id,
        `recording ${rec.id} must reference an SDR`
      ).toBeTruthy();
      if (rec.start_time) {
        expect(Number.isNaN(Date.parse(rec.start_time))).toBe(false);
      }
    }
  });

  test.fixme('4.2.4 — empty recording cleanup (no phantom files)', async () => {
    // Needs the real pipeline: whether a short-lived signal produces a
    // recording is recorder behaviour (§10.3, B18), not observable via
    // broadcast-only ingest.  Covered by the recorder Go suite.
  });
});
