// The portal's one-character action is K for a new key; `?` lists it and the switch works.
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
  await expect(list.getByText('Create an API key')).toBeVisible();
  await expect(list.getByText('Add a node')).toHaveCount(0);
  await list.getByRole('switch', { name: 'Single-key shortcuts' }).click();
  await expect.poll(() => control.preferences.single_key_shortcuts).toBe(false);
});

test('the portal groups destinations and follows the current page', async ({ page }) => {
  const control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
  await page.goto('/');
  const navigation = page.getByRole('complementary').getByRole('navigation');
  await expect(navigation.getByRole('link', { name: 'Home' })).toBeVisible();
  const tools = navigation.getByRole('button', { name: 'AI tools' });
  const workspace = navigation.getByRole('button', { name: 'Workspace' });
  await expect(tools).toHaveAttribute('aria-expanded', 'false');
  await tools.click();
  const unavailable = navigation
    .locator('[aria-disabled=true]')
    .filter({ hasText: 'Conversations' });
  await expect(unavailable).toBeVisible();
  await expect(unavailable.getByText('Chat is not available yet.')).toBeVisible();
  await workspace.click();
  await navigation.getByRole('link', { name: 'My API keys' }).click();
  await expect(tools).toHaveAttribute('aria-expanded', 'false');
  await expect(workspace).toHaveAttribute('aria-expanded', 'true');

  await page.keyboard.press('ControlOrMeta+k');
  await page.getByPlaceholder(/^Type a command/).fill('Models');
  await page.getByRole('option', { name: 'Models Page', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();
  await expect(tools).toHaveAttribute('aria-expanded', 'true');
  await expect(workspace).toHaveAttribute('aria-expanded', 'false');
});
