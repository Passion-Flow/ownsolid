// Downloads a member's paged gateway history and an activated alias's public CA from the
// real Portal dispatcher. The fixture describes its known rows, issuer and signed endpoint.
import { createHash, X509Certificate } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { get } from 'node:https';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Locator, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_KEY_DOWNLOADS_FILE ?? '';
const adminOrigin = process.env.OWNSOLID_LIVE_DOWNLOADS_ADMIN_ORIGIN ?? '';

test.skip(origin === '' || fixture === '', 'needs a signed gateway and scoped history fixture');
test.use({ ...liveOptions, trace: 'off' });

interface DownloadFixture {
  email: string;
  password: string;
  workspace: string;
  other_workspace: string;
  key_name: string;
  key_id: string;
  request_ids: string[];
  other_key_id: string;
  alias_id: string;
  alias_name: string;
  deployment_id: string;
  gateway_origin: string;
  ca_fingerprint: string;
  ca_digest: string;
}

const base = '/api/v1/sites/default/cells/default/workspaces';
const scope = (workspace: string) => `${base}/${workspace}/distribution`;
const sha256 = (bytes: Buffer) => createHash('sha256').update(bytes).digest('hex');

async function restrictBrowserOrigin(page: Page, address: string): Promise<string[]> {
  const expected = new URL(address).origin;
  const foreign: string[] = [];
  await page.route(
    (url) => (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== expected,
    async (route) => {
      foreign.push(new URL(route.request().url()).origin);
      await route.abort();
    },
  );
  return foreign;
}

async function signIn(
  page: Page,
  path: string,
  address: string,
  password: string,
  heading: string,
) {
  await page.goto(path);
  await page.getByLabel('Email').fill(address);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: heading, level: 1 })).toBeVisible();
}

async function downloadFrom(
  page: Page,
  button: Locator,
  path: string,
  name: string,
  media: string,
) {
  const [response, download] = await Promise.all([
    page.waitForResponse((reply) => new URL(reply.url()).pathname === path && reply.ok()),
    page.waitForEvent('download'),
    button.click(),
  ]);
  expect(download.suggestedFilename()).toBe(name);
  const bytes = await readFile(await download.path());
  const headers = await response.allHeaders();
  expect(headers['content-type']).toBe(media);
  expect(headers['content-length']).toBe(String(bytes.length));
  expect(headers['x-artifact-sha256']).toBe(sha256(bytes));
  expect(headers['cache-control']).toBe('no-store');
  return { bytes, query: new URL(response.url()).searchParams };
}

function csvIds(bytes: Buffer): string[] {
  const [header, ...rows] = bytes.toString('utf8').split('\r\n');
  expect(header).toBe(
    'request_id,admitted_at,request_state,outcome_status,request_count,elapsed_ms,prompt_tokens,completion_tokens,total_tokens',
  );
  expect(rows.pop()).toBe('');
  return rows.map((row) => row.split(',')[0] ?? '');
}

function signedEndpointStatus(endpoint: string, ca: Buffer): Promise<number | undefined> {
  return new Promise((resolve, reject) => {
    get(endpoint, { ca, signal: AbortSignal.timeout(5_000) }, (response) => {
      response.resume();
      response.once('end', () => {
        resolve(response.statusCode);
      });
    }).once('error', reject);
  });
}

async function verifyGatewayCA(page: Page, button: Locator, path: string, data: DownloadFixture) {
  const { bytes } = await downloadFrom(
    page,
    button,
    path,
    'model-gateway-ca.pem',
    'application/octet-stream',
  );
  const certificate = new X509Certificate(bytes);
  expect(certificate.ca).toBe(true);
  expect(certificate.fingerprint256.replaceAll(':', '').toLowerCase()).toBe(data.ca_fingerprint);
  expect(sha256(bytes)).toBe(data.ca_digest);
  expect(await signedEndpointStatus(data.gateway_origin, bytes)).toBe(204);
  return bytes;
}

