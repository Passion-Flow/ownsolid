// A person's own keys on a real installation, in a session that is not an administrator's. A fixture
// readies a published model. With a native gateway URL, calls use its public HTTPS endpoint.
// The protocol fixture remains available for installations without that listener. The browser
// issues, rotates and revokes the key, and proves its call history stays with its owner.
import { execFileSync } from 'node:child_process';
import type { GatewayCall } from '@ownsolid/api';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Browser, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const personCommand = process.env.OWNSOLID_LIVE_PERSON_COMMAND ?? '';
const nativeGateway = process.env.OWNSOLID_LIVE_NATIVE_GATEWAY_URL ?? '';
const name = `key-${String(Date.now())}`;
const keySecret = /^pk1\.[0-9a-f-]{36}\./;

test.skip(origin === '' || fixture === '' || personCommand === '', 'needs the gateway fixture');
test.use({ ...liveOptions, trace: 'off' });

/** Runs a fixture command with arguments and gives what it printed. */
function run(command: string, args: string[], secret?: string): string {
  return execFileSync('sh', ['-c', `${command} "$@"`, 'fixture', ...args], {
    encoding: 'utf8',
    input: secret,
  }).trim();
}

type Usage = NonNullable<NonNullable<GatewayCall['outcome']>['usage']>;
interface GatewayReceipt {
  id: string;
  usage: Usage;
}

async function nativeCompletion(model: string, key: string) {
  const base = new URL(nativeGateway);
  if (
    base.protocol !== 'https:' ||
    base.pathname !== '/v1' ||
    base.username ||
    base.password ||
    base.search ||
    base.hash
  ) {
    throw new Error('The native gateway needs its canonical HTTPS base URL ending in /v1');
  }
  const response = await fetch(new URL('/v1/chat/completions', base), {
    method: 'POST',
    headers: { authorization: `Bearer ${key}`, 'content-type': 'application/json' },
    body: JSON.stringify({
      model,
      messages: [{ role: 'user', content: 'Reply to this isolated Portal test.' }],
      max_tokens: 16,
      stream: false,
    }),
    redirect: 'error',
    signal: AbortSignal.timeout(20_000),
  });
  const body = (await response.json()) as {
    object?: string;
    model?: string;
    choices?: { message?: { content?: string } }[];
    usage?: Usage;
    error?: { code?: string };
  };
  return { status: response.status, id: response.headers.get('x-request-id') ?? '', body };
}

async function callGateway(
  state: string,
  secret: string,
  count: number,
): Promise<GatewayReceipt[]> {
  if (!nativeGateway) {
    expect(run(fixture, ['call', state, String(count)], secret)).toBe(String(count));
    return [];
  }
  const receipts: GatewayReceipt[] = [];
  for (let index = 0; index < count; index++) {
    const { status, id, body } = await nativeCompletion(name, secret);
    expect(status, body.error?.code).toBe(200);
    expect(id).toMatch(/^[0-9a-f-]{36}$/);
    expect(body).toMatchObject({ object: 'chat.completion', model: name });
    expect(body.choices?.[0]?.message?.content).toBeTruthy();
    if (!body.usage) throw new Error('The gateway completion has no token usage');
    expect(body.usage.total_tokens).toBeGreaterThan(0);
    receipts.push({ id, usage: body.usage });
  }
  return receipts;
}

function expectReceipt(requests: GatewayCall[], receipt: GatewayReceipt) {
  expect(requests.find(({ id }) => id === receipt.id)).toMatchObject({
    state: 'reported',
    capacity_held: false,
    outcome: { request_id: receipt.id, status: 'succeeded', usage: receipt.usage },
  });
}

async function signedIn(
  browser: Browser,
  address: string,
  password: string,
  path: '/keys' | '/models',
): Promise<Page> {
  const context = await browser.newContext(liveOptions);
  const page = await context.newPage();
  await page.goto(path);
  await page.getByLabel('Email').fill(address);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(
    page.getByRole('heading', { name: path === '/keys' ? 'My API keys' : 'Models', level: 1 }),
  ).toBeVisible();
  return page;
}

