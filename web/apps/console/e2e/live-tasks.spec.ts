// Runs a long task from the console on a real agent of a real installation. The flow stops the
// agent first, so the task is known to wait, and starts it once the page has been left. Skipped
// unless the environment says how; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { execSync } from 'node:child_process';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

const stopAgent = process.env.OWNSOLID_LIVE_AGENT_STOP ?? '';
const startAgent = process.env.OWNSOLID_LIVE_AGENT_START ?? '';

test.skip(origin === '' || stopAgent === '' || startAgent === '', 'needs a real agent');
test.use(liveOptions);

test('watches a task on a real agent, leaves it and is told how it ended', async ({ page }) => {
  test.setTimeout(180_000);
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
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();

  execSync(stopAgent, { stdio: 'ignore' });
  try {
    await page.getByRole('link', { name: 'Tasks' }).click();
    await expect(page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeVisible();
    await page.getByRole('button', { name: 'Collect node facts' }).click();
    const dialog = page.getByRole('dialog', { name: 'Collect node facts' });
    await expect(dialog.getByRole('combobox', { name: 'Node' })).toBeVisible();
    await expectAccessible(page);
    await dialog.getByRole('button', { name: 'Start' }).click();
    await expect(page.getByText('Waiting for the node to pick this task up.')).toBeVisible();
    await expectAccessible(page);

    // The page is left while the task waits, and the top bar keeps track of it.
    await page.getByRole('link', { name: 'Settings' }).click();
    await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
    await expect(page.getByRole('button', { name: /of your tasks running/ })).toBeVisible();
  } finally {
    execSync(startAgent, { stdio: 'ignore' });
  }

  const told = page.getByRole('status').filter({ hasText: 'Collect node facts finished' });
  await expect(told).toBeVisible({ timeout: 90_000 });
  await expect(page.getByRole('button', { name: /of your tasks running/ })).toHaveCount(0);
  await told.getByRole('button', { name: 'Open' }).click();

  const steps = page.getByRole('list', { name: 'Steps of this task' }).getByRole('listitem');
  await expect(steps).toHaveCount(3);
  await expect(steps.first()).toContainText('Read the operating system');
  await expect(steps.nth(1)).toContainText('Read the memory');
  await expect(steps.nth(2)).toContainText('Read the disk');
  await expect(
    page.getByRole('list', { name: 'Steps of this task' }).locator('[aria-current]'),
  ).toHaveCount(0);
  const log = page.getByRole('log');
  await expect(log).toContainText('Operating system:');
  await expect(log).toContainText('Memory:');
  await expect(log).toContainText('Disk at /:');
  await expect(page.getByRole('button', { name: 'Pause' })).toHaveCount(0);
  await expectAccessible(page);

  // The same record comes back after a reload, read from the server.
  await page.reload();
  await expect(steps).toHaveCount(3);
  await expect(log).toContainText('Disk at /:');
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Download' }).click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/^task-.*\.log$/);

  const finished = page.url();
  await page.getByRole('button', { name: 'Run again' }).click();
  await expect(page).not.toHaveURL(finished);
  await expect(page.getByText('Succeeded')).toBeVisible({ timeout: 60_000 });
  await expect(steps).toHaveCount(3);
  await expect(log).toContainText('Disk at /:');

  // Both tasks are in the list, and each opens its own record.
  await page.getByRole('link', { name: 'All tasks' }).click();
  await page
    .getByRole('group', { name: 'Show tasks' })
    .getByRole('button', { name: 'Succeeded' })
    .click();
  await expect(page).toHaveURL(/state=succeeded/);
  await expect(page.getByRole('link', { name: 'Collect node facts' }).first()).toBeVisible();
  expect(await page.getByRole('link', { name: 'Collect node facts' }).count()).toBeGreaterThan(1);
  await expectAccessible(page);

  await page
    .getByRole('group', { name: 'Show tasks' })
    .getByRole('button', { name: 'Failed' })
    .click();
  await expect(page.getByText('No tasks match')).toBeVisible();
  expect(problems).toEqual([]);
});
