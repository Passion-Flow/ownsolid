// The audit trail: an administrator reads who did what and how it ended, narrows it from a link, and
// follows an event to its person or its request.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  failedSignIn,
  licenseEvent,
  mockControlPlane,
  systemEvent,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.auditEvents = [licenseEvent, failedSignIn, systemEvent];
});

const rows = (page: Page) => page.getByRole('row');
const openEvent = (page: Page, action: string) =>
  page.getByRole('button', { name: new RegExp(`^Show details of ${action.replace('.', '\\.')}`) });

test('is reached from the navigation and names people, not IDs', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('navigation').getByRole('link', { name: 'Audit trail' }).click();
  await expect(page.getByRole('heading', { name: 'Audit trail', level: 1 })).toBeFocused();
  const row = rows(page).filter({ hasText: 'license.imported' });
  await expect(row).toContainText('ada@example.test');
  await expect(row).toContainText('platform_license');
  await expect(row).toContainText('Succeeded');
  await expect(rows(page).filter({ hasText: 'auth.login_failed' })).toContainText('Failed');
  await expect(rows(page).filter({ hasText: 'system.retention' })).toContainText('System');
});

test('asks the server for a pinned window and pages with its cursor', async ({ page }) => {
  control.auditEvents = Array.from({ length: 25 }, (_, index) => ({
    ...licenseEvent,
    id: `6f1c7a1e-0000-4000-8000-0000000002${String(index).padStart(2, '0')}`,
  }));
  await page.goto('/audit');
  await expect(rows(page)).toHaveCount(21);
  const first = control.auditQueries.at(-1);
  expect(first?.get('from')).toBeTruthy();
  expect(first?.get('to')).toBeTruthy();
  const span = Date.parse(first?.get('to') ?? '') - Date.parse(first?.get('from') ?? '');
  expect(span).toBe(7 * 86_400_000);
  await page.getByRole('button', { name: 'Load more' }).click();
  await expect(rows(page)).toHaveCount(26);
  const next = control.auditQueries.at(-1);
  expect(next?.get('cursor')).toBe('20');
  expect(next?.get('from')).toBeNull();
  await expect(page.getByRole('button', { name: 'Load more' })).toBeHidden();
});

test('filters by kind and outcome on the server, and from a link', async ({ page }) => {
  await page.goto('/audit');
  await page.getByLabel('Kind of action').selectOption('license');
  await expect(page).toHaveURL(/category=license/);
  await expect(rows(page)).toHaveCount(2);
  expect(control.auditQueries.at(-1)?.get('category')).toBe('license');

  await page.getByLabel('Kind of action').selectOption('');
  await page.getByLabel('Outcome').selectOption('failure');
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).filter({ hasText: 'auth.login_failed' })).toBeVisible();

  await page.goto('/audit?category=auth&outcome=failure');
  await expect(page.getByLabel('Kind of action')).toHaveValue('auth');
  await expect(rows(page).filter({ hasText: 'auth.login_failed' })).toBeVisible();
});

test('widens and narrows the time', async ({ page }) => {
  await page.goto('/audit');
  await page.getByLabel('Time', { exact: true }).selectOption('24h');
  await expect(page).toHaveURL(/range=24h/);
  await expect(rows(page).filter({ hasText: 'system.retention' })).toBeHidden();
  await page.getByLabel('Time', { exact: true }).selectOption('31d');
  await expect(rows(page).filter({ hasText: 'system.retention' })).toBeVisible();
});

test('checks a chosen window before asking', async ({ page }) => {
  await page.goto('/audit');
  await expect(rows(page).filter({ hasText: 'license.imported' })).toBeVisible();
  const asked = control.auditQueries.length;
  await page.getByLabel('Time', { exact: true }).selectOption('custom');
  await expect(page.getByText('Choose when the window starts and ends.')).toBeVisible();
  await page.getByLabel('From', { exact: true }).fill('2026-03-01T00:00');
  await page.getByLabel('To', { exact: true }).fill('2026-01-01T00:00');
  await expect(page.getByText('The window must start before it ends.')).toBeVisible();
  await page.getByLabel('From', { exact: true }).fill('2025-01-01T00:00');
  await expect(page.getByText('A window can cover at most 31 days.')).toBeVisible();
  expect(control.auditQueries).toHaveLength(asked);
  await page.getByLabel('From', { exact: true }).fill('2026-01-01T00:00');
  await page.getByLabel('To', { exact: true }).fill('2026-01-02T00:00');
  await expect(page.getByText('No events match')).toBeVisible();
  const query = control.auditQueries.at(-1);
  expect(Date.parse(query?.get('to') ?? '') - Date.parse(query?.get('from') ?? '')).toBe(
    86_400_000,
  );
  await page.getByLabel('Time', { exact: true }).selectOption('7d');
  await expect(page).toHaveURL(/\/audit$/);
});

