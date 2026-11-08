// A person's own API keys (S-306): issuing one and seeing its secret once, how to call with it,
// rotating and revoking it, its calls, and what happens when any of that is refused
// (docs/research/portal-api-keys.md).
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  deploymentAlias,
  deploymentKey,
  expiringKey,
  keyCall,
  mockControlPlane,
  revokedKey,
  unavailableAlias,
  unusedKey,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

/** Every API path the page called, without the origin. */
let called: string[];

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
  control.aliases = [deploymentAlias, unavailableAlias];
  called = [];
  page.on('request', (request) => {
    const { pathname } = new URL(request.url());
    if (pathname.startsWith('/api/')) called.push(pathname);
  });
});

async function issue(page: Page, name: string) {
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await dialog.getByLabel('Name').fill(name);
  await dialog.getByLabel('Reason').fill('For my script');
  await dialog.getByRole('button', { name: 'Issue', exact: true }).click();
}

async function openDrawer(page: Page, name: string) {
  await page.getByRole('button', { name: `Show ${name}` }).click();
  const drawer = page.getByRole('dialog', { name });
  await drawer.getByRole('heading', { name: 'Calls with this key' }).waitFor();
  return drawer;
}

test('is reached from the navigation, and says what a key is for when empty', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('navigation').getByRole('link', { name: 'My API keys' }).click();
  await expect(page.getByRole('heading', { name: 'My API keys', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No keys yet' })).toBeVisible();
});

test('opens the issue dialog from the k shortcut, on the page as well', async ({ page }) => {
  await page.goto('/keys');
  await page.getByRole('heading', { name: 'My API keys', level: 1 }).waitFor();
  await page.keyboard.press('k');
  await expect(page.getByRole('dialog', { name: 'Issue a key' })).toBeVisible();
});

test("uses the portal's paths and never an administrator's", async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  control.keyCalls = [keyCall(0)];
  await page.goto('/keys');
  const drawer = await openDrawer(page, deploymentKey.label);
  await expect(drawer.getByRole('cell', { name: 'Succeeded' })).toBeVisible();
  const distribution = called.filter((path) => path.includes('distribution'));
  expect(distribution.length).toBeGreaterThan(0);
  expect(distribution.filter((path) => path.includes('/admin/'))).toEqual([]);
  expect(called.filter((path) => path.includes('/users/'))).toEqual([]);
});

test("shows no owner, because every key is the person's own", async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto('/keys');
  await expect(page.getByRole('row', { name: /Support bot/ })).toBeVisible();
  await expect(page.getByRole('columnheader', { name: 'Owner' })).toHaveCount(0);
  const drawer = await openDrawer(page, deploymentKey.label);
  await expect(drawer.getByText('Owner', { exact: true })).toHaveCount(0);
});

test('issues a key, shows its secret once, then how to call with it', async ({ page }) => {
  await page.goto('/keys#issue');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(dialog.getByLabel('Model').getByRole('option')).toHaveText([deploymentAlias.name]);
  await issue(page, 'My script');
  const done = page.getByRole('dialog', { name: 'My script is issued' });
  await expect(done.getByText('It is shown only this once.')).toBeVisible();
  const write = control.keyWrites[0];
  expect(write?.body).toEqual({
    alias_id: deploymentAlias.id,
    label: 'My script',
    ttl_seconds: 86_400,
    reason: 'For my script',
  });
  const secret = new RegExp(`\\.${write?.key.replaceAll('-', '') ?? ''}$`);
  await expect(done.getByText(secret)).toBeVisible();
  await done.getByRole('button', { name: 'Done' }).click();

  const drawer = page.getByRole('dialog', { name: 'My script' });
  await expect(drawer.getByRole('heading', { name: 'Use this key' })).toBeVisible();
  await expect(drawer.getByText('https://gateway.example.test/v1', { exact: true })).toBeVisible();
  await expect(page.getByText(secret)).toHaveCount(0);
  await expect(drawer.locator('pre')).toContainText('Bearer $OWNSOLID_KEY');
  await expect(drawer.getByText('--cacert')).toBeVisible();

  await drawer.getByRole('tab', { name: 'Python' }).click();
  await expect(drawer.locator('pre')).toContainText('os.environ["OWNSOLID_KEY"]');
  await expect(drawer.getByText('SSL_CERT_FILE')).toBeVisible();

  await drawer.getByRole('tab', { name: 'Python' }).press('ArrowRight');
  await expect(drawer.locator('pre')).toContainText('process.env.OWNSOLID_KEY');
  await expect(drawer.getByText('NODE_EXTRA_CA_CERTS')).toBeVisible();
  await expect(drawer.locator('pre')).toContainText(`model: "${deploymentAlias.name}"`);
});

