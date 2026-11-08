// Gateway and keys: issuing a key and seeing its secret once, rotating it with an overlap, revoking
// it by typing its name, each key's calls, and what happens when any of that is refused
// (docs/research/gateway-keys.md).
import {
  adminUser,
  deploymentAlias,
  deploymentKey,
  expiringKey,
  keyCall,
  memberUser,
  mockControlPlane,
  revokedKey,
  unavailableAlias,
  unusedKey,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [{ ...adminUser }, { ...memberUser }];
  control.aliases = [deploymentAlias, unavailableAlias];
});

async function issue(page: Page, name: string) {
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await dialog.getByLabel('Name').fill(name);
  await dialog.getByLabel('Reason').fill('Access for the support bot');
  await dialog.getByRole('button', { name: 'Issue', exact: true }).click();
}

test('is reached from the navigation, and says what a key is for when empty', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Models', exact: true }).click();
  await page.getByRole('navigation').getByRole('link', { name: 'Gateway and keys' }).click();
  await expect(page.getByRole('heading', { name: 'Gateway and keys', level: 1 })).toBeFocused();
  await expect(page.getByRole('heading', { name: 'No keys yet' })).toBeVisible();
  await expect(page.getByText('call one model through the gateway')).toBeVisible();
  await page.getByRole('main').getByRole('button', { name: 'Issue a key' }).first().click();
  await expect(page.getByRole('dialog', { name: 'Issue a key' })).toBeVisible();
});

test('opens the issue dialog from the k shortcut', async ({ page }) => {
  await page.goto('/tasks');
  await expect(page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeVisible();
  await page.keyboard.press('k');
  await expect(page).toHaveURL(/\/gateway$/);
  await expect(page.getByRole('dialog', { name: 'Issue a key' })).toBeVisible();
});

test('issues a key for an available model and shows its secret once', async ({ page }) => {
  await page.goto('/gateway#issue');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  const model = dialog.getByLabel('Model');
  await expect(model.getByRole('option')).toHaveText([deploymentAlias.name]);
  await expect(dialog.getByLabel('Expires after')).toHaveValue('86400');
  await expect(dialog.getByLabel('Expires after').getByRole('option')).toHaveText([
    '1 day (default)',
    '7 days',
    '30 days',
    '90 days',
  ]);
  await expect(dialog.getByLabel('Rate and token limit')).toBeDisabled();
  await expect(dialog.getByText('a key cannot have limits of its own yet')).toBeVisible();
  await dialog.getByLabel('Expires after').selectOption('2592000');
  await issue(page, 'Support bot');
  const done = page.getByRole('dialog', { name: 'Support bot is issued' });
  await expect(done.getByText('It is shown only this once.')).toBeVisible();
  const write = control.keyWrites[0];
  expect(write?.body).toEqual({
    alias_id: deploymentAlias.id,
    label: 'Support bot',
    ttl_seconds: 2_592_000,
    reason: 'Access for the support bot',
  });
  const secret = `pk1.6f1c7a1e-0000-4000-8002-${write?.key.replaceAll('-', '').slice(0, 12)}`;
  await expect(done.getByText(new RegExp(`^${secret}\\.`))).toBeVisible();
  await done.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('row', { name: /Support bot/ })).toContainText(deploymentAlias.name);
  await expect(page.getByText(new RegExp(`^${secret}\\.`))).toHaveCount(0);
});

