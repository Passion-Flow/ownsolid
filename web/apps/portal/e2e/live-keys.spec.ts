// A person's own keys on a real installation, in a session that is not an administrator's. A fixture
// readies a published model, and stands in for the gateway process (GW-114) by admitting calls over
// the real /gateway/v1 protocol; no model runs. The browser issues, rotates and revokes a key, reads
// its calls, and proves that nobody else's session and no other workspace can reach it.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Browser, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const personCommand = process.env.OWNSOLID_LIVE_PERSON_COMMAND ?? '';
const name = `key-${String(Date.now())}`;

test.skip(origin === '' || fixture === '' || personCommand === '', 'needs the gateway fixture');
test.use(liveOptions);

/** Runs a fixture command with arguments and gives what it printed. */
function run(command: string, ...args: string[]): string {
  return execFileSync('sh', ['-c', `${command} "$@"`, 'fixture', ...args], {
    encoding: 'utf8',
  }).trim();
}

async function signedIn(browser: Browser, address: string, password: string): Promise<Page> {
  const context = await browser.newContext(liveOptions);
  const page = await context.newPage();
  await page.goto('/keys');
  await page.getByLabel('Email').fill(address);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'My API keys', level: 1 })).toBeVisible();
  return page;
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

async function openKey(page: Page, index = 0) {
  await page.goto('/keys');
  await page
    .getByRole('button', { name: `Show ${name}` })
    .nth(index)
    .click();
  return page.getByRole('dialog', { name });
}

/** Asks the server directly, as a page could, with this session's cookies. */
function statusOf(page: Page, path: string): Promise<number> {
  return page.evaluate(async (url) => (await fetch(url)).status, path);
}

test('a person issues, uses, rotates and revokes their own key, and nobody else reaches it', async ({
  browser,
}) => {
  test.setTimeout(10 * 60_000);
  const state = run(fixture, 'prepare', name);
  const person = JSON.parse(run(personCommand, name)) as {
    email: string;
    password: string;
    workspace: string;
    other_workspace: string;
  };
  const keysPath = (workspace: string) =>
    `/api/v1/sites/default/cells/default/workspaces/${workspace}/distribution/keys`;

  const page = await signedIn(browser, person.email, person.password);
  const context = page.context();
  const outside: string[] = [];
  const administrative: string[] = [];
  context.on('request', (request) => {
    const url = new URL(request.url());
    if (url.origin !== origin && url.protocol !== 'blob:') outside.push(request.url());
    if (url.pathname.includes('/admin/')) administrative.push(url.pathname);
  });

  await page.getByRole('button', { name: 'Issue a key' }).first().click();
  const issuing = page.getByRole('dialog', { name: 'Issue a key' });
  await issuing.getByLabel('Model').selectOption({ label: name });
  await issuing.getByLabel('Name').fill(name);
  await issuing.getByLabel('Expires after').selectOption({ label: '7 days' });
  await issuing.getByLabel('Reason').fill('Live portal keys run');
  await expectAccessible(page);
  await issuing.getByRole('button', { name: 'Issue', exact: true }).click();
  const first = await secretIn(page, `${name} is issued`);

  // Done opens the key with what is needed to call it, and the secret is gone.
  const opened = page.getByRole('dialog', { name });
  await expect(opened.getByRole('heading', { name: 'Use this key' })).toBeVisible();
  await expect(opened.locator('pre')).toContainText('https://gateway.lane-b.test:18443/v1');
  await expect(opened.locator('pre')).toContainText(name);
  await expect(page.getByText(first)).toHaveCount(0);
  await expectAccessible(page);

  // The scope is enforced by the control plane: the key calls its model and nothing else.
  expect(run(fixture, 'call', state, first, '3')).toBe('3');
  expect(run(fixture, 'wrong', state, first)).toMatch(/^4\d\d /);
  let drawer = await openKey(page);
  const calls = drawer.getByRole('table', { name: 'Calls with this key' });
  await expect(calls.getByRole('row')).toHaveCount(4);
  await expect(calls.getByRole('row').nth(1)).toContainText('Succeeded');

  await drawer.getByRole('button', { name: 'Rotate' }).click();
  const rotating = page.getByRole('dialog', { name: `Rotate ${name}` });
  await rotating.getByLabel('The old secret keeps working').selectOption({ label: 'For 1 hour' });
  await rotating.getByLabel('Reason').fill('Live rotation');
  await rotating.getByRole('button', { name: 'Rotate', exact: true }).click();
  const second = await secretIn(page, `${name} has a new secret`);
  expect(second).not.toBe(first);
  expect(run(fixture, 'call', state, first, '1')).toBe('1');
  expect(run(fixture, 'call', state, second, '1')).toBe('1');

  // Nobody else's session can see or reach the key, an administrator's portal session included.
  const keyId = first.split('.')[1] ?? '';
  const stranger = await signedIn(browser, email, adminPassword());
  await expect(stranger.getByRole('button', { name: `Show ${name}` })).toHaveCount(0);
  expect(await statusOf(stranger, `${keysPath(person.workspace)}/${keyId}`)).toBe(404);
  await stranger.context().close();

  // A workspace the person does not belong to is refused rather than filtered.
  expect(await statusOf(page, `${keysPath(person.workspace)}?limit=20`)).toBe(200);
  expect([403, 404]).toContain(
    await statusOf(page, `${keysPath(person.other_workspace)}?limit=20`),
  );

  // The newer key is listed first; revoke it by typing its name.
  drawer = await openKey(page, 0);
  await expect(drawer.getByText('Replaces')).toBeVisible();
  await drawer.getByRole('button', { name: 'Revoke' }).click();
  const revoking = page.getByRole('dialog', { name: `Revoke ${name}` });
  await revoking.getByLabel(/Type/).fill(name);
  await revoking.getByLabel('Reason').fill('Live revocation');
  await revoking.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(page.getByText(`${name} is revoked`)).toBeVisible();
  expect(() => run(fixture, 'call', state, second, '1')).toThrow();

  expect(outside).toEqual([]);
  expect(administrative).toEqual([]);
  await context.close();
});
