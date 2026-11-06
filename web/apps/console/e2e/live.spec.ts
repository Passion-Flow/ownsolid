// Runs the sign-in flow in a real browser against a real installation, through the
// control plane's own TLS, cookies, CSRF checks and Content-Security-Policy. Skipped
// unless the environment describes an installation; docs/development/environment.md
// has the recipe.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expectAccessible, expectFocusVisible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

// A real provider for the link round trip; its steps are skipped without one.
const providerName = process.env.OWNSOLID_LIVE_PROVIDER_NAME ?? '';
const providerOrigin = process.env.OWNSOLID_LIVE_PROVIDER_ORIGIN ?? '';
const providerUser = process.env.OWNSOLID_LIVE_PROVIDER_USER ?? '';
const providerPasswordFile = process.env.OWNSOLID_LIVE_PROVIDER_PASSWORD_FILE ?? '';

test.skip(origin === '', 'needs OWNSOLID_LIVE_ORIGIN');
test.use(liveOptions);

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
  const encodings = new Set<string>();
  page.on('response', (response) => {
    if (response.request().resourceType() === 'script') {
      scripts.set(new URL(response.url()).pathname, response.status());
      encodings.add(response.headers()['content-encoding'] ?? 'identity');
    }
  });
  const password = adminPassword();
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
  await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
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
  await page.getByPlaceholder(/^Type a command/).fill('compact');
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
    await page.getByRole('button', { name: 'System', exact: true }).click();
    await page.getByRole('navigation').getByRole('link', { name: 'Email delivery' }).click();
    await expect(page.getByRole('heading', { name: 'Email delivery', level: 1 })).toBeFocused();
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    // The list pattern against the real API: a saved view, a recipient filter and the drawer.
    await page.getByLabel('Filter by recipient').fill(email.slice(0, 4));
    await expect(page.getByRole('row').nth(1)).toContainText(email);
    // The filter is in the address, so reloading or sharing it shows the same list.
    await expect(page).toHaveURL(new RegExp(`recipient=${email.slice(0, 4)}`));
    await page.reload();
    await expect(page.getByLabel('Filter by recipient')).toHaveValue(email.slice(0, 4));
    await page.getByLabel('Filter by recipient').fill('');
    await page.getByRole('button', { name: /^Views: / }).click();
    await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
    const viewName = `Live ${String(Date.now())}`;
    await page
      .getByRole('dialog', { name: 'Save this view' })
      .getByLabel('View name')
      .fill(viewName);
    await page
      .getByRole('dialog', { name: 'Save this view' })
      .getByRole('button', { name: 'Save' })
      .click();
    await expect(page.getByRole('button', { name: `Views: ${viewName}` })).toBeVisible();
    await page
      .getByRole('button', { name: /^Show details of the email to / })
      .first()
      .click();
    await expect(page.getByRole('dialog').getByText('Email ID')).toBeVisible();
    await expectAccessible(page);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: /^Views: / }).click();
    await page.getByRole('menuitem', { name: 'Delete this view' }).click();
    await expect(page.getByRole('button', { name: 'Views: Default view' })).toBeVisible();
    // Undo brings the view back through the real API, and it can then be deleted for good.
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Change undone' })).toBeVisible();
    await page.getByRole('button', { name: /^Views: / }).click();
    await page.getByRole('menuitem', { name: viewName }).click();
    await page.getByRole('button', { name: /^Views: / }).click();
    await page.getByRole('menuitem', { name: 'Delete this view' }).click();
    await expect(page.getByRole('button', { name: 'Views: Default view' })).toBeVisible();

    // An invitation can only be revoked by typing its address, and that cannot be undone.
    const invitee = `revoke-${String(Date.now())}@example.invalid`;
    const invited = await page.evaluate(async (address) => {
      const csrf = (await (await fetch('/api/v1/auth/csrf')).json()) as { data: { token: string } };
      const response = await fetch('/api/v1/admin/invitations', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrf.data.token,
          'Idempotency-Key': crypto.randomUUID(),
        },
        body: JSON.stringify({ email: address, locale: 'en' }),
      });
      return response.status;
    }, invitee);
    expect(invited).toBe(201);
    const showEmails = page.getByRole('group', { name: 'Show emails' });
    await showEmails.getByRole('button', { name: 'All' }).click();
    await page.getByLabel('Filter by recipient').fill(invitee);
    await page.getByRole('button', { name: `Show details of the email to ${invitee}` }).click();
    await page.getByRole('button', { name: 'Revoke the invitation' }).click();
    const revoke = page.getByRole('dialog', { name: 'Revoke this invitation' });
    await expectAccessible(page);
    await revoke.getByLabel(`Type ${invitee} to confirm`).fill(invitee);
    await revoke.getByRole('button', { name: 'Revoke the invitation' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: `Invitation to ${invitee} revoked` }),
    ).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await page.getByLabel('Filter by recipient').fill('');
    await showEmails.getByRole('button', { name: 'Failed' }).click();
    const failed = page
      .getByRole('row')
      .filter({ hasText: 'The mail server could not be reached' });
    await expect(failed.first()).toBeVisible();
    await expectAccessible(page);
    await failed
      .first()
      .getByRole('button', { name: /^Send again to / })
      .click();
    // Only the newest email of its subject can be sent again, and earlier runs leave newer
    // ones behind, so a clear refusal is as correct here as a resend.
    const resent = page.getByRole('status').filter({ hasText: 'Email queued again' });
    const refused = page.getByRole('alert').filter({ hasText: 'This email cannot be sent again' });
    await resent.or(refused).first().waitFor();
    if (await resent.isVisible()) {
      await page
        .getByRole('group', { name: 'Show emails' })
        .getByRole('button', { name: 'Waiting' })
        .click();
      await expect(page.getByRole('row').nth(1)).toBeVisible();
    }
  }

  // The account page: a rename, then the password and back, so the next run still signs in.
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Account' }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeFocused();
  await expect(page).toHaveTitle('Account - ownsolid');
  await expect(page.getByText(email)).toBeVisible();
  await expectAccessible(page);
  await expectFocusVisible(page);
  // Dates follow the interface language.
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: chinese['locale.zh-CN'] }).click();
  await expect(page.getByText(/\d{4}\u5e74\d{1,2}\u6708\d{1,2}\u65e5/).first()).toBeVisible();
  // The choice follows the account to the other browser, and automatic takes it back.
  await second.reload();
  await expect(second.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await page.getByLabel(chinese['account.language.label']).selectOption('auto');
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  await second.reload();
  await expect(second.locator('html')).toHaveAttribute('lang', 'en');
  if (!origin.includes('portal')) {
    // The tenant's default reaches someone who has not signed in, then goes back.
    const setDefault = async (value: string) => {
      await page.goto('/settings');
      await page.getByLabel('Default language').selectOption(value);
      await page.getByRole('button', { name: 'Save' }).click();
      await expect(page.locator('html')).toHaveAttribute('lang', value === 'auto' ? 'en' : value);
    };
    await setDefault('zh-CN');
    const visitorContext = await browser.newContext({ baseURL: origin });
    const visitor = await visitorContext.newPage();
    await visitor.goto('/sign-in');
    await expect(visitor.locator('html')).toHaveAttribute('lang', 'zh-CN');
    await visitorContext.close();
    await page.goto('/settings');
    await page.getByLabel(chinese['settings.language.default']).selectOption('auto');
    await page.getByRole('button', { name: chinese['settings.save'] }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await page.goto('/account');
    await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  }
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
    // A provider that remembers an earlier grant returns without asking again.
    const authorize = page.locator('button#authorize-app');
    const linked = page.getByText('The account is linked');
    await authorize.or(linked).first().waitFor();
    if (await authorize.isVisible()) await authorize.click();
    await expect(linked).toBeVisible();
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
    // Linking signed this browser in through the provider, so unlinking ended the session.
    await expect(page).toHaveURL(/\/sign-in/);
    await page.getByLabel('Email').fill(email);
    await page.getByLabel('Password').fill(password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
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
  // The browser accepts Brotli, so the build's precompressed copies are what it receives.
  expect(encodings).toContain('br');
  for (const chunk of [
    '/sign-in-page-',
    '/app-frame-',
    '/overview-page-',
    '/password-pages-',
    '/account-page-',
  ]) {
    expect([...scripts.keys()].some((path) => path.includes(chunk))).toBe(true);
  }
  expect(problems).toEqual([]);
});
