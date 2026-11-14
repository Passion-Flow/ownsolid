// Shared Playwright configuration for browser tests of the built applications.
import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test';

/** Runs an application's `e2e/` specs in Chromium against `vite preview` of its build. */
export function browserConfig({ port }: { port: number }): PlaywrightTestConfig {
  return defineConfig({
    testDir: './e2e',
    outputDir: './test-results',
    fullyParallel: true,
    forbidOnly: Boolean(process.env.CI),
    retries: 0,
    reporter: [['list']],
    use: { baseURL: `http://localhost:${port}`, trace: 'retain-on-failure' },
    projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
    webServer: {
      command: 'pnpm exec vite preview',
      url: `http://localhost:${port}`,
      reuseExistingServer: false,
    },
  });
}
