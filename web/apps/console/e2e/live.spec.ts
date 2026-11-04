// Runs the sign-in flow in a real browser against a real installation, through the
// control plane's own TLS, cookies, CSRF checks and Content-Security-Policy. Skipped
// unless the environment describes an installation; docs/development/environment.md
// has the recipe.
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

const origin = process.env.OWNSOLID_LIVE_ORIGIN ?? '';
const email = process.env.OWNSOLID_LIVE_EMAIL ?? '';
const passwordFile = process.env.OWNSOLID_LIVE_PASSWORD_FILE ?? '';
// A private CA is trusted by pinning the certificate's public key, so the browser still
// refuses any other certificate. The host names resolve through the same flags.
const spki = process.env.OWNSOLID_LIVE_SPKI ?? '';
const hostRules = process.env.OWNSOLID_LIVE_HOST_RULES ?? '';

test.skip(origin === '', 'needs OWNSOLID_LIVE_ORIGIN');
test.use({
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
});

test('signs in and out of a real installation', async ({ page }) => {
  const problems: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
      problems.push(message.text());
    }
  });
  page.on('request', (request) => {
    if (!request.url().startsWith(origin)) problems.push(`outside request: ${request.url()}`);
  });
  const password = (JSON.parse(readFileSync(passwordFile, 'utf8')) as { password: string })
    .password;

  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);

  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(`${password}-wrong`);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('Email or password is incorrect');

  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await expect(page.getByText(`Signed in as ${email}.`)).toBeVisible();

  await page.reload();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();

  await expect(page.locator('body')).toHaveCSS('font-family', /^"Figtree Variable"/);
  const fonts = await page.evaluate(async () => {
    await document.fonts.ready;
    return [...document.fonts].filter((font) => font.status === 'loaded').length;
  });
  expect(fonts).toBeGreaterThan(0);
  await page.getByRole('button', { name: 'Use the dark theme' }).click();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(22, 21, 19)');

  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);
  expect(problems).toEqual([]);
});
