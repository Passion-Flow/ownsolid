// Runs the sign-in flow in a real browser against a real installation, through the
// control plane's own TLS, cookies, CSRF checks and Content-Security-Policy. Skipped
// unless the environment describes an installation; docs/development/environment.md
// has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
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
  // Pages load their code with their route; every file must come from this installation.
  const scripts = new Map<string, number>();
  page.on('response', (response) => {
    if (response.request().resourceType() === 'script') {
      scripts.set(new URL(response.url()).pathname, response.status());
    }
  });
  const password = (JSON.parse(readFileSync(passwordFile, 'utf8')) as { password: string })
    .password;

  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);
  await page.getByLabel('Email').waitFor();
  await expectAccessible(page);

  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(`${password}-wrong`);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('Email or password is incorrect');

  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await expect(page.getByText(`Signed in as ${email}.`)).toBeVisible();
  await expectAccessible(page);

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
  await expectAccessible(page);

  await page.keyboard.press('ControlOrMeta+k');
  await page.getByPlaceholder('Type a command or a page name').fill('compact');
  await page.keyboard.press('Enter');
  await expect(page.locator('html')).toHaveAttribute('data-density', 'compact');
  await page.getByRole('button', { name: /^Notifications/ }).click();
  await expect(page.getByRole('dialog', { name: 'Notifications' })).toContainText(
    'No notifications',
  );
  await page.keyboard.press('Escape');

  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);

  // The account pages emails and sign-in providers link to, against the real API.
  await page.getByRole('link', { name: 'Forgot your password?' }).click();
  // The sign-in page stays until the next page's code has loaded.
  await expect(page.getByRole('heading', { name: 'Reset your password' })).toBeVisible();
  await page.getByLabel('Email').fill(email);
  await page.getByRole('button', { name: 'Send the reset link' }).click();
  await expect(page.getByText('If this address has an account')).toBeVisible();

  const unusable = 'b'.repeat(43);
  await page.goto(`/reset-password#token=${unusable}`);
  await page.getByLabel('New password').fill('a sufficiently long password for this test');
  await page.getByLabel('Confirm the password').fill('a sufficiently long password for this test');
  await page.getByRole('button', { name: 'Set the new password' }).click();
  await expect(page.getByRole('alert')).toContainText('This reset link cannot be used');

  await page.goto('/login/sso-result?error=SSO_PROVIDER_DENIED');
  await expect(page.getByRole('alert')).toContainText('The provider refused the sign-in');

  await page.goto(`/register#token=${unusable}`);
  if (origin.includes('portal')) {
    await expect(page.getByRole('alert')).toContainText('This invitation cannot be used');
  } else {
    await expect(page.getByText('Page not found')).toBeVisible();
  }
  expect([...scripts].filter(([, status]) => status >= 400)).toEqual([]);
  for (const chunk of ['/sign-in-page-', '/app-frame-', '/welcome-page-', '/password-pages-']) {
    expect([...scripts.keys()].some((path) => path.includes(chunk))).toBe(true);
  }
  expect(problems).toEqual([]);
});