test('downloads scoped CSV pages and verifies the gateway CA', async ({ page }, info) => {
  test.setTimeout(120_000);
  const data = JSON.parse(await readFile(fixture, 'utf8')) as DownloadFixture;
  const foreignOrigins = await restrictBrowserOrigin(page, origin);
  const distribution = scope(data.workspace);
  const history = `${distribution}/requests`;
  const exportPath = `${history}/export`;
  const caPath = `${distribution}/aliases/${data.alias_id}/ca`;
  const csvType = 'text/csv; charset=utf-8';

  await signIn(page, '/keys', data.email, data.password, 'My API keys');
  const initialHistory = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.pathname === history && url.searchParams.get('api_key_id') === data.key_id;
  });
  await page.getByRole('button', { name: `Show ${data.key_name}` }).click();
  const drawer = page.getByRole('dialog', { name: data.key_name });
  const { data: firstPage } = (await (await initialHistory).json()) as {
    data: { next_cursor: string | null };
  };
  if (!firstPage.next_cursor) throw new Error('The first history page needs a cursor');
  const csv = drawer.getByRole('button', { name: 'Download CSV' });
  const first = await downloadFrom(page, csv, exportPath, 'gateway-usage.csv', csvType);
  expect(first.query.get('api_key_id')).toBe(data.key_id);
  expect(first.query.get('limit')).toBe('2');
  expect(first.query.has('cursor')).toBe(false);
  expect(csvIds(first.bytes)).toEqual(data.request_ids.slice(0, 2));

  await drawer.getByRole('button', { name: 'Load more' }).click();
  await drawer.getByLabel('CSV page').selectOption({ label: 'Page 2' });
  const second = await downloadFrom(page, csv, exportPath, 'gateway-usage.csv', csvType);
  expect(second.query.get('api_key_id')).toBe(data.key_id);
  expect(second.query.get('limit')).toBe('2');
  expect(second.query.get('cursor')).toBe(firstPage.next_cursor);
  expect(csvIds(second.bytes)).toEqual(data.request_ids.slice(2));
  await csv.scrollIntoViewIfNeeded();
  await info.attach('portal-key-csv-pages', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await info.attach('gateway-usage-first-page', { body: first.bytes, contentType: csvType });
  await info.attach('gateway-usage-second-page', { body: second.bytes, contentType: csvType });

  const caButton = drawer.getByRole('button', { name: 'Download gateway CA' });
  const ca = await verifyGatewayCA(page, caButton, caPath, data);
  await caButton.scrollIntoViewIfNeeded();
  await info.attach('portal-gateway-ca-action', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await info.attach('model-gateway-ca.pem', {
    body: ca,
    contentType: 'application/octet-stream',
  });
  await expectAccessible(page);

  const foreign = await page.evaluate(
    async (paths) => {
      return Promise.all(
        paths.map(async (path) => {
          const response = await fetch(path);
          return {
            status: response.status,
            disposition: response.headers.get('content-disposition'),
            body: await response.text(),
          };
        }),
      );
    },
    [
      `${exportPath}?limit=2&api_key_id=${data.other_key_id}`,
      `${scope(data.other_workspace)}/requests/export?limit=2&api_key_id=${data.key_id}`,
      `${scope(data.other_workspace)}/aliases/${data.alias_id}/ca`,
    ],
  );
  expect(foreign[0]?.status).toBe(200);
  expect(csvIds(Buffer.from(foreign[0]?.body ?? ''))).toEqual([]);
  for (const response of foreign.slice(1)) {
    expect([403, 404]).toContain(response.status);
    expect(response.disposition).toBeNull();
  }

  let unexpectedDownloads = 0;
  page.on('download', () => {
    unexpectedDownloads++;
  });
  const csvRoute = (url: URL) => url.pathname === exportPath;
  await page.route(csvRoute, (route) => {
    const url = new URL(route.request().url());
    url.searchParams.set('api_key_id', data.other_key_id);
    return route.continue({ url: url.href });
  });
  const invalidCursor = page.waitForResponse(
    (response) => csvRoute(new URL(response.url())) && response.status() === 400,
  );
  await csv.click();
  const refused = await invalidCursor;
  expect(await refused.json()).toMatchObject({ code: 'INVALID_CURSOR' });
  expect(await refused.headerValue('content-disposition')).toBeNull();
  await expect(drawer.getByText('This page of results is out of date')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Reload' })).toBeVisible();
  expect(unexpectedDownloads).toBe(0);
  await page.unroute(csvRoute);

  const caRoute = (url: URL) => url.pathname === caPath;
  await page.route(caRoute, (route) => {
    const url = new URL(route.request().url());
    url.pathname = `${scope(data.other_workspace)}/aliases/${data.alias_id}/ca`;
    return route.continue({ url: url.href });
  });
  const deniedCa = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.pathname.endsWith(`/aliases/${data.alias_id}/ca`) && response.status() === 404;
  });
  await caButton.click();
  const caProblem = await deniedCa;
  expect(await caProblem.json()).toMatchObject({ code: 'RESOURCE_NOT_FOUND' });
  expect(await caProblem.headerValue('content-disposition')).toBeNull();
  await expect(drawer.getByText('Gateway CA is unavailable')).toBeVisible();
  expect(unexpectedDownloads).toBe(0);
  await info.attach('portal-gateway-ca-refusal', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await page.goto('/models');
  await page.getByRole('button', { name: `Show ${data.alias_name}` }).click();
  const model = page.getByRole('dialog', { name: data.alias_name });
  const modelCA = model.getByRole('button', { name: 'Download gateway CA' });
  await expect(model.getByText(`${data.gateway_origin}/v1`, { exact: true })).toBeVisible();
  await modelCA.click();
  await expect(model.getByText('Gateway CA is unavailable')).toBeVisible();
  await expect(model.getByText(`${data.gateway_origin}/v1`, { exact: true })).toBeVisible();
  expect(unexpectedDownloads).toBe(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await modelCA.scrollIntoViewIfNeeded();
  await info.attach('portal-model-endpoint-ca-refusal', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.unroute(caRoute);
  expect(await verifyGatewayCA(page, modelCA, caPath, data)).toEqual(ca);
  await expect(model.getByText('Gateway CA is unavailable')).toHaveCount(0);
  await expectAccessible(page);
  await info.attach('portal-model-endpoint-ca', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
  expect(foreignOrigins).toEqual([]);
});

test('downloads the selected key page and CA from the Admin drawer', async ({ browser }, info) => {
  test.skip(adminOrigin === '', 'needs the Admin origin for the same installation');
  const data = JSON.parse(await readFile(fixture, 'utf8')) as DownloadFixture;
  const context = await browser.newContext({
    ...liveOptions,
    baseURL: adminOrigin,
    viewport: { width: 1280, height: 900 },
  });
  try {
    const page = await context.newPage();
    const foreignOrigins = await restrictBrowserOrigin(page, adminOrigin);
    await signIn(page, '/gateway', email, adminPassword(), 'Gateway and keys');
    await page.getByRole('button', { name: `Show ${data.key_name}` }).click();
    const drawer = page.getByRole('dialog', { name: data.key_name });
    const distribution = `/api/v1/sites/default/cells/default/admin/distribution/workspaces/${data.workspace}`;
    const csvButton = drawer.getByRole('button', { name: 'Download CSV' });
    const csv = await downloadFrom(
      page,
      csvButton,
      `${distribution}/requests/export`,
      'gateway-usage.csv',
      'text/csv; charset=utf-8',
    );
    expect(csv.query.get('api_key_id')).toBe(data.key_id);
    expect(csvIds(csv.bytes)).toEqual(data.request_ids.slice(0, 2));
    const caButton = drawer.getByRole('button', { name: 'Download gateway CA' });
    const ca = await verifyGatewayCA(
      page,
      caButton,
      `${distribution}/aliases/${data.alias_id}/ca`,
      data,
    );
    await expectAccessible(page);
    await csvButton.scrollIntoViewIfNeeded();
    await info.attach('admin-gateway-downloads', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    await page.goto(`/services/${data.deployment_id}`);
    const deployment = page.getByRole('region', { name: 'On the gateway' });
    const deploymentCA = deployment.getByRole('button', { name: 'Download gateway CA' });
    await expect(deployment.getByText(`${data.gateway_origin}/v1`, { exact: true })).toBeVisible();
    const scale = page.getByRole('button', { name: 'Scale', exact: true });
    await scale.scrollIntoViewIfNeeded();
    await info.attach('admin-deployment-overview', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    expect(
      await verifyGatewayCA(
        page,
        deploymentCA,
        `${distribution}/aliases/${data.alias_id}/ca`,
        data,
      ),
    ).toEqual(ca);
    await expectAccessible(page);
    await deploymentCA.scrollIntoViewIfNeeded();
    await info.attach('admin-deployment-endpoint-ca', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    expect(foreignOrigins).toEqual([]);
  } finally {
    await context.close();
  }
});
