// The model catalogue on a real installation, in a session that is not an administrator's. A fixture
// publishes a real deployment on a scripted node; no model runs. The browser reads what the server
// joins to a published name, and a workspace the person does not belong to is refused.
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import type { ModelAlias } from '@ownsolid/api';
import { expectAccessible } from '@ownsolid/shell/testing';
import { liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const personCommand = process.env.OWNSOLID_LIVE_PERSON_COMMAND ?? '';
const descriptionFixture = process.env.OWNSOLID_LIVE_MODEL_DESCRIPTION_FILE ?? '';
const name = `cat-${String(Date.now())}`;

test.use({ ...liveOptions, trace: 'off' });

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
}, testInfo) => {
  test.skip(origin === '' || fixture === '' || personCommand === '', 'needs the gateway fixture');
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

  const modelFacts = `/api/v1/sites/default/cells/default/workspaces/${person.workspace}/models`;
  const factsRoute = (url: URL) => url.pathname === modelFacts;
  await page.route(factsRoute, (route) => route.abort('connectionreset'));
  await page.goto('/models');
  await page.getByLabel('Email').fill(person.email);
  await page.getByLabel('Password').fill(person.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();

  // The server still answers for aliases and deployments while the model facts cannot be read.
  const row = page.getByRole('row', { name: new RegExp(name) });
  const retry = page.getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  await expect(page.getByText('Some model details could not load')).toBeVisible();
  await expect(row.getByText('Not known').first()).toBeVisible();
  await expect(row.getByText('Can be called')).toBeVisible();
  await testInfo.attach('portal-model-facts-unavailable', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await page.unroute(factsRoute);
  const facts = page.waitForResponse(
    (response) => factsRoute(new URL(response.url())) && response.ok(),
  );
  await retry.click();
  await facts;
  await expect(row.getByText('Published model')).toBeVisible();
  await expect(row.getByText('2,048 tokens')).toBeVisible();
  await expect(row.getByText('Can be called')).toBeVisible();
  await expect(row.getByText('Not known')).toHaveCount(0);
  await expect(page.getByText('Some model details could not load')).toHaveCount(0);
  await expectAccessible(page);

  await page.getByRole('button', { name: `Show ${name}` }).click();
  const drawer = page.getByRole('dialog', { name });
  await expect(drawer.getByText('https://gateway.lane-b.test:18443/v1')).toBeVisible();
  await expect(drawer.getByText('Qwen/Qwen3-0.6B')).toBeVisible();
  await expect(drawer.getByText('Apache-2.0')).toBeVisible();
  await expect(drawer.getByText('Price', { exact: true })).toHaveCount(0);
  await expectAccessible(page);

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

interface DescriptionFixture {
  email: string;
  password: string;
  workspace: string;
  other_workspace: string;
  aliases: { name: string; description?: string }[];
}

async function readAliasDocument(page: Page, path: string) {
  return page.evaluate(async (url) => {
    const response = await fetch(url);
    return { status: response.status, body: (await response.json()) as unknown };
  }, path);
}

test('renders authorized alias descriptions and keeps them when model facts fail', async ({
  page,
}, info) => {
  test.skip(origin === '' || descriptionFixture === '', 'needs the alias description fixture');
  test.setTimeout(60_000);
  const person = JSON.parse(await readFile(descriptionFixture, 'utf8')) as DescriptionFixture;
  expect(person.aliases.length).toBeGreaterThan(0);
  const outside: string[] = [];
  const admin: string[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname.includes('/admin/')) admin.push(url.pathname);
  });
  await page.route(
    (url) => (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== origin,
    async (route) => {
      outside.push(new URL(route.request().url()).origin);
      await route.abort();
    },
  );

  const base = '/api/v1/sites/default/cells/default/workspaces';
  const scope = `${base}/${person.workspace}`;
  const aliases = `${scope}/distribution/aliases`;
  const factsRoute = (url: URL) => url.pathname === `${scope}/models`;
  const candidate = person.aliases.some(({ description }) => description !== undefined);
  if (candidate) await page.route(factsRoute, (route) => route.abort('connectionreset'));
  await page.goto('/models');
  await page.getByLabel('Email').fill(person.email);
  await page.getByLabel('Password').fill(person.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();
  if (candidate) {
    await expect(page.getByText('Some model details could not load')).toBeVisible({
      timeout: 15_000,
    });
    await info.attach('portal-descriptions-partial', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
  }

  const list = await readAliasDocument(page, `${aliases}?limit=20`);
  expect(list.status).toBe(200);
  const { items } = (list.body as { data: { items: ModelAlias[] } }).data;
  for (const expected of person.aliases) {
    const alias = items.find((item) => item.name === expected.name);
    expect(alias).toBeDefined();
    const id = alias?.id ?? '';
    const detail = await readAliasDocument(page, `${aliases}/${id}`);
    expect(detail.status).toBe(200);
    const body = (detail.body as { data: ModelAlias }).data;
    for (const model of [alias, body]) {
      if (expected.description === undefined) {
        expect(model).not.toHaveProperty('description');
        expect(model).not.toHaveProperty('description_revision');
      } else {
        expect(model).toMatchObject({ description: expected.description });
        expect(model?.description_revision).toBeGreaterThan(0);
      }
    }

    const foreign = `${base}/${person.other_workspace}/distribution/aliases`;
    for (const path of [`${foreign}?limit=20`, `${foreign}/${id}`]) {
      const refused = await readAliasDocument(page, path);
      expect(refused.status).toBe(404);
      expect(refused.body).toMatchObject({ code: 'RESOURCE_NOT_FOUND' });
      if (expected.description)
        expect(JSON.stringify(refused.body)).not.toContain(expected.description);
    }

    const row = page.getByRole('row').filter({ hasText: expected.name });
    await expect(row.getByText('Can be called')).toBeVisible();
    if (candidate) await expect(row.getByText('Not known').first()).toBeVisible();
    await page.getByRole('button', { name: `Show ${expected.name}` }).click();
    const drawer = page.getByRole('dialog', { name: expected.name });
    if (expected.description) {
      const description = drawer.getByText(expected.description, { exact: true });
      await expect(description).toBeVisible();
      expect(await description.textContent()).toBe(expected.description);
      await expect(drawer.locator('img')).toHaveCount(0);
    } else {
      await expect(drawer.getByText('Chat and text generation', { exact: false })).toBeVisible();
    }
    await expect(
      drawer.getByText('The administrator has not added a description yet.'),
    ).toHaveCount(expected.description === '' ? 1 : 0);
    await expect(drawer.getByText('Price', { exact: true })).toHaveCount(0);
    await expectAccessible(page);
    await info.attach(`portal-description-${expected.name}`, {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    await page.keyboard.press('Escape');
  }
  if (candidate) {
    await page.unroute(factsRoute);
    const recovered = page.waitForResponse(
      (response) => factsRoute(new URL(response.url())) && response.ok(),
    );
    await page.getByRole('button', { name: 'Try again' }).click();
    await recovered;
    for (const { name } of person.aliases) {
      await expect(
        page.getByRole('row').filter({ hasText: name }).getByText('Not known'),
      ).toHaveCount(0);
    }
    await expect(page.getByText('Some model details could not load')).toHaveCount(0);
  }
  expect(outside).toEqual([]);
  expect(admin).toEqual([]);
});