test('says to ask an administrator when no model is on the gateway', async ({ page }) => {
  control.aliases = [];
  await page.goto('/keys#issue');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(dialog.getByText('Ask an administrator to publish one')).toBeVisible();
  await expect(dialog.getByRole('link')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Issue', exact: true })).toBeDisabled();
});

test('a lost answer is retried with the same key, and says the secret cannot be shown', async ({
  page,
}) => {
  control.keyWriteFailures = ['lost'];
  await page.goto('/keys#issue');
  await issue(page, 'My script');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await dialog.getByRole('button', { name: 'Try again' }).click();
  const done = page.getByRole('dialog', { name: 'My script is issued' });
  await expect(done.getByText('its secret cannot be shown again')).toBeVisible();
  expect(control.keyWrites.map((write) => write.key)).toEqual([
    control.keyWrites[0]?.key,
    control.keyWrites[0]?.key,
  ]);
  expect(control.gatewayKeys).toHaveLength(1);
});

test('a refused issue says why and keeps the form', async ({ page }) => {
  control.keyWriteFailures = [{ status: 403, code: 'LICENSE_NEW_BUSINESS_UNAVAILABLE' }];
  await page.goto('/keys#issue');
  await issue(page, 'My script');
  const dialog = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(dialog.getByRole('alert')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveValue('My script');
});

test('tiles count and filter from the address', async ({ page }) => {
  control.gatewayKeys = [deploymentKey, expiringKey, unusedKey, revokedKey];
  await page.goto('/keys?tile=expiring');
  await expect(page.getByRole('row', { name: /Nightly report/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Support bot/ })).toHaveCount(0);
  await page.goto('/keys?tile=unused&name=nothing');
  await expect(page.getByRole('heading', { name: 'No key matches' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('row', { name: /Support bot/ })).toBeVisible();
});

test('a failed read says what happened and retries', async ({ page }) => {
  control.keysProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/keys');
  await page.getByRole('button', { name: 'Try again' }).waitFor();
  control.keysProblem = null;
  control.gatewayKeys = [deploymentKey];
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('row', { name: /Support bot/ })).toBeVisible();
});

test("a key's calls page, and a failed read retries on its own", async ({ page }) => {
  control.gatewayKeys = [deploymentKey, unusedKey];
  control.keyCalls = [
    ...Array.from({ length: 25 }, (_, index) => keyCall(index)),
    keyCall(0, unusedKey),
  ];
  control.callsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/keys');
  const drawer = await openDrawer(page, deploymentKey.label);
  await drawer.getByRole('button', { name: 'Try again' }).waitFor();
  control.callsProblem = null;
  await drawer.getByRole('button', { name: 'Try again' }).click();
  await expect(drawer.getByRole('row')).toHaveCount(21);
  await drawer.getByRole('button', { name: 'Load more' }).click();
  await expect(drawer.getByRole('row')).toHaveCount(26);
});

test('says when the model has left the gateway or is not answering', async ({ page }) => {
  control.aliases = [{ ...deploymentAlias, disabled_at: '2026-11-08T00:00:00Z', available: false }];
  control.gatewayKeys = [deploymentKey];
  await page.goto('/keys');
  let drawer = await openDrawer(page, deploymentKey.label);
  await expect(drawer.getByText('no longer on the gateway')).toBeVisible();
  await expect(drawer.locator('pre')).toHaveCount(0);

  await page.keyboard.press('Escape');
  control.aliases = [{ ...deploymentAlias, available: false }];
  await page.reload();
  drawer = await openDrawer(page, deploymentKey.label);
  await expect(drawer.getByText('not answering at the moment')).toBeVisible();
  await expect(drawer.locator('pre')).toContainText('/v1/chat/completions');
});

test('a stopped key has nothing to call', async ({ page }) => {
  control.gatewayKeys = [revokedKey];
  await page.goto('/keys');
  const drawer = await openDrawer(page, revokedKey.label);
  await expect(drawer.getByRole('heading', { name: 'Use this key' })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: 'Rotate' })).toHaveCount(0);
});

test('rotates a key with an overlap: a new secret now, the old one until then', async ({
  page,
}) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto('/keys');
  const drawer = await openDrawer(page, deploymentKey.label);
  await drawer.getByRole('button', { name: 'Rotate' }).click();
  const dialog = page.getByRole('dialog', { name: `Rotate ${deploymentKey.label}` });
  await expect(dialog.getByText('Both secrets work until')).toBeVisible();
  await dialog.getByLabel('Reason').fill('Moving hosts');
  await dialog.getByRole('button', { name: 'Rotate', exact: true }).click();
  const done = page.getByRole('dialog', { name: `${deploymentKey.label} has a new secret` });
  await expect(done.getByText('It is shown only this once.')).toBeVisible();
  expect(control.keyWrites[0]).toMatchObject({
    kind: 'rotate',
    body: { overlap_seconds: 86_400, reason: 'Moving hosts' },
  });
});

test('revokes a key once its name is typed, naming what stops', async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  await page.goto('/keys');
  const drawer = await openDrawer(page, deploymentKey.label);
  await drawer.getByRole('button', { name: 'Revoke' }).click();
  const dialog = page.getByRole('dialog', { name: `Revoke ${deploymentKey.label}` });
  await expect(dialog.getByText(`Calls to ${deploymentKey.alias_name}`)).toBeVisible();
  await dialog.getByLabel('Reason').fill('No longer needed');
  const confirm = dialog.getByRole('button', { name: 'Revoke', exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole('textbox', { name: /^Type/ }).fill(deploymentKey.label);
  await confirm.click();
  await expect(page.getByRole('row', { name: /Support bot/ })).toContainText('Revoked');
  expect(control.keyWrites[0]).toMatchObject({
    kind: 'revoke',
    body: { reason: 'No longer needed' },
  });
});

test('reads in Chinese', async ({ page }) => {
  control.gatewayKeys = [deploymentKey];
  control.preferences.locale = 'zh-CN';
  await page.goto('/keys');
  await expect(page.getByRole('heading', { name: chinese['keys.title'], level: 1 })).toBeVisible();
  await page.getByRole('button', { name: new RegExp(deploymentKey.label) }).click();
  await expect(page.getByRole('heading', { name: chinese['gateway.use.title'] })).toBeVisible();
});