/** The secret a dialog shows once. */
async function secretIn(page: Page, title: string): Promise<string> {
  const dialog = page.getByRole('dialog', { name: title });
  await expect(dialog.getByText('It is shown only this once.')).toBeVisible();
  const secret = (await dialog.getByText(keySecret).textContent()) ?? '';
  expect(keySecret.test(secret)).toBe(true);
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

async function readCalls(page: Page, path: string): Promise<GatewayCall[]> {
  return page.evaluate(async (url) => {
    const response = await fetch(url, { cache: 'no-store' });
    if (!response.ok) throw new Error(`Call history returned HTTP ${response.status}`);
    return ((await response.json()) as { data: { items: GatewayCall[] } }).data.items;
  }, path);
}

async function reportedCalls(page: Page, path: string, count: number): Promise<GatewayCall[]> {
  await expect
    .poll(async () => (await readCalls(page, path)).map(({ state }) => state), { timeout: 15_000 })
    .toEqual(Array.from({ length: count }, () => 'reported'));
  return readCalls(page, path);
}

async function selectWorkspace(page: Page, name: string) {
  await page.getByRole('button', { name: /Switch workspace/ }).click();
  await page.getByRole('menuitem', { name, exact: true }).click();
  await expect(
    page.getByRole('button', { name: `Workspace: ${name}. Switch workspace` }),
  ).toBeVisible();
}

test('a person goes from a published model to their own key and scoped calls', async ({
  browser,
}, testInfo) => {
  test.setTimeout(10 * 60_000);
  const state = run(fixture, ['prepare', name]);
  const person = JSON.parse(run(personCommand, [name])) as {
    email: string;
    password: string;
    workspace: string;
    workspace_name: string;
    switch_workspace_name: string;
    other_workspace: string;
  };
  const distribution = (workspace: string) =>
    `/api/v1/sites/default/cells/default/workspaces/${workspace}/distribution`;

  const page = await signedIn(browser, person.email, person.password, '/models');
  const context = page.context();
  const outside: string[] = [];
  const administrative: string[] = [];
  context.on('request', (request) => {
    const url = new URL(request.url());
    if (url.origin !== origin && url.protocol !== 'blob:') outside.push(request.url());
    if (url.pathname.includes('/admin/')) administrative.push(url.pathname);
  });

  await selectWorkspace(page, person.workspace_name);
  await page.getByRole('button', { name: `Show ${name}` }).click();
  const model = page.getByRole('dialog', { name });
  await expect(model.getByText('Can be called')).toBeVisible();
  await model.getByRole('link', { name: 'Issue a key' }).click();
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
  await expect(opened.locator('pre')).toContainText(
    nativeGateway || 'https://gateway.lane-b.test:18443/v1',
  );
  await expect(opened.locator('pre')).toContainText(name);
  await expect(page.getByText(keySecret)).toHaveCount(0);
  await expectAccessible(page);

  // The scope is enforced by the control plane: the key calls its model and nothing else.
  const initialReceipts = await callGateway(state, first, 3);
  if (nativeGateway) {
    const { status, body } = await nativeCompletion(`not-${name}`, first);
    expect({ status, code: body.error?.code }).toEqual({ status: 404, code: 'model_not_found' });
  } else {
    expect(run(fixture, ['wrong', state], first)).toMatch(/^4\d\d /);
  }
  const keyId = first.split('.')[1] ?? '';
  const history = (workspace: string, id = keyId) =>
    `${distribution(workspace)}/requests?api_key_id=${id}&limit=20`;
  const ownerId = await page.evaluate(async () => {
    const response = await fetch('/api/v1/me');
    if (!response.ok) throw new Error(`Identity read returned HTTP ${response.status}`);
    return ((await response.json()) as { data: { user: { id: string } } }).data.user.id;
  });
  const requests = await reportedCalls(page, history(person.workspace), 3);
  for (const request of requests) {
    expect(request).toMatchObject({
      api_key_id: keyId,
      actor_id: ownerId,
      workspace_id: person.workspace,
      alias_name: name,
      state: 'reported',
    });
  }
  for (const receipt of initialReceipts) expectReceipt(requests, receipt);
  let drawer = await openKey(page);
  const calls = drawer.getByRole('table', { name: 'Calls with this key' });
  await expect(calls.getByRole('row')).toHaveCount(4);
  await expect(calls.getByRole('row').nth(1)).toContainText('Succeeded');
  await calls.scrollIntoViewIfNeeded();
  await testInfo.attach('portal-key-call-history', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await page.goto('/models');
  await expect(page.getByRole('button', { name: `Show ${name}` })).toBeVisible();
  await selectWorkspace(page, person.switch_workspace_name);
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
  await expect(page.getByRole('button', { name: `Show ${name}` })).toHaveCount(0);
  await page.goto('/keys');
  await expect(page.getByRole('heading', { name: 'No keys yet' })).toBeVisible();
  await expect(page.getByRole('button', { name: `Show ${name}` })).toHaveCount(0);
  await testInfo.attach('portal-empty-workspace-keys', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await selectWorkspace(page, person.workspace_name);
  drawer = await openKey(page);
  await expect(
    drawer.getByRole('table', { name: 'Calls with this key' }).getByRole('row'),
  ).toHaveCount(4);

  await drawer.getByRole('button', { name: 'Rotate' }).click();
  const rotating = page.getByRole('dialog', { name: `Rotate ${name}` });
  await rotating.getByLabel('The old secret keeps working').selectOption({ label: 'For 1 hour' });
  await rotating.getByLabel('Reason').fill('Live rotation');
  await rotating.getByRole('button', { name: 'Rotate', exact: true }).click();
  const second = await secretIn(page, `${name} has a new secret`);
  expect(second === first).toBe(false);
  const overlapReceipts = await callGateway(state, first, 1);
  const replacementReceipts = await callGateway(state, second, 1);
  const replacementId = second.split('.')[1] ?? '';

  // Nobody else's session can see or reach the key, an administrator's portal session included.
  const stranger = await signedIn(browser, email, adminPassword(), '/keys');
  await expect(stranger.getByRole('button', { name: `Show ${name}` })).toHaveCount(0);
  expect(await statusOf(stranger, `${distribution(person.workspace)}/keys/${keyId}`)).toBe(404);
  expect(await readCalls(stranger, history(person.workspace))).toEqual([]);
  await stranger.context().close();

  // A workspace the person does not belong to is refused rather than filtered.
  expect(await statusOf(page, `${distribution(person.workspace)}/keys?limit=20`)).toBe(200);
  expect([403, 404]).toContain(
    await statusOf(page, `${distribution(person.other_workspace)}/keys?limit=20`),
  );
  expect([403, 404]).toContain(await statusOf(page, history(person.other_workspace)));

  // The newer key is listed first; revoke it by typing its name.
  drawer = await openKey(page, 0);
  await expect(drawer.getByText('Replaces')).toBeVisible();
  await drawer.getByRole('button', { name: 'Revoke' }).click();
  const revoking = page.getByRole('dialog', { name: `Revoke ${name}` });
  await revoking.getByLabel(/Type/).fill(name);
  await revoking.getByLabel('Reason').fill('Live revocation');
  await revoking.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(page.getByText(`${name} is revoked`)).toBeVisible();
  if (nativeGateway) {
    const { status, id, body } = await nativeCompletion(name, second);
    const refusal = { status, id, code: body.error?.code };
    expect(refusal).toMatchObject({ status: 401, code: 'invalid_api_key' });
    const oldRows = await reportedCalls(page, history(person.workspace), 4);
    const newRows = await reportedCalls(page, history(person.workspace, replacementId), 1);
    for (const receipt of overlapReceipts) expectReceipt(oldRows, receipt);
    for (const receipt of replacementReceipts) expectReceipt(newRows, receipt);
    expect(newRows[0]).toMatchObject({ actor_id: ownerId, workspace_id: person.workspace });
    expect(newRows.some((row) => row.id === id)).toBe(false);
    const revoked = await openKey(page);
    await expect(revoked.getByText('Revoked', { exact: true })).toBeVisible();
    const historyTable = revoked.getByRole('table', { name: 'Calls with this key' });
    await expect(historyTable.getByRole('row')).toHaveCount(2);
    await expect(historyTable.getByRole('row').nth(1)).toContainText('Succeeded');
    await testInfo.attach('portal-native-gateway-receipts', {
      body: JSON.stringify(
        { initialReceipts, overlapReceipts, replacementReceipts, refusal },
        null,
        2,
      ),
      contentType: 'application/json',
    });
  } else {
    expect(() => run(fixture, ['call', state, '1'], second)).toThrow();
  }

  expect(outside).toEqual([]);
  expect(administrative).toEqual([]);
  await context.close();
});
