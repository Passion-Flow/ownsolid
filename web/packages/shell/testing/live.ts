// What the specs that run against a real installation share. They are skipped unless the
// environment describes one; docs/development/environment.md has the recipe.
import { readFileSync } from 'node:fs';

/** The entrance to test. */
export const origin = process.env.OWNSOLID_LIVE_ORIGIN ?? '';

/** The administrator to sign in as. */
export const email = process.env.OWNSOLID_LIVE_EMAIL ?? '';

// A private CA is trusted by pinning the certificate's public key, so the browser still
// refuses any other certificate. The host names resolve through the same flags.
const spki = process.env.OWNSOLID_LIVE_SPKI ?? '';
const hostRules = process.env.OWNSOLID_LIVE_HOST_RULES ?? '';

/** Playwright options that reach the installation through its own TLS. */
export const liveOptions = {
  baseURL: origin,
  // The default headless shell ignores --host-resolver-rules.
  channel: 'chromium',
  launchOptions: {
    args: [
      '--no-proxy-server',
      `--ignore-certificate-errors-spki-list=${spki}`,
      `--host-resolver-rules=${hostRules}`,
    ],
  },
};

/** Reads the administrator's password from the installation's credential file. */
export function adminPassword(): string {
  const file = process.env.OWNSOLID_LIVE_PASSWORD_FILE ?? '';
  return (JSON.parse(readFileSync(file, 'utf8')) as { password: string }).password;
}
