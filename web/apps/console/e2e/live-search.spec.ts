// Runs global search on a real installation: its permissions, its cell routing and its database, which
// the mocked flows cannot show. It starts a task on a real agent so there is one to find. Skipped unless
// the environment names a joined node with a running agent; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

const nodeName = process.env.OWNSOLID_LIVE_SEARCH_NODE ?? '';

test.skip(origin === '' || nodeName === '', 'needs a joined node');
test.use(liveOptions);

const row = (page: Page, name: string | RegExp) => page.getByRole('option', { name });

async function menu(page: Page, text: string) {
  await page.keyboard.press('ControlOrMeta+k');
  await page.getByRole('combobox').fill(text);
}

test('finds real nodes, people, tasks and workspaces, and opens each', async ({ page }) => {
  test.setTimeout(150_000);
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
  await page.goto('/');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();

  // Text from the middle of the name, in the other case.
  const part = nodeName.slice(2, -1).toUpperCase();
  await menu(page, part);
  await expect(row(page, new RegExp(`^${nodeName}.*Node$`))).toBeVisible();
  await expectAccessible(page);
  await page.getByRole('combobox').press('Enter');
  await expect(page).toHaveURL(/\/nodes\?open=/);
  await expect(page.getByRole('dialog', { name: nodeName })).toBeVisible();
  await page.keyboard.press('Escape');

  await page.goto(`/nodes?name=${encodeURIComponent(part)}`);
  await expect(page.getByRole('row').filter({ hasText: nodeName })).toBeVisible();

  // A task to find, from the same node.
  await page.goto('/tasks');
  await page.getByRole('button', { name: 'Collect node facts' }).click();
  const dialog = page.getByRole('dialog', { name: 'Collect node facts' });
  await dialog.getByRole('combobox', { name: 'Node' }).selectOption({ label: nodeName });
  await dialog.getByRole('button', { name: 'Start' }).click();
  await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]{36}$/);
  const taskId = new URL(page.url()).pathname.split('/').at(-1) ?? '';
  await expect(page.getByText('Succeeded').first()).toBeVisible({ timeout: 90_000 });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await menu(page, part);
  const taskRow = row(page, /Collect node facts.*Task$/).first();
  await expect(taskRow).toContainText(nodeName);
  await taskRow.click();
  await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]{36}$/);

  await menu(page, taskId);
  await expect(row(page, /Task$/)).toHaveCount(1);
  await page.keyboard.press('Escape');

  await page.goto(`/tasks?node=${encodeURIComponent(part)}`);
  await expect(page.getByRole('row').filter({ hasText: nodeName }).first()).toBeVisible();

  // The address of the administrator, from its middle.
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await menu(page, email.slice(1, 5).toUpperCase());
  await row(page, new RegExp(`${email}.*Person$`)).click();
  await expect(page).toHaveURL(/\/users\?open=/);
  await expect(page.getByRole('dialog')).toContainText(email);
  await page.keyboard.press('Escape');

  // The first workspace, by its name in the other case.
  await page.goto('/users/workspaces');
  const first = page.getByRole('row').nth(1).getByRole('link');
  const workspace = (await first.textContent()) ?? '';
  await menu(page, workspace.toUpperCase());
  await row(page, new RegExp(`^${workspace}.*Workspace$`)).click();
  await expect(page).toHaveURL(/\/users\/workspaces\/[0-9a-f-]{36}$/);

  // A wildcard is plain text, and nothing failed along the way.
  await menu(page, '%');
  await expect(page.getByRole('listbox').getByText('No results')).toBeVisible();
  await expect(page.getByText('could not be searched')).toHaveCount(0);
  await page.keyboard.press('Escape');

  expect(problems).toEqual([]);
});
