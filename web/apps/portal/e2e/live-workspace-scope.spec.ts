// POR-114 on the existing Home and Models pages. The fixture seeds real published
// projections in separate workspaces and tenants. No workflow or model runs.
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_PORTAL_SCOPE_FILE ?? '';

test.skip(origin === '' || fixture === '', 'needs the disposable Portal scope fixture');
test.use({ ...liveOptions, trace: 'off' });

interface Workspace {
  id: string;
  name: string;
  application: string;
}

interface ScopeFixture {
  people: { email: string; password: string; workspace: Workspace }[];
  second: Workspace;
  foreign: { workspace: string; marker: string };
}

async function signIn(page: Page, person: ScopeFixture['people'][number]) {
  await page.getByLabel('Email').fill(person.email);
  await page.getByLabel('Password').fill(person.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
}

async function chooseWorkspace(page: Page, workspace: Workspace) {
  await page.getByRole('button', { name: /^Workspace: .*\. Switch workspace$/ }).click();
  await page.getByRole('menuitem', { name: workspace.name, exact: true }).click();
  await expect(
    page.getByRole('button', { name: `Workspace: ${workspace.name}. Switch workspace` }),
  ).toBeVisible();
}

test('refuses the Home request for another tenant and keeps scope across navigation and login', async ({
  page,
}, info) => {
  test.setTimeout(120_000);
  const data = JSON.parse(await readFile(fixture, 'utf8')) as ScopeFixture;
  const [first, next] = data.people;
  expect(first).toBeDefined();
  expect(next).toBeDefined();
  if (!first || !next) throw new Error('The scope fixture needs two ordinary accounts');
  const outside: string[] = [];
  const bodies: Promise<void>[] = [];
  const leaked: string[] = [];
  const unreadable: string[] = [];
  const scopes: { path: string; workspace: string }[] = [];
  const navigation: { stage: string; url: string }[] = [];
  const base = '/api/v1/sites/default/cells/default';
  const applications = `${base}/applications`;
  const published = page.locator('section').filter({
    has: page.getByRole('heading', { name: 'Published to you', exact: true }),
  });
  await page.route(
    (url) => (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== origin,
    async (route) => {
      outside.push(new URL(route.request().url()).origin);
      await route.abort();
    },
  );
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith(base)) return;
    const workspace =
      url.searchParams.get('workspace_id') ?? /\/workspaces\/([^/]+)/.exec(url.pathname)?.[1];
    if (workspace) scopes.push({ path: url.pathname, workspace });
  });
  page.on('response', (response) => {
    if (!new URL(response.url()).pathname.startsWith(base)) return;
    bodies.push(
      response
        .text()
        .then((body) => {
          if (body.includes(data.foreign.marker)) leaked.push(new URL(response.url()).pathname);
        })
        .catch(() => {
          if (response.request().failure()?.errorText !== 'net::ERR_ABORTED') {
            unreadable.push(new URL(response.url()).pathname);
          }
        }),
    );
  });

  await page.goto('/');
  await signIn(page, first);
  await chooseWorkspace(page, first.workspace);
  await expect(published.getByText(first.workspace.application, { exact: true })).toBeVisible();
  const foreign = (url: URL) =>
    url.pathname === applications && url.searchParams.get('workspace_id') === first.workspace.id;
  await page.route(foreign, (route) => {
    const url = new URL(route.request().url());
    url.searchParams.set('workspace_id', data.foreign.workspace);
    return route.continue({ url: url.href });
  });
  const refused = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      url.pathname === applications &&
      url.searchParams.get('workspace_id') === data.foreign.workspace
    );
  });
  await page.reload();
  const response = await refused;
  expect(response.status()).toBe(404);
  expect(await response.headerValue('content-type')).toBe('application/problem+json');
  expect(await response.headerValue('cache-control')).toBe('no-store');
  const problem = (await response.json()) as { code: string; request_id: string };
  expect(problem.code).toBe('RESOURCE_NOT_FOUND');
  expect(problem.request_id).toBe(await response.headerValue('x-request-id'));
  expect(new URL(response.url()).searchParams.get('workspace_id')).toBe(data.foreign.workspace);
  await expect(published.getByText('Not found', { exact: true })).toBeVisible();
  await expect(
    published.getByText('Switch to another workspace, or ask an administrator', { exact: false }),
  ).toBeVisible();
  await expect(published.getByText('Nothing published yet', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Pick up where you left off' })).toBeVisible();
  await info.attach('home-foreign-workspace-refused', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  navigation.push({ stage: 'refused-home', url: page.url() });
  await page.unroute(foreign);
  await page.reload();
  await expect(published.getByText(first.workspace.application, { exact: true })).toBeVisible();

  await chooseWorkspace(page, data.second);
  await expect(published.getByText(data.second.application, { exact: true })).toBeVisible();
  await expect(page.getByText(first.workspace.application, { exact: true })).toHaveCount(0);
  const beforeModels = scopes.length;
  await page.getByRole('navigation').getByRole('button', { name: 'AI tools', exact: true }).click();
  await page.getByRole('navigation').getByRole('link', { name: 'Models', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Models', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No models yet', exact: true })).toBeVisible();
  navigation.push({ stage: 'models-second-workspace', url: page.url() });
  for (const path of ['distribution/aliases', 'deployments', 'models']) {
    expect(
      scopes
        .slice(beforeModels)
        .some((read) => read.path === `${base}/workspaces/${data.second.id}/${path}`),
    ).toBe(true);
  }
  const aliasesPath = `${base}/workspaces/${data.second.id}/distribution/aliases`;
  const aliasesRoute = (url: URL) => url.pathname === aliasesPath;
  await page.route(aliasesRoute, (route) => {
    const url = new URL(route.request().url());
    url.pathname = `${base}/workspaces/${data.foreign.workspace}/distribution/aliases`;
    return route.continue({ url: url.href });
  });
  const refusedAliases = page.waitForResponse(
    (reply) =>
      new URL(reply.url()).pathname ===
      `${base}/workspaces/${data.foreign.workspace}/distribution/aliases`,
  );
  await page.reload();
  const aliasResponse = await refusedAliases;
  expect(aliasResponse.status()).toBe(404);
  expect(await aliasResponse.headerValue('content-type')).toBe('application/problem+json');
  expect(await aliasResponse.headerValue('cache-control')).toBe('no-store');
  const aliasProblem = (await aliasResponse.json()) as { code: string; request_id: string };
  expect(aliasProblem.code).toBe('RESOURCE_NOT_FOUND');
  expect(aliasProblem.request_id).toBe(await aliasResponse.headerValue('x-request-id'));
  expect(new URL(aliasResponse.url()).pathname).toBe(
    `${base}/workspaces/${data.foreign.workspace}/distribution/aliases`,
  );
  await expect(page.getByText('Not found', { exact: true })).toBeVisible();
  await expect(
    page.getByText('Go back to the list and open it from there.', { exact: false }),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No models yet', exact: true })).toHaveCount(0);
  await info.attach('models-foreign-workspace-refused', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  navigation.push({ stage: 'refused-models', url: page.url() });
  await page.unroute(aliasesRoute);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'No models yet', exact: true })).toBeVisible();
  await info.attach('models-selected-workspace', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await page.goBack();
  await expect(published.getByText(data.second.application, { exact: true })).toBeVisible();
  navigation.push({ stage: 'back-home-second-workspace', url: page.url() });
  await page.reload();
  await expect(published.getByText(data.second.application, { exact: true })).toBeVisible();
  const parameterAt = scopes.length;
  await page.goto(`/?workspace_id=${data.foreign.workspace}`);
  await expect(published.getByText(data.second.application, { exact: true })).toBeVisible();
  expect(scopes.slice(parameterAt).length).toBeGreaterThan(0);
  expect(scopes.slice(parameterAt).every((read) => read.workspace === data.second.id)).toBe(true);
  navigation.push({ stage: 'page-parameter-keeps-selected-scope', url: page.url() });
  await info.attach('home-selected-workspace-after-history', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await chooseWorkspace(page, first.workspace);
  await expect(published.getByText(first.workspace.application, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await expect(page.getByLabel('Email')).toBeVisible();
  const switchedAt = scopes.length;
  await signIn(page, next);
  await expect(published.getByText(next.workspace.application, { exact: true })).toBeVisible();
  await expect(page.getByText(first.workspace.application, { exact: true })).toHaveCount(0);
  await page.reload();
  await expect(published.getByText(next.workspace.application, { exact: true })).toBeVisible();
  expect(scopes.slice(switchedAt).every((read) => read.workspace === next.workspace.id)).toBe(true);
  navigation.push({ stage: 'different-person-reloaded', url: page.url() });
  await info.attach('home-second-person', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(published.getByText(next.workspace.application, { exact: true })).toBeVisible();
  await info.attach('home-second-person-phone', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await Promise.all(bodies);
  expect(leaked).toEqual([]);
  expect(unreadable).toEqual([]);
  expect(outside).toEqual([]);
  await expect(page.getByText(data.foreign.marker, { exact: false })).toHaveCount(0);
  const proof = {
    refused_request_ids: [problem.request_id, aliasProblem.request_id],
    navigation,
    scopes,
    outside,
  };
  const bytes = JSON.stringify(proof, null, 2);
  await writeFile(join(dirname(fixture), 'scope-proof.json'), bytes, { mode: 0o600 });
  await info.attach('portal-scope-proof', { body: bytes, contentType: 'application/json' });
});
