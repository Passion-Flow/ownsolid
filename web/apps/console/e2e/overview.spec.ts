// The overview: an administrator lands on what to do, how the fleet stands, what needs attention with
// a link to the fix, and what is left to set up. Each source fails on its own.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  activeLicense,
  activeSummary,
  adminUser,
  disabledNode,
  failedMail,
  failedTask,
  memberUser,
  missingLicense,
  mockControlPlane,
  node,
  offlineNode,
  pendingInvitation,
  runningTask,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [node];
  control.users = [adminUser, memberUser];
});

const justNow = () => new Date().toISOString();
const hoursAgo = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString();
const attention = (page: Page) =>
  page.locator('section').filter({ has: page.getByRole('heading', { name: 'Needs attention' }) });

test('is the first page and says when nothing needs you', async ({ page }) => {
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Welcome, Ada Lovelace', level: 1 }),
  ).toBeVisible();
  await expect(page.getByText('nothing needs you', { exact: false }).first()).toBeVisible();
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Finish setup' })).toBeHidden();
  await expect(page).toHaveTitle(/Overview/);
});

test('names an offline node and opens its drawer', async ({ page }) => {
  control.nodes = [node, offlineNode];
  await page.goto('/');
  await expect(page.getByText('1 item needs your attention.')).toBeVisible();
  await attention(page).getByRole('link', { name: 'Open the node' }).click();
  await expect(page).toHaveURL(new RegExp(`/nodes\\?open=${offlineNode.id}$`));
  await expect(page.getByRole('dialog', { name: 'gpu-a03' })).toBeVisible();
});

test('leaves a node disabled on purpose alone', async ({ page }) => {
  control.nodes = [node, { ...offlineNode, status: 'disabled' }];
  await page.goto('/');
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeVisible();
  await expect(page.getByText('A disabled node also counts')).toBeVisible();
});

test('names a few offline nodes, and links to the rest', async ({ page }) => {
  control.nodes = [0, 1, 2, 3, 4].map((index) => ({
    ...offlineNode,
    id: `6f1c7a1e-0000-4000-8000-0000000001${String(index).padStart(2, '0')}`,
    name: `gpu-b${String(index)}`,
  }));
  await page.goto('/');
  await expect(attention(page).getByText('last reported')).toHaveCount(3);
  await expect(page.getByText('More nodes are offline')).toBeVisible();
  await page.getByRole('link', { name: 'See offline nodes' }).click();
  await expect(page).toHaveURL(/\/nodes\?connectivity=offline&status=active$/);
  await expect(page.getByRole('row', { name: /gpu-b4/ })).toBeVisible();
});

test('links a recent failed task to the failed tasks', async ({ page }) => {
  control.tasks = [{ ...failedTask, task: { ...failedTask.task, completed_at: justNow() } }];
  await page.goto('/');
  await expect(page.getByText('1 task failed in the last 24 hours').first()).toBeVisible();
  await expect(attention(page).getByText('Its log says why.')).toBeVisible();
  await page.getByRole('link', { name: 'See failed tasks' }).click();
  await expect(page).toHaveURL(/\/tasks\?state=failed$/);
});

test('links a recent failed email to the failed emails', async ({ page }) => {
  control.mail = [{ ...failedMail, completed_at: justNow() }];
  await page.goto('/');
  await expect(page.getByText('1 email failed in the last 24 hours')).toBeVisible();
  await page.getByRole('link', { name: 'See failed emails' }).click();
  await expect(page).toHaveURL(/\/mail\?state=failed$/);
});

test('lets old failures go, so the list can clear', async ({ page }) => {
  control.tasks = [{ ...failedTask, task: { ...failedTask.task, completed_at: hoursAgo(25) } }];
  control.mail = [{ ...failedMail, completed_at: hoursAgo(25) }];
  await page.goto('/');
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeVisible();
  await expect(page.getByText('None failed in the last 24 hours')).toBeVisible();
});