test('says there is nothing to call when no model is on the gateway', async ({ page }) => {
  control.aliases = [unavailableAlias];
  await page.goto('/gateway#issue');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(dialog.getByText('No model is on the gateway in this workspace')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Issue', exact: true })).toBeDisabled();
  await dialog.getByRole('link', { name: 'Open Model services' }).click();
  await expect(page.getByRole('heading', { name: 'Model services', level: 1 })).toBeVisible();
});

test('a lost answer is retried with the same key, and says the secret cannot be shown again', async ({
  page,
}) => {
  control.keyWriteFailures = ['lost'];
  await page.goto('/gateway#issue');
  await issue(page, 'Support bot');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(
    page.getByText('The key was made, but its secret cannot be shown again'),
  ).toBeVisible();
  expect(control.keyWrites).toHaveLength(2);
  expect(control.keyWrites[1]?.key).toBe(control.keyWrites[0]?.key);
  expect(control.gatewayKeys).toHaveLength(1);
});

test('a refused issue says why and keeps the form', async ({ page }) => {
  control.keyWriteFailures = [{ status: 409, code: 'RESOURCE_LIMIT_REACHED' }];
  await page.goto('/gateway#issue');
  await issue(page, 'Support bot');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(dialog.getByText('The limit is reached')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveValue('Support bot');
});

test('tiles count and filter from the address, and the over-limit tile says it is not ready', async ({
  page,
}) => {
  control.gatewayKeys = [deploymentKey, expiringKey, unusedKey, revokedKey];
  await page.goto('/gateway');
  const tiles = page.getByRole('group', { name: 'Filter keys' });
  await expect(tiles.getByRole('button', { name: /All keys/ })).toContainText('4');
  await expect(tiles.getByRole('button', { name: /Expiring within a week/ })).toContainText('1');
  await expect(tiles.getByRole('button', { name: /Never used/ })).toContainText('1');
  await expect(tiles.getByRole('button', { name: /Over limit/ })).toBeDisabled();
  await expect(tiles.getByRole('button', { name: /Over limit/ })).toContainText('Not ready');
  await tiles.getByRole('button', { name: /Expiring within a week/ }).click();
  await expect(page).toHaveURL(/tile=expiring/);
  await expect(page.getByRole('row')).toHaveCount(2);
  await expect(page.getByRole('row', { name: /Nightly report/ })).toContainText(
    memberUser.display_name,
  );
  await page.reload();
  await expect(page.getByRole('row')).toHaveCount(2);
  await page.getByRole('searchbox', { name: 'Search keys' }).fill('nothing like this');
  await expect(page.getByRole('heading', { name: 'No key matches' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('row')).toHaveCount(5);
  await expect(page.getByRole('row', { name: /Old pilot/ })).toContainText('Revoked');
});

test('a failed read says what happened and retries', async ({ page }) => {
  control.keysProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/gateway');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.keysProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'No keys yet' })).toBeVisible();
});

test("a key's drawer shows only that key's calls, a page at a time", async ({ page }) => {
  control.gatewayKeys = [deploymentKey, unusedKey];
  control.keyCalls = [
    ...Array.from({ length: 25 }, (_, index) => keyCall(index)),
    keyCall(30, unusedKey),
  ];
  await page.goto('/gateway');
  await page.getByRole('button', { name: `Show ${deploymentKey.label}` }).click();
  const drawer = page.getByRole('dialog', { name: deploymentKey.label });
  await expect(drawer.getByText(`Calls ${deploymentAlias.name} only`)).toBeVisible();
  await expect(drawer.getByText(adminUser.display_name)).toBeVisible();
  const calls = drawer.getByRole('table', { name: 'Calls with this key' });
  await expect(calls.getByRole('row')).toHaveCount(21);
  await expect(calls.getByRole('row', { name: /Model timed out/ }).first()).toBeVisible();
  await drawer.getByRole('button', { name: 'Load more' }).click();
  await expect(calls.getByRole('row')).toHaveCount(26);
  await expect(drawer.getByRole('button', { name: 'Load more' })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: `Show ${unusedKey.label}` }).click();
  const other = page.getByRole('dialog', { name: unusedKey.label });
  await expect(
    other.getByRole('table', { name: 'Calls with this key' }).getByRole('row'),
  ).toHaveCount(2);
});

test('a failed call log retries on its own, and a key with no calls says so', async ({ page }) => {
  control.gatewayKeys = [unusedKey];
  control.callsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto(`/gateway?open=${unusedKey.id}`);
  const drawer = page.getByRole('dialog', { name: unusedKey.label });
  await expect(drawer.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.callsProblem = null;
  await drawer.getByRole('button', { name: 'Try again' }).click();
  await expect(drawer.getByText('No call has been made with this key.')).toBeVisible();
});

test('rotates a key with an overlap: a new secret now, the old one until then', async ({
  page,
}) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto(`/gateway?open=${deploymentKey.id}`);
  await page
    .getByRole('dialog', { name: deploymentKey.label })
    .getByRole('button', { name: 'Rotate' })
    .click();
  const dialog = page.getByRole('dialog', { name: `Rotate ${deploymentKey.label}` });
  await expect(dialog.getByLabel('The old secret keeps working')).toHaveValue('86400');
  await expect(dialog.getByText(/Both secrets work until/)).toBeVisible();
  await dialog.getByLabel('Reason').fill('Quarterly rotation');
  await dialog.getByRole('button', { name: 'Rotate', exact: true }).click();
  const done = page.getByRole('dialog', { name: `${deploymentKey.label} has a new secret` });
  await expect(done.getByText('It is shown only this once.')).toBeVisible();
  expect(control.keyWrites[0]?.body).toEqual({
    overlap_seconds: 86_400,
    reason: 'Quarterly rotation',
  });
  await done.getByRole('button', { name: 'Done' }).click();
  const rows = page.getByRole('row', { name: new RegExp(deploymentKey.label) });
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: 'Being replaced' })).toHaveCount(1);
  const drawer = page.getByRole('dialog', { name: deploymentKey.label });
  await expect(drawer.getByText('Replaced by')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Rotate' })).toHaveCount(0);
});

test('rotating now stops the old key at once', async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto(`/gateway?open=${deploymentKey.id}`);
  await page
    .getByRole('dialog', { name: deploymentKey.label })
    .getByRole('button', { name: 'Rotate' })
    .click();
  const dialog = page.getByRole('dialog', { name: `Rotate ${deploymentKey.label}` });
  await dialog.getByLabel('The old secret keeps working').selectOption('0');
  await expect(dialog.getByText('Use this when the secret leaked.')).toBeVisible();
  await dialog.getByLabel('Reason').fill('The secret leaked');
  await dialog.getByRole('button', { name: 'Rotate', exact: true }).click();
  await page.getByRole('button', { name: 'Done' }).click();
  await expect(
    page.getByRole('row', { name: new RegExp(deploymentKey.label) }).filter({ hasText: 'Revoked' }),
  ).toHaveCount(1);
});

