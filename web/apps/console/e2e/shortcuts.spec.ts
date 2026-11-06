// One-character shortcuts: they act at once, never while typing or behind a layer, say so when
// their page does not exist yet, and a person can turn them off, which follows the account.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { adminUser, mockControlPlane, type ControlPlane } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [adminUser];
});

const addNode = (page: Page) => page.getByRole('dialog', { name: 'Add a node' });
const list = (page: Page) => page.getByRole('dialog', { name: 'Keyboard shortcuts' });

async function overview(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
}

test('N adds a node from anywhere, and on the Nodes page itself', async ({ page }) => {
  await overview(page);
  await page.keyboard.press('n');
  await expect(addNode(page)).toBeVisible();
  await expect(page).toHaveURL(/\/nodes$/);
  await page.keyboard.press('Escape');
  await expect(addNode(page)).toHaveCount(0);
  await page.keyboard.press('Shift+N');
  await expect(addNode(page)).toBeVisible();
});

test('a key does nothing while typing, with a modifier, or behind a layer', async ({ page }) => {
  await page.goto('/users');
  const search = page.getByRole('main').getByRole('searchbox');
  await search.click();
  await page.keyboard.press('n');
  await expect(search).toHaveValue('n');
  await expect(page).toHaveURL(/\/users/);
  await search.fill('');
  await search.blur();
  await page.keyboard.press('Alt+n');
  await expect(page).toHaveURL(/\/users/);
  await page.getByRole('button', { name: 'Invite', exact: true }).click();
  await page.getByRole('dialog').first().waitFor();
  await page.keyboard.press('Tab');
  await page.keyboard.press('?');
  await expect(list(page)).toHaveCount(0);
});

test('/ goes to the search on the page, and only when it has one', async ({ page }) => {
  await page.goto('/users');
  await expect(page.getByRole('heading', { name: 'Users', level: 1 })).toBeVisible();
  await page.keyboard.press('/');
  const search = page.getByRole('main').getByRole('searchbox');
  await expect(search).toBeFocused();
  await expect(search).toHaveValue('');
  await overview(page);
  await page.keyboard.press('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
});

test('? lists them, and an action without a page says so', async ({ page }) => {
  await overview(page);
  await page.keyboard.press('?');
  await expect(list(page).getByText('Add a node')).toBeVisible();
  await expect(list(page).getByText('Deploy a model (not available yet)')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('d');
  await expect(
    page.getByRole('status').filter({ hasText: 'Deploy a model is not available yet.' }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
});

test('turning them off stops every one-character key and keeps the command menu', async ({
  page,
}) => {
  await overview(page);
  await page.keyboard.press('?');
  await list(page).getByRole('switch', { name: 'Single-key shortcuts' }).click();
  await expect.poll(() => control.preferences.single_key_shortcuts).toBe(false);
  await page.keyboard.press('Escape');
  await page.keyboard.press('n');
  await page.keyboard.press('?');
  await expect(list(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/$/);

  // The list stays reachable, so the switch does too.
  await page.keyboard.press('Control+k');
  await page.getByRole('option', { name: 'Keyboard shortcuts' }).click();
  await list(page).getByRole('switch', { name: 'Single-key shortcuts' }).click();
  await expect.poll(() => control.preferences.single_key_shortcuts).toBe(true);
  await page.keyboard.press('Escape');
  await page.keyboard.press('n');
  await expect(addNode(page)).toBeVisible();
});

test('the choice follows the account into another browser', async ({ page }) => {
  control.preferences.single_key_shortcuts = false;
  await page.goto('/account');
  const toggle = page.getByRole('switch', { name: 'Single-key shortcuts' });
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await page.keyboard.press('n');
  await expect(page).toHaveURL(/\/account$/);
  await toggle.click();
  await expect.poll(() => control.preferences.single_key_shortcuts).toBe(true);
  expect(control.preferenceWrites.at(-1)).toMatchObject({ single_key_shortcuts: true });
  expect(control.preferenceWrites.at(-1)).not.toHaveProperty('theme');
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto('/');
    await page.getByRole('heading', { level: 1 }).waitFor();
    await page.keyboard.press('?');
    await expect(page.getByRole('dialog', { name: chinese['shortcuts.title'] })).toBeVisible();
  });
});
