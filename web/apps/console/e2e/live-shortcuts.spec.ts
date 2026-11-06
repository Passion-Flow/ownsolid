// Runs the one-character shortcuts on a real installation: `N` opens Add a node, `?` lists them, and
// turning them off reaches a second browser through the account. Leaves them on. Skipped unless the
// environment describes an installation; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

test.skip(origin === '', 'needs an installation');
test.use(liveOptions);

async function signIn(page: Page): Promise<void> {
  await page.goto('/account');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
}

test('shortcuts work, and turning them off follows the account', async ({ page, browser }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const toggle = page.getByRole('switch', { name: 'Single-key shortcuts' });
  await expect(toggle).toHaveAttribute('aria-checked', 'true');

  await page.keyboard.press('?');
  const list = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
  await expect(list.getByText('Add a node')).toBeVisible();
  await expectAccessible(page);
  await page.keyboard.press('Escape');
  await page.keyboard.press('n');
  await expect(page.getByRole('dialog', { name: 'Add a node' })).toBeVisible();
  await page.keyboard.press('Escape');

  // J moves to the first row of a real list, and Enter opens it.
  await page.goto('/users');
  await expect(page.getByRole('row').nth(1)).toBeVisible();
  await page.keyboard.press('j');
  await expect(page.locator('main table tbody tr').first().locator(':focus')).toHaveCount(1);
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/open=/);
  await page.keyboard.press('Escape');

  await page.goto('/account');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');

  const other = await browser.newContext(liveOptions);
  const elsewhere = await other.newPage();
  await signIn(elsewhere);
  const otherToggle = elsewhere.getByRole('switch', { name: 'Single-key shortcuts' });
  await expect(otherToggle).toHaveAttribute('aria-checked', 'false');
  await elsewhere.keyboard.press('n');
  await expect(elsewhere).toHaveURL(/\/account$/);
  await otherToggle.click();
  await expect(otherToggle).toHaveAttribute('aria-checked', 'true');
  await other.close();

  await page.reload();
  await expect(toggle).toHaveAttribute('aria-checked', 'true');
});
