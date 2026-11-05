// Runs the sign-in flow in a real browser against a real installation, through the
// control plane's own TLS, cookies, CSRF checks and Content-Security-Policy. Skipped
// unless the environment describes an installation; docs/development/environment.md
// has the recipe.
import { expectAccessible, expectFocusVisible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

const origin = process.env.OWNSOLID_LIVE_ORIGIN ?? '';
const email = process.env.OWNSOLID_LIVE_EMAIL ?? '';
const passwordFile = process.env.OWNSOLID_LIVE_PASSWORD_FILE ?? '';
// A private CA is trusted by pinning the certificate's public key, so the browser still
// refuses any other certificate. The host names resolve through the same flags.
const spki = process.env.OWNSOLID_LIVE_SPKI ?? '';
const hostRules = process.env.OWNSOLID_LIVE_HOST_RULES ?? '';
// A real provider for the link round trip; its steps are skipped without one.
const providerName = process.env.OWNSOLID_LIVE_PROVIDER_NAME ?? '';
const providerOrigin = process.env.OWNSOLID_LIVE_PROVIDER_ORIGIN ?? '';
const providerUser = process.env.OWNSOLID_LIVE_PROVIDER_USER ?? '';
const providerPasswordFile = process.env.OWNSOLID_LIVE_PROVIDER_PASSWORD_FILE ?? '';

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

test('signs in and out of a real installation', async ({ page, browser }) => {
  const problems: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
      problems.push(message.text());
    }
  });
  page.on('request', (request) => {
    const url = request.url();
    const expected = providerOrigin !== '' && url.startsWith(providerOrigin);
    if (!url.startsWith(origin) && !expected) problems.push(`outside request: ${url}`);
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
  let preferenceSaves = 0;
  page.on('response', (response) => {
    const request = response.request();
    if (request.method() === 'PATCH' && request.url().endsWith('/api/v1/preferences')) {
      expect(response.status()).toBe(200);
      preferenceSaves += 1;
    }
  });

  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);
  await page.getByLabel('Email').waitFor();
  await expect(page).toHaveTitle('Sign in - ownsolid');
  await expectAccessible(page);
  await expectFocusVisible(page);

  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(`${password}-wrong`);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('Email or password is incorrect');

  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await expect(page.getByText(`Signed in as ${email}.`)).toBeVisible();
  // Signing in is a navigation: focus moves to the start page's heading.
  await expect(page.getByRole('heading', { level: 1 })).toBeFocused();
  await expectAccessible(page);
  await expectFocusVisible(page);

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

  // A second browser remembers nothing, so what it shows comes from the account.
  await expect.poll(() => preferenceSaves).toBe(2);
  const elsewhere = await browser.newContext({ baseURL: origin });
  const second = await elsewhere.newPage();
  await second.goto('/');
  await second.getByLabel('Email').fill(email);
  await second.getByLabel('Password').fill(password);
  await second.getByRole('button', { name: 'Sign in' }).click();
  await expect(second.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await expect(second.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(second.locator('html')).toHaveAttribute('data-density', 'compact');

  // What changes there reaches this browser when the user comes back to it. This also
  // leaves the account as the next run expects it.
  await second.getByRole('button', { name: 'Use the light theme' }).click();
  await second.getByRole('button', { name: 'Use comfortable spacing' }).click();
  await expect(async () => {
    await page.evaluate(() => {
      window.dispatchEvent(new Event('visibilitychange'));
    });
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light', { timeout: 500 });
    await expect(page.locator('html')).toHaveAttribute('data-density', 'comfortable', {
      timeout: 500,
    });
  }).toPass();

  // Email delivery is a console page. A failed email from an earlier run, the installation
  // having no reachable mail server, is sent again and shows as waiting.
  if (!origin.includes('portal')) {
    await page.getByRole('link', { name: 'Email delivery' }).click();
    await expect(page.getByRole('heading', { name: 'Email delivery', level: 1 })).toBeFocused();
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    const failed = page
      .getByRole('row')
      .filter({ hasText: 'The mail server could not be reached' });
    await expect(failed.first()).toBeVisible();
    await expectAccessible(page);
    await failed
      .first()
      .getByRole('button', { name: /^Send again to / })
      .click();
    await expect(page.getByRole('status')).toContainText('Email queued again');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Waiting' })
      .click();
    await expect(page.getByRole('row').filter({ hasText: 'Password reset' }).first()).toBeVisible();
  }

  // The account page: a rename, then the password and back, so the next run still signs in.
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Account' }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeFocused();
  await expect(page).toHaveTitle('Account - ownsolid');
  await expect(page.getByText(email)).toBeVisible();
  await expectAccessible(page);
  await expectFocusVisible(page);
  const nameField = page.getByLabel('Display name');
  const originalName = await nameField.inputValue();
  const saveName = async (name: string) => {
    await nameField.fill(name);
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('button', { name: 'Account menu' })).toHaveAttribute('title', name);
  };
  await saveName(`${originalName} renamed`);
  await page.reload();
  await expect(nameField).toHaveValue(`${originalName} renamed`);
  await saveName(originalName);

  const changePassword = async (current: string, next: string) => {
    await page.getByLabel('Current password').fill(current);
    await page.getByLabel('New password').fill(next);
    await page.getByLabel('Confirm the password').fill(next);
    await page.getByRole('button', { name: 'Change password' }).click();
  };
  const changedPassword = `${password} changed`;
  await changePassword(`${password}-wrong`, changedPassword);
  await expect(page.getByText('The current password is not correct')).toBeVisible();
  await changePassword(password, changedPassword);
  await expect(
    page.getByRole('status').filter({ hasText: 'Other sessions were signed out' }),
  ).toBeVisible();
  // This browser holds the new session and can still write; the other browser's has ended.
  await page.reload();
  await expect(nameField).toHaveValue(originalName);
  await second.reload();
  await expect(second).toHaveURL(/\/sign-in/);
  await second.getByLabel('Email').fill(email);
  await second.getByLabel('Password').fill(changedPassword);
  await second.getByRole('button', { name: 'Sign in' }).click();
  await expect(second.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await elsewhere.close();
  await changePassword(changedPassword, password);
  await expect(
    page.getByRole('status').filter({ hasText: 'Other sessions were signed out' }),
  ).toBeVisible();

  if (providerName === '') {
    await expect(page.getByText('No providers to link')).toBeVisible();
  } else {
    // A real provider round trip: link at the provider, come back, then unlink.
    const providerPassword = readFileSync(providerPasswordFile, 'utf8').trim();
    await page.getByRole('button', { name: `Link ${providerName}` }).click();
    const linkDialog = page.getByRole('dialog', { name: `Link ${providerName}` });
    await linkDialog.getByLabel('Current password').fill(password);
    await expectAccessible(page);
    await linkDialog.getByRole('button', { name: 'Continue to the provider' }).click();
    await page.waitForURL(`${providerOrigin}/**`);
    await page.locator('input[name="user_name"]').fill(providerUser);
    await page.locator('input[name="password"]').fill(providerPassword);
    await page.locator('form[action*="login"] button.primary').click();
    const authorize = page.locator('button#authorize-app');
    await authorize.click();
    await page.waitForURL(`${origin}/account/sso-result*`);
    await expect(page.getByText('The account is linked')).toBeVisible();
    await page.getByRole('link', { name: 'Back to your account' }).click();
    await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
    await expect(page.getByText(/^Provider account \d+/)).toBeVisible();
    await expectAccessible(page);

    await page.getByRole('button', { name: `Unlink ${providerName}` }).click();
    const unlinkDialog = page.getByRole('dialog', { name: `Unlink ${providerName}` });
    await unlinkDialog.getByLabel('Current password').fill(`${password}-wrong`);
    await unlinkDialog.getByRole('button', { name: 'Unlink' }).click();
    await expect(unlinkDialog.getByText('The password is not correct')).toBeVisible();
    await unlinkDialog.getByLabel('Current password').fill(password);
    await unlinkDialog.getByRole('button', { name: 'Unlink' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: `${providerName} unlinked` }),
    ).toBeVisible();
    await expect(page.getByRole('button', { name: `Link ${providerName}` })).toBeVisible();
  }

  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.goto('/');
  await expect(page).toHaveURL(/\/sign-in/);

  // The account pages emails and sign-in providers link to, against the real API.
  await page.getByRole('link', { name: 'Forgot your password?' }).click();
  // The sign-in page stays until the next page's code has loaded.
  await expect(page.getByRole('heading', { name: 'Reset your password' })).toBeFocused();
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
  for (const chunk of [
    '/sign-in-page-',
    '/app-frame-',
    '/welcome-page-',
    '/password-pages-',
    '/account-page-',
  ]) {
    expect([...scripts.keys()].some((path) => path.includes(chunk))).toBe(true);
  }
  expect(problems).toEqual([]);
});
