// Runs the sidebar on a real installation: its Content-Security-Policy with the rail's name drawn beside an
// icon, and the choice reaching a second browser through the account. Leaves the sidebar open. Skipped unless
// the environment describes an installation; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

test.skip(origin === '', 'needs an installation');
test.use(liveOptions);

const sidebar = (page: Page) => page.getByRole('complementary');
const nav = (page: Page) => sidebar(page).getByRole('navigation');
const link = (page: Page, name: string) => nav(page).getByRole('link', { name, exact: true });

async function signIn(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(sidebar(page)).toBeVisible();
}

test('groups, the filter and the rail work, and the rail follows the account', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  const problems: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
      problems.push(message.text());
    }
  });
  page.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      problems.push(`outside request: ${request.url()}`);
    }
  });
  await signIn(page, '/tasks');
  await expect(link(page, 'Tasks')).toHaveAttribute('aria-current', 'page');
  await expect(link(page, 'Audit trail')).toHaveCount(0);
  await expectAccessible(page);

  await nav(page).getByRole('button', { name: 'Governance', exact: true }).click();
  await link(page, 'Audit trail').click();
  await expect(page).toHaveURL(/\/audit/);
  await expect(nav(page).getByRole('button', { name: 'Operations', exact: true })).toHaveAttribute(
    'aria-expanded',
    'false',
  );

  await nav(page).getByRole('searchbox', { name: 'Filter pages' }).fill('LICENSE');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/license/);

  // A pin reaches the second browser too, and comes off at the end.
  await nav(page).getByRole('button', { name: 'Pin License' }).click();
  await expect(nav(page).getByText('Pinned', { exact: true })).toBeVisible();

  await sidebar(page).getByRole('button', { name: 'Collapse navigation' }).click();
  await link(page, 'Nodes').hover();
  await expect(sidebar(page).locator('.fixed')).toHaveText('Nodes');
  await expectAccessible(page);

  const other = await browser.newContext({
    ...liveOptions,
    baseURL: origin,
    ignoreHTTPSErrors: false,
  });
  const second = await other.newPage();
  try {
    await signIn(second, '/tasks');
    await expect(sidebar(second).getByRole('button', { name: 'Expand navigation' })).toBeVisible();
    await expect(sidebar(second).getByRole('list', { name: 'Pinned' })).toContainText('License');
  } finally {
    await other.close();
  }
  await sidebar(page).getByRole('button', { name: 'Expand navigation' }).click();
  await expect(sidebar(page).getByRole('button', { name: 'Collapse navigation' })).toBeVisible();
  await nav(page).getByRole('button', { name: 'Unpin License' }).first().click();
  await expect(nav(page).getByText('Pinned', { exact: true })).toHaveCount(0);
  await page.reload();
  await expect(nav(page).getByRole('searchbox', { name: 'Filter pages' })).toBeVisible();

  // A form keeps what works at its default behind one fold.
  await page.goto('/nodes/pacing');
  await expect(page.getByLabel('Heartbeat interval (seconds)')).toBeVisible();
  await expect(page.getByLabel('Jitter (percent)')).toBeHidden();
  await page.getByText('Advanced settings (3)').click();
  await expect(page.getByLabel('Jitter (percent)')).toBeVisible();
  await expectAccessible(page);
  expect(problems).toEqual([]);
});
