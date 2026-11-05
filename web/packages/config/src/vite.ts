// Shared Vite and Vitest configuration for the console, the portal and the packages.
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';
import { defineConfig, type TestUserConfig, type ViteUserConfig } from 'vitest/config';
import { bundleBudgetPlugin, type BundleBudget } from './bundle-budget.ts';

const setupFile = fileURLToPath(new URL('./vitest-setup.ts', import.meta.url));

/** Test settings for packages and applications: jsdom, Testing Library matchers. */
export const testConfig: TestUserConfig = {
  environment: 'jsdom',
  globals: true,
  setupFiles: [setupFile],
  include: ['src/**/*.test.{ts,tsx}', 'locales/**/*.test.ts'],
  restoreMocks: true,
};

/** Vitest configuration for a package that has no application build. */
export function packageConfig(): ViteUserConfig {
  return defineConfig({ test: testConfig });
}

/** Options for {@link appConfig}. */
export interface AppOptions {
  /** Port for `vite dev` and `vite preview`; each application has its own. */
  port: number;
}

/**
 * The Content-Security-Policy the control plane sends with every page. `vite preview`
 * sends the same header so browser tests fail on anything it would block; a test
 * keeps this copy equal to the control plane's.
 */
export const contentSecurityPolicy =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'";

// The bundled fonts and icons; their licenses require the notices to ship with them.
const noticedPackages = [
  '@fontsource-variable/figtree',
  '@fontsource-variable/jetbrains-mono',
  'lucide-react',
];

/** The license texts of the bundled fonts and icons. */
export function thirdPartyNotices(): string {
  return noticedPackages
    .map((name) => {
      const file = new URL(`../../ui/node_modules/${name}/LICENSE`, import.meta.url);
      return `${name}\n\n${readFileSync(file, 'utf8').trim()}\n`;
    })
    .join('\n---\n\n');
}

function noticesPlugin(): Plugin {
  return {
    name: 'ownsolid-third-party-notices',
    apply: 'build',
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'assets/third-party-notices.txt',
        source: thirdPartyNotices(),
      });
    },
  };
}

/**
 * The size budget of both applications, in gzip bytes. Set in November 2026, when the
 * first download measured 138 kB and the largest later chunk, the signed-in frame,
 * 39 kB; the headroom is for the shared code P1 pages add. A page that needs more
 * loads it with its route; raising a limit is a reviewed change (ADR 0016).
 */
export const bundleBudget: BundleBudget = { initial: 160_000, chunk: 60_000 };

// The control plane answers these paths; the development server forwards them.
const controlPaths = ['/api', '/runtime-config.json', '/health'];

/**
 * Vite configuration for an application.
 *
 * `vite dev` forwards API calls to the control-plane origin named by the
 * OWNSOLID_DEV_ORIGIN environment variable, for example
 * `https://admin.ownsolid.test:8443`. The certificate is verified; point
 * NODE_EXTRA_CA_CERTS at the installation's CA file when it is private.
 */
export function appConfig({ port }: AppOptions): ViteUserConfig {
  const origin = process.env.OWNSOLID_DEV_ORIGIN;
  const proxy = origin
    ? Object.fromEntries(
        controlPaths.map((path) => [
          path,
          // The control plane accepts writes only from its own origin.
          { target: origin, changeOrigin: true, secure: true, headers: { Origin: origin } },
        ]),
      )
    : undefined;
  return defineConfig({
    plugins: [react(), tailwindcss(), noticesPlugin(), bundleBudgetPlugin(bundleBudget)],
    build: {
      target: 'es2023',
      sourcemap: false,
      // The control plane's Content-Security-Policy allows fonts and scripts only from
      // its own origin, so no asset may be inlined as a data: URL.
      assetsInlineLimit: 0,
    },
    server: { port, strictPort: true, ...(proxy ? { proxy } : {}) },
    preview: {
      port,
      strictPort: true,
      headers: { 'Content-Security-Policy': contentSecurityPolicy },
    },
    test: testConfig,
  });
}
