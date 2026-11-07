// Refusing agent traffic: an administrator sets a level with a reason, types a confirmation for the
// strongest one, and the Nodes page says so for as long as it lasts.
import { expectAccessible, mockControlPlane, type ControlPlane } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
});

const level = (page: Page) => page.getByLabel('Refuse', { exact: true });
const reason = (page: Page) => page.getByLabel('Why this level');
const apply = (page: Page) => page.getByRole('button', { name: 'Apply' });

test('applies a level with its reason, shows it, and can undo it', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await expect(apply(page)).toBeDisabled();
  await level(page).selectOption('telemetry');
  await expect(page.getByText("nodes' metrics and logs stop arriving")).toBeVisible();
  await expect(apply(page)).toBeDisabled();
  await reason(page).fill('Fleet overload');
  await apply(page).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Now refusing: Transfers and telemetry.' }),
  ).toBeVisible();
  expect(control.shed.level).toBe('telemetry');
  expect(control.nodeReasons).toEqual(['Fleet overload']);
  await expect(page.getByRole('alert').filter({ hasText: 'being refused' })).toBeVisible();
  await expect(reason(page)).toHaveValue('');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'being refused' })).toBeHidden();
  expect(control.shed.level).toBe('none');
  expect(control.nodeReasons).toEqual(['Fleet overload', 'Undone from the console']);
});

test('asks for the level to be typed before refusing heartbeats', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await level(page).selectOption('heartbeats');
  await reason(page).fill('Database saturated');
  await apply(page).click();
  const dialog = page.getByRole('dialog', { name: 'Refuse all agent traffic' });
  await expect(dialog.getByText('Every node will turn offline here')).toBeVisible();
  await expect(dialog.getByText('This cannot be undone')).toBeHidden();
  await expect(dialog.getByText('already running on them continue')).toBeVisible();
  await expectAccessible(page);
  const confirm = dialog.getByRole('button', { name: 'Refuse agent traffic' });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel('Type heartbeats to confirm').fill('heartbeat');
  await expect(confirm).toBeDisabled();
  expect(control.shed.level).toBe('none');
  await dialog.getByLabel('Type heartbeats to confirm').fill('heartbeats');
  await confirm.click();
  await expect(dialog).toBeHidden();
  expect(control.shed.level).toBe('heartbeats');
});

test('the Nodes page warns while agents are refused, and leads to the switch', async ({ page }) => {
  control.shed = { ...control.shed, level: 'heartbeats', version: 2 };
  await page.goto('/nodes');
  const warning = page.getByRole('alert').filter({ hasText: 'being refused' });
  await expect(warning).toContainText('Everything except quarantine reports');
  await warning.getByRole('link', { name: 'Review this' }).click();
  await expect(page).toHaveURL(/\/nodes\/pacing$/);
  await expect(level(page)).toHaveValue('heartbeats');
  await level(page).selectOption('none');
  await reason(page).fill('Load has passed');
  await apply(page).click();
  await expect(page.getByRole('status').filter({ hasText: 'no longer refused' })).toBeVisible();
  await page.goto('/nodes');
  await expect(page.getByRole('heading', { name: 'Nodes', level: 1 })).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: 'being refused' })).toBeHidden();
});

test('the Nodes page stays useful when the level cannot be read', async ({ page }) => {
  control.shedProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/nodes');
  await expect(page.getByRole('heading', { name: 'Nodes', level: 1 })).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: 'being refused' })).toBeHidden();
});

test('a failed change says why, keeps the form and can be retried', async ({ page }) => {
  control.nextShedChange = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/nodes/pacing');
  await level(page).selectOption('bulk');
  await reason(page).fill('Transfers saturating');
  await apply(page).click();
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Now refusing: Transfers.' }),
  ).toBeVisible();
  expect(control.shed.level).toBe('bulk');
});

test('a level another administrator changed replaces the stale form', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await level(page).selectOption('bulk');
  await reason(page).fill('Stale form');
  control.shed = { ...control.shed, level: 'telemetry', version: 2 };
  await apply(page).click();
  await expect(level(page)).toHaveValue('telemetry');
  await expect(page.getByText('Refused now').locator('..')).toContainText(
    'Transfers and telemetry',
  );
});

test('says the permission is missing without losing the pacing form', async ({ page }) => {
  control.shedProblem = { status: 403, code: 'ACCESS_DENIED' };
  await page.goto('/nodes/pacing');
  await expect(page.getByLabel('Heartbeat interval (seconds)')).toBeVisible();
  await expect(apply(page)).toBeHidden();
  await expectAccessible(page);
});

test('is usable in Chinese', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('ownsolid.locale', 'zh-CN');
  });
  await page.goto('/nodes/pacing');
  await expect(
    page.getByRole('heading', { name: '\u62d2\u7edd\u4ee3\u7406\u7a0b\u5e8f\u6d41\u91cf' }),
  ).toBeVisible();
});
