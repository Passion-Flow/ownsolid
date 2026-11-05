// The account page in a real browser, on both entrances: profile, password and sign-in
// providers, with their failures and retries.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import {
  account,
  giteaBinding,
  giteaConnector,
  mockControlPlane,
  type ControlPlane,
} from './control-plane.ts';

const nextPassword = 'another sufficiently long password';

async function fillPasswordForm(page: Page, current: string, next: string): Promise<void> {
  await page.getByLabel('Current password').fill(current);
  await page.getByLabel('New password').fill(next);
  await page.getByLabel('Confirm the password').fill(next);
  await page.getByRole('button', { name: 'Change password' }).click();
}

/** Registers the account flows in the current spec file. */
export function accountFlows(audience: ControlPlane['audience']): void {
  let control: ControlPlane;

  test.beforeEach(async ({ page }) => {
    control = await mockControlPlane(page, audience);
    control.signedIn = true;
  });

  test('reaches the page from the account menu and the command menu', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: 'Account' }).click();
    const heading = page.getByRole('heading', { name: 'Account', level: 1 });
    await expect(heading).toBeFocused();
    await expect(page).toHaveTitle('Account - ownsolid');

    await page.goto('/');
    await page.getByRole('heading', { name: 'Welcome, Ada Lovelace' }).waitFor();
    await page.keyboard.press('ControlOrMeta+k');
    await page.getByPlaceholder('Type a command or a page name').fill('Account');
    await page.keyboard.press('Enter');
    await expect(heading).toBeVisible();
  });

  test('saves the display name and shows it everywhere', async ({ page }) => {
    await page.goto('/account');
    const save = page.getByRole('button', { name: 'Save' });
    await expect(save).toBeDisabled();
    await expect(page.getByText(account.email)).toBeVisible();

    await page.getByLabel('Display name').fill('Ada King');
    await save.click();
    await expect(page.getByRole('status').filter({ hasText: 'Profile saved' })).toBeVisible();
    expect(control.user.display_name).toBe('Ada King');
    await expect(save).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Account menu' })).toHaveAttribute(
      'title',
      'Ada King',
    );

    await page.reload();
    await expect(page.getByLabel('Display name')).toHaveValue('Ada King');
  });

  test('refuses an empty name', async ({ page }) => {
    await page.goto('/account');
    await page.getByLabel('Display name').fill('   ');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText('Enter your name.')).toBeVisible();
    expect(control.user.display_name).toBe('Ada Lovelace');
  });

  test('recovers from a save that lost to another browser', async ({ page }) => {
    await page.goto('/account');
    await expect(page.getByLabel('Display name')).toBeVisible();
    // Another browser renames the account after this page loaded it.
    control.user = { ...control.user, version: control.user.version + 1 };
    await page.getByLabel('Display name').fill('Ada King');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText('This was changed by someone else')).toBeVisible();
    await expect(page.getByLabel('Display name')).toHaveValue('Ada King');

    // The refusal loaded the current version, so saving again goes through.
    await expect
      .poll(async () => {
        await page.getByRole('button', { name: 'Save' }).click();
        return control.user.display_name;
      })
      .toBe('Ada King');
  });

  test('offers a retry when saving fails on the server', async ({ page }) => {
    control.nextProfileSave = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/account');
    await page.getByLabel('Display name').fill('Ada King');
    await page.getByRole('button', { name: 'Save' }).click();
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Profile saved' })).toBeVisible();
    await expect(page.getByRole('alert')).toHaveCount(0);
  });

  test('changes the password and stays signed in', async ({ page }) => {
    await page.goto('/account');
    await fillPasswordForm(page, account.password, nextPassword);
    await expect(
      page.getByRole('status').filter({ hasText: 'Other sessions were signed out' }),
    ).toBeVisible();
    expect(control.password).toBe(nextPassword);
    await expect(page.getByLabel('Current password')).toHaveValue('');
    await expect(page.getByLabel('New password')).toHaveValue('');
    await expect(page.getByLabel('Confirm the password')).toHaveValue('');

    // The change replaced the session, and CSRF tokens are bound to it.
    const fetched = control.csrfFetches;
    await page.getByLabel('Display name').fill('Ada King');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Profile saved' })).toBeVisible();
    expect(control.csrfFetches).toBe(fetched + 1);
  });

  test('marks a wrong current password on its field', async ({ page }) => {
    await page.goto('/account');
    await fillPasswordForm(page, 'not the current password', nextPassword);
    const field = page.getByLabel('Current password');
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(page.getByText('The current password is not correct')).toBeVisible();
    await expect(page.getByRole('alert')).toHaveCount(0);
    expect(control.password).toBe(account.password);

    await field.fill(account.password);
    await page.getByRole('button', { name: 'Change password' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: 'Other sessions were signed out' }),
    ).toBeVisible();
  });

  test('explains password failures with their next step', async ({ page }) => {
    await page.goto('/account');
    await fillPasswordForm(page, account.password, account.password);
    await expect(page.getByRole('alert')).toContainText('This is already your password');
    await fillPasswordForm(page, account.password, 'too short');
    await expect(page.getByRole('alert')).toContainText('Use 15 to 128 characters.');
    control.nextPasswordChange = {
      status: 429,
      code: 'RATE_LIMITED',
      headers: { 'Retry-After': '60' },
    };
    await fillPasswordForm(page, account.password, nextPassword);
    await expect(page.getByRole('alert')).toBeVisible();
    expect(control.password).toBe(account.password);
  });

  test('checks entries before sending them', async ({ page }) => {
    await page.goto('/account');
    await page.getByRole('button', { name: 'Change password' }).click();
    await expect(page.getByText('Enter your current password.')).toBeVisible();
    await expect(page.getByText('Enter a password.')).toBeVisible();
    await page.getByLabel('Current password').fill(account.password);
    await page.getByLabel('New password').fill(nextPassword);
    await page.getByLabel('Confirm the password').fill('something else entirely');
    await page.getByRole('button', { name: 'Change password' }).click();
    await expect(page.getByText('The two passwords do not match.')).toBeVisible();
    expect(control.password).toBe(account.password);
  });

  test('links to the reset email for a forgotten password', async ({ page }) => {
    await page.goto('/account');
    await page.getByRole('link', { name: 'Forgot your password?' }).click();
    await expect(page.getByRole('heading', { name: 'Reset your password' })).toBeVisible();
  });

  test('says so when there are no providers', async ({ page }) => {
    await page.goto('/account');
    await expect(page.getByText('No providers to link')).toBeVisible();
    await expect(page.getByRole('button', { name: /^(Link|Unlink) / })).toHaveCount(0);
  });

  test('recovers when the providers could not load', async ({ page }) => {
    control.ssoBindingsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/account');
    await expect(page.getByLabel('Display name')).toBeVisible();
    const retry = page.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible();
    control.ssoBindingsProblem = null;
    await retry.click();
    await expect(page.getByText('No providers to link')).toBeVisible();
  });

  test('links a provider after confirming the password', async ({ page }) => {
    await page.route('**/idp.example/**', (route) =>
      route.fulfill({ contentType: 'text/html', body: 'provider' }),
    );
    control.ssoAddress = 'https://idp.example/authorize';
    control.ssoConnectors = [giteaConnector];
    await page.goto('/account');
    await page.getByRole('button', { name: 'Link Company Gitea' }).click();
    const dialog = page.getByRole('dialog', { name: 'Link Company Gitea' });
    await expect(dialog.getByLabel('Current password')).toBeFocused();

    await dialog.getByRole('button', { name: 'Continue to the provider' }).click();
    await expect(dialog.getByText('Enter your current password.')).toBeVisible();
    expect(control.ssoStarts).toHaveLength(0);

    await dialog.getByLabel('Current password').fill('wrong password');
    await dialog.getByRole('button', { name: 'Continue to the provider' }).click();
    await expect(dialog.getByText('The password is not correct')).toBeVisible();

    await dialog.getByLabel('Current password').fill(account.password);
    await dialog.getByRole('button', { name: 'Continue to the provider' }).click();
    await expect(page).toHaveURL('https://idp.example/authorize');
    expect(control.ssoStarts.at(-1)).toEqual({
      mode: 'link',
      password: account.password,
      reason: 'Linked from the account page',
    });
  });

  test('comes back to the account page after linking', async ({ page }) => {
    await page.goto('/account/sso-result?result=linked');
    await expect(page.getByText('The account is linked')).toBeVisible();
    await page.getByRole('link', { name: 'Back to your account' }).click();
    await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  });

  test('explains a link the provider refused and returns to the account page', async ({ page }) => {
    await page.goto('/account/sso-result?error=SSO_BINDING_CONFLICT');
    await expect(page.getByRole('alert')).toContainText('already linked');
    await page.getByRole('link', { name: 'Back to your account' }).click();
    await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  });

  test('unlinks a provider, retrying with the same key', async ({ page }) => {
    control.ssoBindings = [giteaBinding];
    control.ssoConnectors = [giteaConnector];
    control.nextUnlink = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/account');
    await expect(page.getByText('Provider account 4242')).toBeVisible();
    await page.getByRole('button', { name: 'Unlink Company Gitea' }).click();
    const dialog = page.getByRole('dialog', { name: 'Unlink Company Gitea' });
    await dialog.getByLabel('Current password').fill(account.password);
    await expect(dialog.getByLabel('Reason')).toHaveValue('Unlinked from the account page');

    await dialog.getByRole('button', { name: 'Unlink' }).click();
    await expect(dialog.getByRole('alert')).toBeVisible();
    await dialog.getByRole('button', { name: 'Unlink' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: 'Company Gitea unlinked' }),
    ).toBeVisible();
    await expect(dialog).toHaveCount(0);
    expect(control.unlinkKeys).toHaveLength(2);
    expect(control.unlinkKeys[1]).toBe(control.unlinkKeys[0]);

    // The connector is offered again.
    await expect(page.getByRole('button', { name: 'Link Company Gitea' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Unlink Company Gitea' })).toHaveCount(0);
  });

  test('requires a reason and closes without changing anything', async ({ page }) => {
    control.ssoBindings = [giteaBinding];
    await page.goto('/account');
    const open = page.getByRole('button', { name: 'Unlink Company Gitea' });
    await open.click();
    const dialog = page.getByRole('dialog', { name: 'Unlink Company Gitea' });
    await dialog.getByLabel('Current password').fill(account.password);
    await dialog.getByLabel('Reason').fill('  ');
    await dialog.getByRole('button', { name: 'Unlink' }).click();
    await expect(dialog.getByText('Enter a reason.')).toBeVisible();
    expect(control.unlinkKeys).toHaveLength(0);

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(open).toBeFocused();
  });

  test('fits a phone without scrolling sideways', async ({ page }) => {
    control.ssoBindings = [giteaBinding];
    control.ssoConnectors = [giteaConnector];
    await page.setViewportSize({ width: 360, height: 780 });
    await page.goto('/account');
    await expect(page.getByRole('button', { name: 'Unlink Company Gitea' })).toBeVisible();
    const overflow = await page
      .locator('main')
      .evaluate((main) => main.scrollWidth - main.clientWidth);
    expect(overflow).toBe(0);
  });

  test('sends the person to sign in when the session has ended', async ({ page }) => {
    control.nextPasswordChange = { status: 401, code: 'AUTH_SESSION_EXPIRED' };
    await page.goto('/account');
    await fillPasswordForm(page, account.password, nextPassword);
    await expect(page).toHaveURL(/\/sign-in/);
  });

  test('is complete in Chinese', async ({ page }) => {
    await page.goto('/account');
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: chinese['locale.zh-CN'] }).click();
    await expect(
      page.getByRole('heading', { name: chinese['account.title'], level: 1 }),
    ).toBeVisible();
    await expect(page.getByLabel(chinese['account.password.current'])).toBeVisible();
  });
}
