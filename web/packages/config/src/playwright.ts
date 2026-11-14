// Shared Playwright configuration for browser tests of the built applications.
import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test';

/** Runs an application's `e2e/` specs in Chromium against `vite preview` of its build. */
export function browserConfig({ port }: { port: number }): PlaywrightTestConfig {
  const offset = Number(process.env.OWNSOLID_PLAYWRIGHT_PORT_OFFSET ?? '0');
  const previewPort = port + offset;
  if (!Number.isInteger(offset) || previewPort < 1024 || previewPort > 65535) {
    throw new Error('OWNSOLID_PLAYWRIGHT_PORT_OFFSET must select an unprivileged TCP port');
  }
  const url = `http://localhost:${previewPort}`;
  return defineConfig({
    testDir: './e2e',
    outputDir: './test-results',
    fullyParallel: true,
    forbidOnly: Boolean(process.env.CI),
    retries: 0,
    reporter: [['list']],
    use: { baseURL: url, trace: 'retain-on-failure' },
    projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
    webServer: {
      command: `pnpm exec vite preview --port ${previewPort} --strictPort`,
      url,
      reuseExistingServer: false,
    },
  });
}
