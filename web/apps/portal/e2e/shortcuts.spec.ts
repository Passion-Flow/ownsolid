// The portal has no one-character actions of its own, but `?` lists what it has and the switch works.
import { mockControlPlane } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

test('lists the shortcuts it has, and offers the switch', async ({ page }) => {
  const control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
  await page.goto('/account');
  await page.getByRole('heading', { name: 'Account', level: 1 }).waitFor();
  await page.keyboard.press('?');
  const list = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
  await expect(list.getByText('Open the command menu')).toBeVisible();
  await expect(list.getByText('Add a node')).toHaveCount(0);
  await list.getByRole('switch', { name: 'Single-key shortcuts' }).click();
  await expect.poll(() => control.preferences.single_key_shortcuts).toBe(false);
});

test('the portal has a small navigation with nothing to open or filter', async ({ page }) => {
  const control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
  await page.goto('/');
  const navigation = page.getByRole('complementary').getByRole('navigation');
  await expect(navigation.getByRole('link', { name: 'Home' })).toBeVisible();
  await expect(navigation.getByRole('searchbox')).toHaveCount(0);
  await expect(navigation.getByRole('button')).toHaveCount(0);
});