test('puts what stops work first, and a coming renewal last', async ({ page }) => {
  control.license = {
    ...activeLicense,
    current: { ...activeSummary, expires_at: '2027-01-06T08:00:00Z' },
  };
  control.nodes = [offlineNode];
  control.mail = [{ ...failedMail, completed_at: justNow() }];
  await page.goto('/');
  const titles = attention(page).getByRole('listitem');
  await expect(titles).toHaveCount(3);
  await expect(titles.nth(0)).toContainText('gpu-a03');
  await expect(titles.nth(1)).toContainText('email failed');
  await expect(titles.nth(2)).toContainText('License renewal due in 60 days');
  await titles.nth(2).getByRole('link', { name: 'Open the license' }).click();
  await expect(page.getByRole('heading', { name: 'License', level: 1 })).toBeVisible();
});

test('leads with a license that refuses work', async ({ page }) => {
  control.license = {
    ...activeLicense,
    state: 'expired',
    new_business_allowed: false,
    warning: true,
  };
  control.nodes = [offlineNode];
  await page.goto('/');
  await expect(attention(page).getByRole('listitem').first()).toContainText(
    'The license has expired',
  );
});

test('shows the fleet by state, each a link to its list', async ({ page }) => {
  control.nodes = [node, offlineNode, disabledNode];
  await page.goto('/');
  const fleet = page
    .locator('section')
    .filter({ has: page.getByRole('heading', { name: 'Fleet health' }) });
  await expect(fleet.getByRole('link')).toHaveCount(4);
  await fleet.getByRole('link', { name: /^Offline/ }).click();
  await expect(page).toHaveURL(/\/nodes\?connectivity=offline$/);
  await expect(page.getByRole('row', { name: /gpu-a03/ })).toBeVisible();
});

test('counts nodes, running tasks and the license on the tiles', async ({ page }) => {
  control.nodes = [node, offlineNode];
  control.tasks = [runningTask];
  await page.goto('/');
  await expect(
    page.getByRole('link', { name: /^Nodes online\s*1\s*of 2\s*1 offline/ }),
  ).toBeVisible();
  await expect(page.getByRole('link', { name: /^Tasks running\s*1/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /^License\s*Valid\s*To / }).first()).toBeVisible();
});

test('a new installation is walked through setup, from real state', async ({ page }) => {
  control.license = missingLicense;
  control.nodes = [];
  control.users = [adminUser];
  await page.goto('/');
  const setup = page
    .locator('section')
    .filter({ has: page.getByRole('heading', { name: 'Finish setup' }) });
  await expect(setup.getByText('1 of 4 done')).toBeVisible();
  await expect(setup.getByRole('link')).toHaveCount(3);
  await expect(page.getByText('No nodes yet').first()).toBeVisible();

  control.license = activeLicense;
  control.nodes = [node];
  await page.reload();
  await expect(setup.getByText('3 of 4 done')).toBeVisible();
  await expect(setup.getByRole('link')).toHaveCount(1);

  // A waiting invitation counts: the person has done their part.
  control.invitations = [pendingInvitation];
  await page.reload();
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeVisible();
  await expect(setup).toBeHidden();
});

