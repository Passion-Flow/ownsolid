import { helpFlows, mockControlPlane } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

helpFlows('portal');

test.describe('portal help in the account menu', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  for (const [path, slug] of [
    ['/', 'getting-started'],
    ['/models', 'models'],
    ['/keys', 'api-keys'],
    ['/account', 'account'],
    ['/support', 'what-is-supported'],
  ] as const) {
    test(`opens the article for ${path} on a phone`, async ({ page }) => {
      const control = await mockControlPlane(page, 'portal');
      control.signedIn = true;
      await page.goto(path);
      await page.getByRole('button', { name: 'Account menu' }).click();
      await page.getByRole('menuitem', { name: 'Help for this page' }).click();
      await expect(page).toHaveURL(`/help/${slug}`);
      await expect(page.getByRole('main').getByRole('heading', { level: 1 })).toBeVisible();
    });
  }

  test('opens all help from a page without a matching article', async ({ page }) => {
    const control = await mockControlPlane(page, 'portal');
    control.signedIn = true;
    await page.goto('/help/account');
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: 'Help', exact: true }).click();
    await expect(page).toHaveURL('/help');
    await expect(page.getByRole('searchbox', { name: 'Search help' })).toBeVisible();
  });
});
