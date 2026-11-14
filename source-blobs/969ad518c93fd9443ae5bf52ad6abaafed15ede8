// Runs the portal home on a real installation: a published application, a conversation and a failed
// run of the signed-in person's are read from the running server, and marking the run as read
// persists. Skipped unless the environment describes an installation and names the application;
// docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

/** The application published to the person's workspace, with one conversation and a failed run. */
const application = process.env.OWNSOLID_LIVE_HOME_APPLICATION ?? '';

test.skip(origin === '' || application === '', 'needs an installation and a published application');
test.use(liveOptions);

const section = (page: Page, heading: string) =>
  page.locator('section').filter({ has: page.getByRole('heading', { name: heading }) });

test('reads every section from the server and keeps a run marked as read', async ({ page }) => {
  test.setTimeout(120_000);
  const refused: string[] = [];
  const elsewhere: string[] = [];
  const workspaces = new Set<string>();
  page.on('response', (response) => {
    const { pathname } = new URL(response.url());
    if (pathname.startsWith('/api/') && pathname !== '/api/v1/me' && response.status() >= 400) {
      refused.push(`${String(response.status())} ${pathname}`);
    }
  });
  page.on('request', (sent) => {
    const url = new URL(sent.url());
    if (url.origin !== origin) elsewhere.push(sent.url());
    const workspace = url.searchParams.get('workspace_id');
    if (workspace) workspaces.add(workspace);
  });

  await page.goto('/');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, /, level: 1 })).toBeVisible();

  const needs = section(page, 'Needs you');
  await expect(needs.getByText(application)).toBeVisible();
  await expect(needs.getByText('Workflow run failed')).toBeVisible();
  const conversations = section(page, 'Pick up where you left off');
  await expect(conversations.getByText(application)).toBeVisible();
  await expect(conversations.getByText('2 turns', { exact: false })).toBeVisible();
  const published = section(page, 'Published to you');
  await expect(published.getByText(application)).toBeVisible();
  await expect(published.getByText('Version 1', { exact: false })).toBeVisible();
  await expect(section(page, 'Usage').getByText('POR-111')).toBeVisible();
  await expectAccessible(page);

  await needs.getByRole('button', { name: `Mark as read: ${application}` }).click();
  await expect(needs.getByText(application)).toBeHidden();
  await page.reload();
  await expect(published.getByText(application)).toBeVisible();
  await expect(needs.getByText(application)).toBeHidden();

  // Both lists asked for the session's one workspace, and nothing left the installation.
  expect(workspaces.size).toBe(1);
  expect(refused).toEqual([]);
  expect(elsewhere).toEqual([]);
});
