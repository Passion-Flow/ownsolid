// The model catalogue on a real installation, in a session that is not an administrator's. A fixture
// publishes a real deployment on a scripted node; no model runs. The browser reads what the server
// joins to a published name, and a workspace the person does not belong to is refused.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const personCommand = process.env.OWNSOLID_LIVE_PERSON_COMMAND ?? '';
const name = `cat-${String(Date.now())}`;

test.skip(origin === '' || fixture === '' || personCommand === '', 'needs the gateway fixture');
test.use(liveOptions);

/** Runs a fixture command with arguments and gives what it printed. */
function run(command: string, ...args: string[]): string {
  return execFileSync('sh', ['-c', `${command} "$@"`, 'fixture', ...args], {
    encoding: 'utf8',
  }).trim();
}

/** Asks the server directly, as a page could, with this session's cookies. */
function statusOf(page: Page, path: string): Promise<number> {
  return page.evaluate(async (url) => (await fetch(url)).status, path);
}

test('a person reads what their workspace may call, and nothing of another', async ({
  browser,
}) => {
  test.setTimeout(10 * 60_000);
  run(fixture, 'prepare', name);
  const person = JSON.parse(run(personCommand, name)) as {
    email: string;
    password: string;
    workspace: string;
    other_workspace: string;
  };

  const context = await browser.newContext(liveOptions);
  const page = await context.newPage();
  const outside: string[] = [];
  const administrative: string[] = [];
  context.on('request', (request) => {
    const url = new URL(request.url());
    if (url.origin !== origin && url.protocol !== 'blob:') outside.push(request.url());
    if (url.pathname.includes('/admin/')) administrative.push(url.pathname);
  });

  await page.goto('/models');
  await page.getByLabel('Email').fill(person.email);
  await page.getByLabel('Password').fill(person.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();

  // The name, the model joined to it through its deployment, and whether it can be called.
  const row = page.getByRole('row', { name: new RegExp(name) });
  await expect(row.getByText('Published model')).toBeVisible();
  await expect(row.getByText('2,048 tokens')).toBeVisible();
  await expect(row.getByText('Can be called')).toBeVisible();
  await expect(row.getByText('Not known')).toHaveCount(0);
  await expectAccessible(page);

  await page.getByRole('button', { name: `Show ${name}` }).click();
  const drawer = page.getByRole('dialog', { name });
  await expect(drawer.getByText('https://gateway.lane-b.test:18443/v1')).toBeVisible();
  await expect(drawer.getByText('Qwen/Qwen3-0.6B')).toBeVisible();
  await expect(drawer.getByText('Apache-2.0')).toBeVisible();
  await expect(drawer.getByText('Not ready in this release', { exact: false })).toBeVisible();
  await expectAccessible(page);

  // The way on from here works: the published model can take a key.
  await drawer.getByRole('link', { name: 'Issue a key' }).click();
  const issuing = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(issuing.getByLabel('Model').locator('option', { hasText: name })).toHaveCount(1);
  await issuing.getByRole('button', { name: 'Cancel' }).click();

  // Each read answers for the person's own workspace and is refused for another.
  const base = '/api/v1/sites/default/cells/default/workspaces';
  for (const read of ['distribution/aliases?limit=20', 'deployments', 'models']) {
    expect(await statusOf(page, `${base}/${person.workspace}/${read}`)).toBe(200);
    expect([403, 404]).toContain(await statusOf(page, `${base}/${person.other_workspace}/${read}`));
  }

  expect(outside).toEqual([]);
  expect(administrative).toEqual([]);
  await context.close();
});
