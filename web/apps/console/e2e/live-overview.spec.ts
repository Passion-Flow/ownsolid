// Runs the overview on a real installation: every source answers, the figures match what the API
// reports, a real offline node leads to its drawer, and the quick actions open their dialogs. Needs
// an active node whose agent has stopped; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

const hasOfflineNode = process.env.OWNSOLID_LIVE_OFFLINE_NODE === 'true';

test.skip(origin === '' || !hasOfflineNode, 'needs an active node whose agent has stopped');
test.use(liveOptions);

interface Listed {
  data: { items: { id: string; name: string }[] };
}

test('shows real figures and leads to the fix', async ({ page }) => {
  test.setTimeout(120_000);
  const refused: string[] = [];
  const elsewhere: string[] = [];
  const blocked: string[] = [];
  page.on('response', (response) => {
    const { pathname } = new URL(response.url());
    // Asking who is signed in before anyone has is how the page finds the sign-in form.
    if (pathname.startsWith('/api/') && pathname !== '/api/v1/me' && response.status() >= 400) {
      refused.push(`${String(response.status())} ${pathname}`);
    }
  });
  page.on('request', (request) => {
    if (new URL(request.url()).origin !== origin) elsewhere.push(request.url());
  });
  page.on('console', (message) => {
    if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
      blocked.push(message.text());
    }
  });

  const offlineRead = page.waitForResponse(
    (response) =>
      response.url().includes('connectivity=offline') && response.url().includes('status=active'),
  );
  const countsRead = page.waitForResponse((response) => response.url().endsWith('/nodes/summary'));
  await page.goto('/');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, /, level: 1 })).toBeFocused();

  const offline = ((await (await offlineRead).json()) as Listed).data.items;
  const counts = ((await (await countsRead).json()) as { data: { online: number; total: number } })
    .data;
  const [first] = offline;
  if (!first) throw new Error('the installation has no active offline node');

  const attention = page
    .locator('section')
    .filter({ has: page.getByRole('heading', { name: 'Needs attention' }) });
  await expect(attention.getByText(`${first.name} last reported`)).toBeVisible();
  await expect(page.getByText(/items? needs? your attention/)).toBeVisible();
  await expect(
    page.getByRole('link', {
      name: new RegExp(`^Nodes online\\s*${String(counts.online)}\\s*of ${String(counts.total)}`),
    }),
  ).toBeVisible();
  await expect(
    page
      .locator('section')
      .filter({ has: page.getByRole('heading', { name: 'Fleet health' }) })
      .getByRole('link'),
  ).toHaveCount(4);
  await expectAccessible(page);

  await attention.getByRole('link', { name: 'Open the node' }).first().click();
  await expect(page).toHaveURL(new RegExp(`/nodes\\?open=${first.id}$`));
  await expect(page.getByRole('dialog', { name: first.name })).toBeVisible();
  await page.keyboard.press('Escape');

  await page.getByRole('navigation').getByRole('link', { name: 'Overview' }).click();
  await page
    .getByRole('link', { name: /^Add a node/ })
    .first()
    .click();
  await expect(page.getByRole('dialog', { name: 'Add a node' })).toBeVisible();
  await expect(page).toHaveURL(/\/nodes$/);
  await page.keyboard.press('Escape');

  await page.getByRole('navigation').getByRole('link', { name: 'Overview' }).click();
  await page.getByRole('link', { name: /^Invite someone/ }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');

  await page.getByRole('navigation').getByRole('link', { name: 'Overview' }).click();
  await expect(attention.getByText(`${first.name} last reported`)).toBeVisible();

  expect(refused).toEqual([]);
  expect(elsewhere).toEqual([]);
  expect(blocked).toEqual([]);
});
