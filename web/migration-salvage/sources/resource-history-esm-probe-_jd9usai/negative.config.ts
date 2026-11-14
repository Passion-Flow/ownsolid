import { createRequire } from 'node:module';
const require = createRequire("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-history/web/apps/console/package.json");
const { defineConfig, devices } = require('@playwright/test');
export default defineConfig({
  testDir: "/Users/passion./.local/state/ownsolid/web-scopes/resource-history-esm-probe-_jd9usai/negative-probe",
  testMatch: 'resource-activity.spec.ts',
  fullyParallel: true,
  retries: 0,
  use: { baseURL: 'http://localhost:31037', trace: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    cwd: "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-help-44ab5229/web/apps/console",
    command: 'pnpm exec vite preview --port 31037 --strictPort',
    url: 'http://localhost:31037',
    reuseExistingServer: false,
  },
});
