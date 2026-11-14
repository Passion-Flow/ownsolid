// Gateway and keys on a real installation. A fixture readies a deployment on a scripted node,
// provisions a gateway instance and publishes an alias, then stands in for the gateway process
// (GW-114) by admitting calls over the real /gateway/v1 protocol. The browser issues, rotates and
// revokes keys against the real control plane and reads each key's calls; no model runs.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const name = `gw-${String(Date.now())}`;

test.skip(origin === '' || fixture === '', 'needs the gateway fixture');
test.use(liveOptions);

/** Runs the fixture with arguments and gives what it printed. */
function run(...args: string[]): string {
  return execFileSync('sh', ['-c', `${fixture} "$@"`, 'fixture', ...args], {
    encoding: 'utf8',
  }).trim();
}

async function signIn(page: Page, path: string) {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
}

/** The secret a dialog shows once. */
async function secretIn(page: Page, title: string): Promise<string> {
  const dialog = page.getByRole('dialog', { name: title });
  await expect(dialog.getByText('It is shown only this once.')).toBeVisible();
  const secret = (await dialog.getByText(/^pk1\./).textContent()) ?? '';
  expect(secret).toMatch(/^pk1\.[0-9a-f-]{36}\./);
  await dialog.getByRole('button', { name: 'Done' }).click();
  return secret;
}

async function openKey(page: Page, label: string, index = 0) {
  await page.goto('/gateway');
  await page
    .getByRole('button', { name: `Show ${label}` })
    .nth(index)
    .click();
  return page.getByRole('dialog', { name: label });
}

test('issues, rotates and revokes keys, and each key logs only its own calls', async ({
  browser,
}) => {
  test.setTimeout(10 * 60_000);
  const state = run('prepare', name);
  const context = await browser.newContext();
  const page = await context.newPage();
  const outside: string[] = [];
  context.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      outside.push(request.url());
    }
  });
  await signIn(page, '/gateway#issue');
  const issuing = page.getByRole('dialog', { name: 'Issue a key' });
  await issuing.getByLabel('Model').selectOption({ label: name });
  await issuing.getByLabel('Name').fill(name);
  await issuing.getByLabel('Expires after').selectOption({ label: '7 days' });
  await issuing.getByLabel('Reason').fill('Live gateway keys run');
  await expectAccessible(page);
  await issuing.getByRole('button', { name: 'Issue', exact: true }).click();
  const first = await secretIn(page, `${name} is issued`);
  await expect(page.getByRole('row', { name: new RegExp(name) })).toContainText('Never');

  // The scope is enforced by the control plane: the key calls its model and nothing else.
  expect(run('call', state, first, '3')).toBe('3');
  expect(run('wrong', state, first)).toMatch(/^4\d\d /);

  let drawer = await openKey(page, name);
  const calls = drawer.getByRole('table', { name: 'Calls with this key' });
  await expect(calls.getByRole('row')).toHaveCount(4);
  await expect(calls.getByRole('row').nth(1)).toContainText('Succeeded');
  await expect(calls.getByRole('row').nth(1)).toContainText('12');
  await expectAccessible(page);

  await drawer.getByRole('button', { name: 'Rotate' }).click();
  const rotating = page.getByRole('dialog', { name: `Rotate ${name}` });
  await rotating.getByLabel('The old secret keeps working').selectOption({ label: 'For 1 hour' });
  await expect(rotating.getByText(/Both secrets work until/)).toBeVisible();
  await rotating.getByLabel('Reason').fill('Live rotation');
  await rotating.getByRole('button', { name: 'Rotate', exact: true }).click();
  const second = await secretIn(page, `${name} has a new secret`);
  expect(second).not.toBe(first);
  // Both secrets work during the overlap.
  expect(run('call', state, first, '1')).toBe('1');
  expect(run('call', state, second, '1')).toBe('1');

  await page.goto('/gateway');
  const rows = page.getByRole('row', { name: new RegExp(name) });
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: 'Being replaced' })).toHaveCount(1);

  // The newer key is listed first; revoke it by typing its name.
  drawer = await openKey(page, name, 0);
  await expect(drawer.getByText('Replaces')).toBeVisible();
  await expect(
    drawer.getByRole('table', { name: 'Calls with this key' }).getByRole('row'),
  ).toHaveCount(2);
  await drawer.getByRole('button', { name: 'Revoke' }).click();
  const revoking = page.getByRole('dialog', { name: `Revoke ${name}` });
  await revoking.getByLabel(/Type/).fill(name);
  await revoking.getByLabel('Reason').fill('Live revocation');
  await revoking.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(page.getByText(`${name} is revoked`)).toBeVisible();
  expect(() => run('call', state, second, '1')).toThrow();

  drawer = await openKey(page, name, 1);
  await expect(drawer.getByText('Being replaced')).toBeVisible();
  await expect(
    drawer.getByRole('table', { name: 'Calls with this key' }).getByRole('row'),
  ).toHaveCount(5);
  await page.goto('/audit');
  await expect(page.getByRole('main').getByText('API key rotated').first()).toBeVisible();
  expect(outside).toEqual([]);
  await context.close();
});
