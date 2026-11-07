// A node on its own page: reached from the drawer or by a link that names it, with tabs for what it is,
// what it has run and what people did to it.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  disabledNode,
  expectAccessible,
  finishedTask,
  mockControlPlane,
  node,
  systemEvent,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [node, disabledNode];
});

const path = `/nodes/${node.id}`;
const title = (page: Page) => page.getByRole('heading', { name: node.name, level: 1 });
const tab = (page: Page, name: string) => page.getByRole('tab', { name });

test('opens from the drawer, and the sidebar still marks Nodes', async ({ page }) => {
  await page.goto(`/nodes?open=${node.id}`);
  await page.getByRole('dialog').getByRole('link', { name: 'Open the full page' }).click();
  await expect(page).toHaveURL(new RegExp(`${path}$`));
  await expect(title(page)).toBeVisible();
  await expect(
    page.getByRole('navigation').getByRole('link', { name: 'Nodes', exact: true }),
  ).toHaveAttribute('aria-current', 'page');
  await expect(page.getByText(node.facts.hostname)).toBeVisible();
  await expectAccessible(page);
});

test('its tabs work from the keyboard and live in the address', async ({ page }) => {
  await page.goto(path);
  const overview = tab(page, 'Overview');
  await expect(overview).toHaveAttribute('aria-selected', 'true');
  await overview.focus();
  await page.keyboard.press('ArrowRight');
  await expect(tab(page, 'Tasks')).toBeFocused();
  await expect(tab(page, 'Tasks')).toHaveAttribute('aria-selected', 'true');
  await expect(page).toHaveURL(/tab=tasks$/);
  await page.keyboard.press('End');
  await expect(tab(page, 'Activity')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowRight');
  await expect(overview).toBeFocused();
  await expect(page).toHaveURL(new RegExp(`${path}$`));

  await page.goto(`${path}?tab=activity`);
  await expect(tab(page, 'Activity')).toHaveAttribute('aria-selected', 'true');
  await page.goto(`${path}?tab=nonsense`);
  await expect(overview).toHaveAttribute('aria-selected', 'true');
});

test('Tasks lists this node and links each task', async ({ page }) => {
  control.tasks = [finishedTask];
  await page.goto(`${path}?tab=tasks`);
  await page.getByRole('tabpanel').getByRole('link', { name: 'Collect node facts' }).click();
  await expect(page).toHaveURL(new RegExp(`/tasks/${finishedTask.task.id}$`));
});

test('Tasks says when there are none, and a failure can be retried', async ({ page }) => {
  await page.goto(`${path}?tab=tasks`);
  await expect(page.getByText('No tasks yet')).toBeVisible();
  await expectAccessible(page);

  await page.route('**/admin/nodes/*/tasks?*', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/problem+json',
      body: JSON.stringify({ code: 'INTERNAL_ERROR', title: 'x', status: 503 }),
    }),
  );
  await page.reload();
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  await page.unroute('**/admin/nodes/*/tasks?*');
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByText('No tasks yet')).toBeVisible();
});

test('Activity shows what people did to this node over its whole life', async ({ page }) => {
  const mine = {
    ...systemEvent,
    id: '6f1c7a1e-0000-4000-8000-0000000000e1',
    occurred_at: node.created_at,
    target_id: node.id,
  };
  control.auditEvents = [mine, { ...systemEvent, id: '6f1c7a1e-0000-4000-8000-0000000000e2' }];
  await page.goto(`${path}?tab=activity`);
  const rows = page.getByRole('tabpanel').getByRole('row');
  await expect(rows).toHaveCount(2);
  const asked = control.auditQueries.at(-1);
  expect(asked?.get('target_id')).toBe(node.id);
  expect(Date.parse(asked?.get('from') ?? '')).toBeLessThanOrEqual(Date.parse(node.created_at));
  await expectAccessible(page);
  await rows.nth(1).getByRole('link').click();
  await expect(page).toHaveURL(new RegExp(`/audit\\?.*open=${mine.id}`));
  await expect(page.getByRole('dialog')).toBeVisible();
});

test('Activity says when there is none, and a failure can be retried', async ({ page }) => {
  control.auditEvents = [];
  await page.goto(`${path}?tab=activity`);
  await expect(page.getByText('No activity yet')).toBeVisible();
  control.auditProblem = { status: 503, code: 'INTERNAL_ERROR' };
  await page.reload();
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.auditProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByText('No activity yet')).toBeVisible();
});

test('renaming stays on the page and can be undone', async ({ page }) => {
  await page.goto(path);
  await page.getByRole('button', { name: 'Rename' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('textbox').fill('gpu-renamed');
  await dialog.getByRole('button', { name: 'Rename' }).click();
  await expect(page.getByRole('heading', { name: 'gpu-renamed', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(title(page)).toBeVisible();
});

test('removing goes back to the list', async ({ page }) => {
  await page.goto(path);
  await page.getByRole('main').getByRole('button', { name: 'Remove' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove this node' });
  await dialog.getByRole('textbox').first().fill(node.name);
  await dialog.getByRole('textbox').last().fill('Retired');
  await dialog.getByRole('button', { name: 'Remove the node' }).click();
  await expect(page).toHaveURL(/\/nodes$/);
  await expect(page.getByRole('status').filter({ hasText: 'removed' })).toBeVisible();
});

test('a node that does not exist says so, and a failure can be retried', async ({ page }) => {
  await page.goto('/nodes/6f1c7a1e-0000-4000-8000-ffffffffffff');
  await expect(
    page.getByRole('heading', { name: 'This node could not be found', level: 1 }),
  ).toBeVisible();
  await expect(page.getByRole('link', { name: 'All nodes' })).toBeVisible();
  await expectAccessible(page);

  let failing = true;
  await page.route(/\/admin\/nodes\/[0-9a-f-]{36}$/, (route) =>
    failing
      ? route.fulfill({
          status: 503,
          contentType: 'application/problem+json',
          body: JSON.stringify({ code: 'INTERNAL_ERROR', title: 'x', status: 503 }),
        })
      : route.fallback(),
  );
  await page.goto(path);
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  failing = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(title(page)).toBeVisible();
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('names its tabs', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto(path);
    await expect(tab(page, chinese['nodes.page.tab.activity'])).toBeVisible();
  });
});