test('finds a request by its ID, and explains what is not one', async ({ page }) => {
  await page.goto('/audit');
  const search = page.getByRole('searchbox', { name: 'Find by request ID' });
  await search.fill('nonsense');
  await expect(page.getByText('That is not a request ID.')).toBeVisible();
  await search.fill(failedSignIn.request_id);
  await expect(rows(page)).toHaveCount(2);
  expect(control.auditQueries.at(-1)?.get('request_id')).toBe(failedSignIn.request_id);
});

test('shows an event, and follows it to its person', async ({ page }) => {
  await page.goto('/audit');
  await openEvent(page, 'auth.login_failed').click();
  const drawer = page.getByRole('dialog', { name: 'auth.login_failed' });
  await expect(drawer).toContainText('Portal');
  await expect(drawer).toContainText('Affected account');
  await expect(drawer).toContainText(failedSignIn.request_id);
  await drawer.getByRole('button', { name: "This person's actions" }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`actor=${failedSignIn.actor_user_id ?? ''}`));
  await expect(page).not.toHaveURL(/open=/);
  await expect(page.getByText('Person: grace@example.test')).toBeVisible();
  await expect(rows(page)).toHaveCount(2);
  await page.getByRole('button', { name: "Show everyone's actions" }).click();
  await expect(rows(page)).toHaveCount(4);
});

test('follows an event to its request', async ({ page }) => {
  await page.goto('/audit');
  await openEvent(page, 'license.imported').click();
  const drawer = page.getByRole('dialog', { name: 'license.imported' });
  await expect(drawer).toContainText('Annual renewal');
  await drawer.getByRole('button', { name: 'Everything from this request' }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`request=${licenseEvent.request_id}`));
  await expect(rows(page)).toHaveCount(2);
});

test('an event with no person says the system did it', async ({ page }) => {
  await page.goto('/audit?range=31d');
  await openEvent(page, 'system.retention').click();
  const drawer = page.getByRole('dialog', { name: 'system.retention' });
  await expect(drawer).toContainText('System');
  await expect(drawer.getByRole('button', { name: "This person's actions" })).toBeHidden();
});

test("an event's own link opens it, however much time has passed", async ({ page }) => {
  await page.goto('/audit');
  await openEvent(page, 'license.imported').click();
  const link = await page
    .getByRole('dialog', { name: 'license.imported' })
    .getByText(/\/audit\?range=custom/)
    .textContent();
  await page.goto(new URL(link ?? '').pathname + new URL(link ?? '').search);
  await expect(page.getByRole('dialog', { name: 'license.imported' })).toBeVisible();
  const query = control.auditQueries.at(-1);
  expect(Date.parse(query?.get('to') ?? '') - Date.parse(query?.get('from') ?? '')).toBe(120_000);
});

test('says when a linked event is not in the list', async ({ page }) => {
  await page.goto('/audit?open=6f1c7a1e-0000-4000-8000-0000000000ee');
  await expect(page.getByRole('dialog', { name: 'That event is not in this list' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page).not.toHaveURL(/open=/);
});

test('says when nothing is recorded and when filters hide everything', async ({ page }) => {
  control.auditEvents = [];
  await page.goto('/audit');
  await expect(page.getByText('Nothing recorded in the last 7 days')).toBeVisible();
  await page.getByLabel('Kind of action').selectOption('sso');
  await expect(page.getByText('No events match')).toBeVisible();
  await page.getByRole('button', { name: 'Clear the filters' }).click();
  await expect(page).toHaveURL(/\/audit$/);
});

test('a list that cannot load offers a retry', async ({ page }) => {
  control.auditProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/audit');
  const retry = page.getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  control.auditProblem = null;
  await retry.click();
  await expect(rows(page).filter({ hasText: 'license.imported' })).toBeVisible();
});

test('explains a refusal from the server', async ({ page }) => {
  control.auditProblem = { status: 400, code: 'VALIDATION_FAILED' };
  await page.goto('/audit');
  await expect(page.getByRole('alert')).toBeVisible();
});

test('hides a column and keeps it hidden', async ({ page }) => {
  await page.goto('/audit');
  await page.getByRole('button', { name: 'Columns' }).click();
  await page.getByRole('menuitemcheckbox', { name: 'Target' }).click();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('columnheader', { name: 'Target' })).toBeHidden();
  await page.reload();
  await expect(page.getByRole('columnheader', { name: 'Action' })).toBeVisible();
  await expect(page.getByRole('columnheader', { name: 'Target' })).toBeHidden();
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/audit');
  await expect(page.getByText('license.imported')).toHaveCount(0);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language, with action codes as they are', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto('/audit');
    await expect(page.getByRole('heading', { name: chinese['audit.title'] })).toBeVisible();
    await expect(rows(page).filter({ hasText: 'license.imported' })).toContainText(
      chinese['audit.outcome.success'],
    );
  });
});