test('a key rotated by someone else meanwhile says so', async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  control.keyWriteFailures = [{ status: 409, code: 'API_KEY_ALREADY_ROTATED' }];
  await page.goto(`/gateway?open=${deploymentKey.id}`);
  await page
    .getByRole('dialog', { name: deploymentKey.label })
    .getByRole('button', { name: 'Rotate' })
    .click();
  const dialog = page.getByRole('dialog', { name: `Rotate ${deploymentKey.label}` });
  await dialog.getByLabel('Reason').fill('Quarterly rotation');
  await dialog.getByRole('button', { name: 'Rotate', exact: true }).click();
  await expect(dialog.getByText('This key was already rotated')).toBeVisible();
});

test('revokes a key once its name is typed, naming what stops', async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto(`/gateway?open=${deploymentKey.id}`);
  await page
    .getByRole('dialog', { name: deploymentKey.label })
    .getByRole('button', { name: 'Revoke' })
    .click();
  const dialog = page.getByRole('dialog', { name: `Revoke ${deploymentKey.label}` });
  await expect(
    dialog.getByText(`Calls to ${deploymentAlias.name} with this key fail`),
  ).toBeVisible();
  await expect(dialog.getByText('It was last used')).toBeVisible();
  const confirm = dialog.getByRole('button', { name: 'Revoke', exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(/Type/).fill(deploymentKey.label);
  await dialog.getByLabel('Reason').fill('Pilot finished');
  await confirm.click();
  await expect(page.getByText(`${deploymentKey.label} is revoked`)).toBeVisible();
  expect(control.keyWrites[0]).toMatchObject({
    kind: 'revoke',
    body: { reason: 'Pilot finished' },
  });
  const drawer = page.getByRole('dialog', { name: deploymentKey.label });
  await expect(drawer.getByText('Revoked')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Revoke' })).toHaveCount(0);
});
