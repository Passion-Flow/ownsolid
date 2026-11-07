// Runs the Workspaces pages on a real installation: creates a workspace, adds a real account, is
// refused for the last owner, changes a role, and disables the workspace with its reason in the audit
// trail. Each run leaves one disabled workspace behind, since the API has no deletion. Skipped unless
// the environment describes an installation; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

// An account other than the administrator, to add to the workspace.
const other = process.env.OWNSOLID_LIVE_OTHER_ACCOUNT ?? '';

test.skip(origin === '' || other === '', 'needs a second account on an installation');
test.use(liveOptions);

test('creates a workspace and changes who belongs', async ({ page }) => {
  test.setTimeout(120_000);
  const elsewhere: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).origin !== origin) elsewhere.push(request.url());
  });
  const name = `Live ${String(Date.now())}`;
  const reason = `Live run ${name}`;

  await page.goto('/users/workspaces');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Workspaces', level: 1 })).toBeVisible();
  await expectAccessible(page);

  await page.getByRole('button', { name: 'Create a workspace' }).click();
  const create = page.getByRole('dialog', { name: 'Create a workspace' });
  await create.getByLabel('Name').fill(name);
  await create.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();
  await expect(page.getByRole('row', { name: new RegExp(email) })).toContainText('Owner');
  await expectAccessible(page);

  // The only owner cannot leave.
  await page.getByRole('button', { name: `Remove ${email}` }).click();
  const refusal = page.getByRole('dialog', { name: `Remove ${email}` });
  await refusal.getByLabel('Reason').fill(reason);
  await refusal.getByRole('button', { name: 'Remove' }).click();
  await expect(refusal.getByText('A workspace needs an owner')).toBeVisible();
  await refusal.getByRole('button', { name: 'Cancel' }).click();

  await page.getByRole('button', { name: 'Add a person' }).click();
  const add = page.getByRole('dialog', { name: 'Add a person' });
  await add.getByLabel('Email').fill(`nobody-${String(Date.now())}@lane-b.test`);
  await add.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(add.getByText('No account has this address.')).toBeVisible();
  await add.getByLabel('Email').fill(other);
  await add.getByRole('button', { name: 'Add', exact: true }).click();
  const row = page.getByRole('row', { name: new RegExp(other) });
  await expect(row).toContainText('Member');

  await page.getByRole('button', { name: `Change role for ${other}` }).click();
  const change = page.getByRole('dialog');
  await change.getByLabel('Role').selectOption('viewer');
  await change.getByLabel('Reason').fill(reason);
  await change.getByRole('button', { name: 'Change the role' }).click();
  await expect(row).toContainText('Viewer');

  await page.getByRole('button', { name: `Remove ${other}` }).click();
  const remove = page.getByRole('dialog');
  await remove.getByLabel('Reason').fill(reason);
  await remove.getByRole('button', { name: 'Remove' }).click();
  await expect(row).toBeHidden();

  await page.getByRole('button', { name: 'Disable' }).click();
  const disable = page.getByRole('dialog');
  await disable.getByLabel('Reason').fill(reason);
  await disable.getByRole('button', { name: 'Disable the workspace' }).click();
  await expect(page.getByRole('button', { name: 'Enable' })).toBeVisible();

  await page.reload();
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();
  await expect(page.getByRole('main').getByText('Disabled', { exact: true })).toBeVisible();

  // The reason reached the audit trail; disabling is recorded as archiving.
  await page.goto('/audit?category=workspace');
  await page.getByRole('row').filter({ hasText: 'workspace.archived' }).first().waitFor();
  await page
    .getByRole('button', { name: /^Show details of workspace\.archived/ })
    .first()
    .click();
  await expect(page.getByRole('dialog').getByText(reason)).toBeVisible();
  expect(elsewhere).toEqual([]);
});
