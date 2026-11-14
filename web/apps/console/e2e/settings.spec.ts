// Settings: an administrator sets the language of people who have not chosen one.
import { languageFlows, mockControlPlane, type ControlPlane } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

languageFlows('admin');

test.describe('settings page', () => {
  let control: ControlPlane;

  test.beforeEach(async ({ page }) => {
    control = await mockControlPlane(page, 'admin');
    control.signedIn = true;
  });

  test('reaches the page from the navigation', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'System', exact: true }).click();
    await page.getByRole('link', { name: 'Settings' }).click();
    await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeFocused();
  });

  test('saves the default and applies it to someone who has not chosen', async ({ page }) => {
    await page.goto('/settings');
    const save = page.getByRole('button', { name: 'Save' });
    await expect(save).toBeDisabled();
    const field = page.getByLabel('Default language');
    await expect(field).toHaveValue('auto');
    await field.selectOption('zh-CN');
    await save.click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
    expect(control.tenantSettings).toEqual({ default_locale: 'zh-CN', revision: 2 });
  });

  test('undoes the change', async ({ page }) => {
    await page.goto('/settings');
    await page.getByLabel('Default language').selectOption('en');
    await page.getByRole('button', { name: 'Save' }).click();
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect
      .poll(() => control.tenantSettings)
      .toEqual({ default_locale: 'auto', revision: 3 });
    await expect(page.getByLabel('Default language')).toHaveValue('auto');
  });

  test('recovers from a save that lost to another administrator', async ({ page }) => {
    await page.goto('/settings');
    await expect(page.getByLabel('Default language')).toBeVisible();
    control.tenantSettings = { default_locale: 'en', revision: 2 };
    await page.getByLabel('Default language').selectOption('zh-CN');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('alert')).toContainText('This was changed by someone else');
    // The refusal loaded what the other administrator saved.
    await expect(page.getByLabel('Default language')).toHaveValue('en');
    await page.getByLabel('Default language').selectOption('zh-CN');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect.poll(() => control.tenantSettings.default_locale).toBe('zh-CN');
  });

  test('recovers when the settings could not load', async ({ page }) => {
    control.tenantSettingsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/settings');
    const retry = page.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible();
    control.tenantSettingsProblem = null;
    await retry.click();
    await expect(page.getByLabel('Default language')).toBeVisible();
  });
});
