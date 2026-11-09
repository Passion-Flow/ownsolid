import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expectAccessible, mockControlPlane, node, teamWorkspace } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

const tokenPath = '**/admin/node-join-tokens';
const csv = (rows: string[]) => ['node_name,rack', ...rows].join('\r\n');
const upload = async (page: Page, content: string, label = english['nodes.bulk.file']) => {
  const [chooser] = await Promise.all([
    page.waitForEvent('filechooser'),
    page.getByRole('button', { name: label, exact: true }).click(),
  ]);
  await chooser.setFiles({
    name: 'node-inventory.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(content),
  });
};

const enter = async (page: Page) => {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  return control;
};

for (const locale of ['en', 'zh-CN'] as const) {
  const strings = locale === 'en' ? english : chinese;
  test(`48 rows follow their token receipts and rack totals (${locale})`, async ({
    page,
  }, info) => {
    const control = await enter(page);
    control.preferences.locale = locale;
    const rows = Array.from({ length: 48 }, (_, index) => {
      const rack = index < 24 ? '"Rack A, north"' : 'Rack B';
      return `batch-${String(index + 1).padStart(2, '0')},${rack}`;
    });
    await page.goto('/nodes');
    const entry = page.getByRole('link', { name: strings['nodes.bulk.open'] });
    await expect(entry).toBeVisible();
    await info.attach(`nodes-csv-entry-${locale}`, {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png',
    });
    await entry.click();
    await expect(page).toHaveURL(/\/fleet\/enroll$/);
    await expect(page.getByText(strings['nodes.bulk.empty'], { exact: true })).toBeVisible();
    await info.attach(`bulk-enrollment-empty-${locale}`, {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png',
    });
    await upload(page, '\uFEFF' + csv(rows), strings['nodes.bulk.file']);
    await expect(
      page.getByText(strings['nodes.bulk.preview'].replace('{count}', '48')),
    ).toBeVisible();
    await page.getByLabel(strings['nodes.bulk.filter'], { exact: true }).selectOption('consumed');
    await expect(page.getByText(strings['nodes.bulk.emptyFilter'], { exact: true })).toBeVisible();
    await page.getByRole('button', { name: strings['nodes.bulk.clearFilter'] }).click();
    await page.getByRole('button', { name: strings['nodes.bulk.issue'], exact: true }).click();
    await expect.poll(() => control.joinTokens.length).toBe(48);

    const token = control.joinTokens.find((item) => item.node_name === 'batch-01');
    if (!token) throw new Error('expected receipt for batch-01');
    const first = page.getByRole('listitem').filter({ hasText: 'batch-01' });
    await expect(first).toContainText(strings['nodes.bulk.state.pending']);

    // Neither a matching node name nor a receipt with the wrong workspace proves this node joined.
    control.nodes = [{ ...node, name: token.node_name }];
    const tokenIndex = control.joinTokens.indexOf(token);
    control.joinTokens[tokenIndex] = {
      ...token,
      status: 'consumed',
      node_id: node.id,
      workspace_id: '6f1c7a1e-0000-4000-8000-000000000099',
    };
    await expect(page.getByText(strings['nodes.bulk.unverified'], { exact: true })).toBeVisible({
      timeout: 12_000,
    });
    await expect(first).toContainText(strings['nodes.bulk.state.pending']);

    control.joinTokens[tokenIndex] = { ...token, status: 'consumed', node_id: node.id };
    await expect(first).toContainText(strings['nodes.bulk.state.consumed'], { timeout: 12_000 });
    await expect(first.getByRole('link', { name: strings['nodes.bulk.openNode'] })).toHaveAttribute(
      'href',
      `/nodes/${node.id}`,
    );
    const rack = page
      .getByRole('listitem')
      .filter({ has: page.getByText('Rack A, north', { exact: true }) });
    await expect(rack).toContainText(
      strings['nodes.bulk.rackCount']
        .replace('{joined}', '1')
        .replace('{total}', '24')
        .replace('{pending}', '23'),
    );
    await expectAccessible(page);
    // Avoid capturing any token value in the evidence.
    await page.getByLabel(strings['nodes.bulk.filter'], { exact: true }).selectOption('consumed');
    await info.attach(`bulk-enrollment-${locale}`, {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png',
    });
    await page.reload();
    await expect(first).toContainText(strings['nodes.bulk.state.consumed']);
    const second = page.getByRole('listitem').filter({ hasText: 'batch-02' });
    await expect(second).toContainText(strings['nodes.bulk.secretUnavailable']);
    expect(await page.evaluate(() => Object.values(sessionStorage).join(' '))).not.toContain(
      'scripted-join-token',
    );
  });
}

test('rejects duplicates and a CSV workspace override before any POST', async ({ page }) => {
  const control = await enter(page);
  await page.goto('/fleet/enroll');
  await upload(page, csv(['node-1,Rack A', 'NODE-1,Rack B']));
  await expect(page.getByText('This name appears more than once.', { exact: false })).toHaveCount(
    2,
  );
  await expect(page.getByRole('button', { name: 'Issue tokens', exact: true })).toBeDisabled();
  await upload(page, 'node_name,rack,workspace_id\nnode-1,,other');
  await expect(page.getByText('CSV could not be imported', { exact: true })).toBeVisible();
  expect(control.joinTokens).toHaveLength(0);
});

