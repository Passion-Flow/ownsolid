// Runs the audit trail on a real installation: signing in writes a real event, which the page shows
// with the person's address, finds by family and by request, and opens from its own link. Skipped
// unless the environment describes an installation; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

test.skip(origin === '', 'needs an installation');
test.use(liveOptions);

test('shows real events and follows them', async ({ page }) => {
  test.setTimeout(120_000);
  const refused: string[] = [];
  const elsewhere: string[] = [];
  page.on('response', (response) => {
    const { pathname } = new URL(response.url());
    if (pathname.startsWith('/api/') && pathname !== '/api/v1/me' && response.status() >= 400) {
      refused.push(`${String(response.status())} ${pathname}`);
    }
  });
  page.on('request', (request) => {
    if (new URL(request.url()).origin !== origin) elsewhere.push(request.url());
  });

  await page.goto('/audit');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Audit trail', level: 1 })).toBeVisible();
  await expectAccessible(page);

  await page.getByLabel('Kind of action').selectOption('auth');
  await expect(page).toHaveURL(/category=auth/);
  const signIn = page.getByRole('row').filter({ hasText: 'Signed in' }).first();
  await expect(signIn).toContainText(email);
  await expect(signIn).toContainText('Succeeded');
  await signIn.getByRole('button', { name: /^Show details of / }).click();

  const drawer = page.getByRole('dialog', { name: 'Signed in' });
  await expect(drawer).toContainText('Console');
  await expectAccessible(page);
  const link = (await drawer.getByText(/\/audit\?range=custom/).textContent()) ?? '';
  await drawer.getByRole('button', { name: 'Everything from this request' }).click();
  await expect(page).toHaveURL(/request=/);
  await expect(page.getByRole('row').filter({ hasText: 'Signed in' })).toHaveCount(1);

  await page.goto(link);
  await expect(page.getByRole('dialog', { name: 'Signed in' })).toBeVisible();
  await page.keyboard.press('Escape');

  await page.goto(`/audit?category=auth&actor=${'0'.repeat(8)}-0000-4000-8000-${'0'.repeat(12)}`);
  await expect(page.getByText('No events match')).toBeVisible();

  expect(refused).toEqual([]);
  expect(elsewhere).toEqual([]);
});
