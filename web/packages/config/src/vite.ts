// Shared Vite and Vitest configuration for the console, the portal and the packages.
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { defineConfig, type TestUserConfig, type ViteUserConfig } from 'vitest/config';

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
    plugins: [react(), tailwindcss()],
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