test('stops after the three in-flight requests and resumes the remaining rows once', async ({
  page,
}) => {
  const control = await enter(page);
  let release!: () => void;
  const paused = new Promise<void>((resolve) => {
    release = resolve;
  });
  let calls = 0;
  await page.route(tokenPath, async (route) => {
    calls++;
    await paused;
    await route.fallback();
  });
  await page.goto('/fleet/enroll');
  await upload(page, csv(Array.from({ length: 8 }, (_, i) => `stop-${i},`)));
  await page.getByRole('button', { name: 'Issue tokens', exact: true }).click();
  await expect.poll(() => calls).toBe(3);
  await page.getByRole('button', { name: 'Stop issuing' }).click();
  release();
  const resume = page.getByRole('button', { name: 'Issue remaining tokens' });
  await expect(resume).toBeEnabled();
  expect(calls).toBe(3);
  expect(control.joinTokens).toHaveLength(3);
  await resume.click();
  await expect.poll(() => control.joinTokens.length).toBe(8);
  expect(new Set(control.joinTokens.map((item) => item.node_name)).size).toBe(8);
});

test('reports real permission and workspace refusals on their rows', async ({ page }) => {
  const control = await enter(page);
  let requestWorkspace = '';
  await page.route(tokenPath, (route) => {
    const input = route.request().postDataJSON() as { workspace_id: string; node_name: string };
    requestWorkspace = input.workspace_id;
    const denied = input.node_name === 'restricted';
    return route.fulfill({
      status: denied ? 403 : 404,
      json: { code: denied ? 'ACCESS_DENIED' : 'RESOURCE_NOT_FOUND', request_id: 'test-request' },
    });
  });
  await page.goto('/fleet/enroll');
  await page
    .getByRole('main')
    .getByLabel('Workspace', { exact: true })
    .selectOption(teamWorkspace.id);
  await upload(page, csv(['restricted,', 'workspace-unavailable,']));
  await page.getByRole('button', { name: 'Issue tokens', exact: true }).click();
  await expect(page.getByText('Control refused enrollment')).toBeVisible();
  await expect(page.getByRole('listitem').filter({ hasText: 'restricted' })).toContainText(
    'must grant enrollment access',
  );
  await expect(
    page.getByRole('listitem').filter({ hasText: 'workspace-unavailable' }),
  ).toContainText('could not find the workspace');
  expect(requestWorkspace).toBe(teamWorkspace.id);
  expect(control.joinTokens).toHaveLength(0);
});

test('retries only a confirmed throttled request and preserves an uncertain outcome', async ({
  page,
}) => {
  const control = await enter(page);
  const attempted: string[] = [];
  await page.route(tokenPath, async (route) => {
    const { node_name: name } = route.request().postDataJSON() as { node_name: string };
    const first = !attempted.includes(name);
    attempted.push(name);
    if (name === 'lost-reply') return route.abort('connectionreset');
    if (name === 'throttled' && first) {
      return route.fulfill({
        status: 429,
        headers: { 'Retry-After': '2' },
        json: { code: 'RATE_LIMITED', request_id: 'test-throttle' },
      });
    }
    return route.fallback();
  });
  await page.goto('/fleet/enroll');
  await upload(page, csv(['lost-reply,', 'throttled,', 'success,']));
  await page.getByRole('button', { name: 'Issue tokens', exact: true }).click();
  await expect(page.getByText('Issuance unconfirmed')).toBeVisible();
  const retry = page.getByRole('button', { name: 'Retry eligible rows' });
  await expect(retry).toBeDisabled();
  await expect(retry).toBeEnabled({ timeout: 5_000 });
  await retry.click();
  await expect.poll(() => control.joinTokens.length).toBe(2);
  expect(attempted.filter((name) => name === 'lost-reply')).toHaveLength(1);
  expect(attempted.filter((name) => name === 'throttled')).toHaveLength(2);
  expect(attempted.filter((name) => name === 'success')).toHaveLength(1);
});

test('revokes an individual unused token after typing its node name', async ({ page }, info) => {
  const control = await enter(page);
  await page.goto('/fleet/enroll');
  await upload(page, csv(['retire-this-token,']));
  await page.getByRole('button', { name: 'Issue tokens', exact: true }).click();
  await page.getByRole('button', { name: 'Revoke the token for retire-this-token' }).click();
  const dialog = page.getByRole('dialog', { name: 'Revoke the token for retire-this-token' });
  const confirm = dialog.getByRole('button', { name: 'Revoke', exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole('textbox').fill('different-name');
  await expect(confirm).toBeDisabled();
  await dialog.getByRole('textbox').fill('retire-this-token');
  await expect(confirm).toBeEnabled();
  await expectAccessible(page);
  // A scripted token is hidden before capturing this surface.
  await info.attach('bulk-revoke-confirmation', {
    body: await dialog.screenshot(),
    contentType: 'image/png',
  });
  await confirm.click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('listitem').filter({ hasText: 'retire-this-token' })).toContainText(
    'Revoked',
  );
  expect(control.joinTokens).toHaveLength(0);
});
