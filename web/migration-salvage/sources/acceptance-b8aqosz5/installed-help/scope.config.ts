import { defineConfig } from '@playwright/test';
import { join } from 'node:path';
import { liveOptions } from "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-4463bbe6/web/packages/shell/testing/live.ts";
import { sourcePath, sourceRevision } from './source.ts';

export default defineConfig({
  testDir: join(sourcePath, 'web/apps/portal/e2e'),
  testMatch: 'live-workspace-scope.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  metadata: { sourceRevision },
  use: { ...liveOptions, locale: 'en-US', viewport: { width: 1440, height: 900 }, trace: 'off', video: 'off', screenshot: 'off' },
  projects: [{ name: 'chromium' }],
});
