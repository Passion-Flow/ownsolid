// Which language a page shows: the person's choice, then the tenant's default, then the
// browser's. The browsers in these flows prefer English unless a flow says otherwise.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import { account, mockControlPlane, type ControlPlane } from './control-plane.ts';

const signInTitle = (page: Page, title: string) =>
  page.getByRole('heading', { name: title, level: 1 });

async function signIn(page: Page, submit = 'Sign in'): Promise<void> {
  await page.locator('input[name="email"], input[type="email"]').first().fill(account.email);
  await page.locator('input[type="password"]').first().fill(account.password);
  await page.getByRole('button', { name: submit, exact: true }).click();
}

/** Registers the language flows in the current spec file. */
export function languageFlows(audience: ControlPlane['audience']): void {
  let control: ControlPlane;

  test.beforeEach(async ({ page }) => {
    control = await mockControlPlane(page, audience);
  });

  test.describe('with a browser that prefers Chinese', () => {
    test.use({ locale: 'zh-CN' });

    test('follows the browser when nothing else decides', async ({ page }) => {
      await page.goto('/sign-in');
      await expect(signInTitle(page, chinese['signIn.title'])).toBeVisible();
    });

    test('lets the tenant default overrule the browser', async ({ page }) => {
      control.tenantSettings.default_locale = 'en';
      await page.goto('/sign-in');
      await expect(signInTitle(page, 'Sign in')).toBeVisible();
    });
  });

  test('uses the tenant default before anyone signs in', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto('/sign-in');
    await expect(signInTitle(page, chinese['signIn.title'])).toBeVisible();
  });

  test('lets a choice in this browser overrule the tenant default', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto('/sign-in');
    await page.getByLabel(chinese['locale.label']).selectOption('en');
    await expect(signInTitle(page, 'Sign in')).toBeVisible();
    await page.reload();
    await expect(signInTitle(page, 'Sign in')).toBeVisible();
  });

  test('lets the account overrule everything, in a browser that remembers nothing', async ({
    page,
  }) => {
    control.tenantSettings.default_locale = 'en';
    control.preferences.locale = 'zh-CN';
    control.signedIn = true;
    await page.goto('/account');
    await expect(signInTitle(page, chinese['account.title'])).toBeVisible();
    expect(control.preferenceWrites).toEqual([]);
  });

  test('saves a choice from the account menu to the account', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/account');
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: chinese['locale.zh-CN'] }).click();
    await expect(signInTitle(page, chinese['account.title'])).toBeVisible();
    await expect.poll(() => control.preferences.locale).toBe('zh-CN');
    expect(control.preferenceWrites).toEqual([{ locale: 'zh-CN', expected_revision: 1 }]);
  });

  test('offers automatic on the account page and goes back to the default', async ({ page }) => {
    control.tenantSettings.default_locale = 'en';
    control.preferences.locale = 'zh-CN';
    control.signedIn = true;
    await page.goto('/account');
    await expect(signInTitle(page, chinese['account.title'])).toBeVisible();
    const field = page.getByLabel(chinese['account.language.label']);
    await expect(field).toHaveValue('zh-CN');
    await field.selectOption('auto');
    await expect(signInTitle(page, 'Account')).toBeVisible();
    await expect.poll(() => control.preferences.locale).toBe('auto');
    await page.reload();
    await expect(signInTitle(page, 'Account')).toBeVisible();
    await expect(page.getByLabel('Interface language')).toHaveValue('auto');
  });

  test('carries a choice made on the sign-in page to the account', async ({ page }) => {
    await page.goto('/sign-in');
    await page.getByLabel('Language').selectOption('zh-CN');
    await expect(signInTitle(page, chinese['signIn.title'])).toBeVisible();
    await signIn(page, chinese['signIn.submit']);
    await expect.poll(() => control.preferences.locale).toBe('zh-CN');
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/\S/);
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  });

  test('does not save anything for someone who has not chosen', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/account');
    await expect(signInTitle(page, 'Account')).toBeVisible();
    await expect(page.getByLabel('Interface language')).toHaveValue('auto');
    await page.waitForTimeout(300);
    expect(control.preferenceWrites).toEqual([]);
  });

  test('keeps the choice and offers a retry when saving fails', async ({ page }) => {
    control.signedIn = true;
    control.preferencesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/account');
    await page.getByLabel('Interface language').selectOption('zh-CN');
    await expect(signInTitle(page, chinese['account.title'])).toBeVisible();
    const retry = page.getByRole('button', { name: chinese['action.retry'] });
    await expect(retry).toBeVisible();
    control.preferencesProblem = null;
    await retry.click();
    await expect.poll(() => control.preferences.locale).toBe('zh-CN');
  });
}