test('quick actions open the dialog they name, once', async ({ page }) => {
  await page.goto('/');
  await page
    .getByRole('link', { name: /^Add a node/ })
    .first()
    .click();
  const add = page.getByRole('dialog', { name: 'Add a node' });
  await expect(add).toBeVisible();
  await expect(page).toHaveURL(/\/nodes$/);
  await expect(add.locator(':focus')).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(add).toBeHidden();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Nodes', level: 1 })).toBeVisible();
  await expect(add).toBeHidden();

  await page.goto('/');
  await page.getByRole('link', { name: /^Invite someone/ }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page).toHaveURL(/\/users$/);
  await page.keyboard.press('Escape');

  await page.goto('/');
  await page
    .getByRole('link', { name: /^Import a license/ })
    .first()
    .click();
  await expect(page.getByRole('button', { name: 'Choose a file' })).toBeVisible();

  await page.goto('/');
  await page.getByRole('link', { name: /^See tasks/ }).click();
  await expect(page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeVisible();
});

test('one source failing leaves the others and offers a retry', async ({ page }) => {
  control.nodes = [offlineNode];
  control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/');
  await expect(attention(page).getByText('Could not check:')).toBeVisible();
  await expect(attention(page).getByText('Email', { exact: true })).toBeVisible();
  await expect(attention(page).getByText('gpu-a03 last reported')).toBeVisible();
  control.mailProblem = null;
  await attention(page).getByRole('button', { name: 'Try again' }).click();
  await expect(attention(page).getByText('Could not check:')).toBeHidden();
  await expect(attention(page).getByText('gpu-a03 last reported')).toBeVisible();
});

test('does not call a page that could not check clear', async ({ page }) => {
  control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  control.tasksProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  control.licenseProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/');
  // One failure, named once, however many sources it hit.
  await expect(attention(page).getByRole('button', { name: 'Try again' })).toHaveCount(1);
  await expect(attention(page).getByText('Tasks', { exact: true })).toHaveCount(1);
  await expect(page.getByText('Part of this page could not load')).toBeVisible();
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeHidden();
});

test('shows the fleet failure where the fleet goes, with a retry', async ({ page }) => {
  await page.route('**/nodes/summary', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/problem+json',
      body: JSON.stringify({ code: 'STORAGE_UNAVAILABLE', request_id: 'r' }),
    }),
  );
  await page.goto('/');
  const fleet = page
    .locator('section')
    .filter({ has: page.getByRole('heading', { name: 'Fleet health' }) });
  const retry = fleet.getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  await page.unroute('**/nodes/summary');
  await retry.click();
  await expect(fleet.getByRole('link')).toHaveCount(4);
});

test('reads in the background, so an open overview keeps no session alive', async ({ page }) => {
  const foreground: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      /^\/api\/.*(nodes|tasks|mail-deliveries|admin\/users|admin\/invitations|admin\/license)/.test(
        path,
      ) &&
      request.headers()['x-ownsolid-background'] !== 'true'
    ) {
      foreground.push(path);
    }
  });
  await page.goto('/');
  await expect(page.getByText('Nothing needs you', { exact: true })).toBeVisible();
  expect(foreground).toEqual([]);
});

test('keeps what it showed when a later read fails', async ({ page }) => {
  control.nodes = [offlineNode];
  await page.goto('/');
  await expect(attention(page).getByText('gpu-a03 last reported')).toBeVisible();
  await page.route('**/nodes?*', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/problem+json',
      body: JSON.stringify({ code: 'STORAGE_UNAVAILABLE', request_id: 'r' }),
    }),
  );
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.waitForTimeout(300);
  await expect(attention(page).getByText('gpu-a03 last reported')).toBeVisible();
  await expect(attention(page).getByText('Could not check:')).toBeHidden();
});

for (const width of [1440, 1024, 390]) {
  test(`has no sideways scroll at ${String(width)} pixels`, async ({ page }) => {
    control.nodes = [offlineNode];
    control.license = missingLicense;
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/');
    await page.getByText('gpu-a03 last reported').waitFor();
    const fits = await page.evaluate(() => {
      const main = document.querySelector('main');
      return main ? main.scrollWidth <= main.clientWidth : false;
    });
    expect(fits).toBe(true);
  });
}

test('the portal keeps its own start page', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/');
  await expect(page.getByText('Fleet health')).toHaveCount(0);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    control.nodes = [offlineNode];
    await page.goto('/');
    await expect(page.getByRole('heading', { name: chinese['overview.attention'] })).toBeVisible();
    await expect(
      page.getByRole('link', { name: chinese['overview.attention.node.open'] }),
    ).toBeVisible();
  });
});
