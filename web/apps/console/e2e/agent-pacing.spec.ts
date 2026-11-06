// Agent pacing: an administrator slows the fleet's reports with a reason, and is told what the
// online window allows before control refuses it.
import { mockControlPlane, type ControlPlane } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
});

const heartbeat = (page: Page) => page.getByLabel('Heartbeat interval (seconds)');
const save = (page: Page) => page.getByRole('button', { name: 'Save' });

test('is reached from the Nodes page and shows the current pacing', async ({ page }) => {
  await page.goto('/nodes');
  await page.getByRole('link', { name: 'Agent pacing' }).click();
  await expect(page.getByRole('heading', { name: 'Agent pacing', level: 1 })).toBeFocused();
  await expect(page.getByRole('link', { name: 'Nodes', exact: true }).first()).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page.getByText('25 s')).toBeVisible();
  await expect(heartbeat(page)).toHaveValue('10');
  await expect(page.getByText('the interval can be up to 15 seconds')).toBeVisible();
});

test('saves a change with its reason and can undo it', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await expect(save(page)).toBeDisabled();
  await heartbeat(page).fill('15');
  await expect(save(page)).toBeDisabled();
  await page.getByLabel('Reason').fill('Reducing heartbeat load');
  await save(page).click();
  await expect(page.getByRole('status').filter({ hasText: 'Pacing saved' })).toBeVisible();
  expect(control.pacing.heartbeat_seconds).toBe(15);
  expect(control.nodeReasons).toEqual(['Reducing heartbeat load']);
  await expect(page.getByLabel('Reason')).toHaveValue('');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(heartbeat(page)).toHaveValue('10');
  expect(control.pacing.heartbeat_seconds).toBe(10);
  expect(control.nodeReasons).toEqual(['Reducing heartbeat load', 'Undone from the console']);
});

test('explains the online window before control refuses', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await heartbeat(page).fill('20');
  await page.getByLabel('Reason').fill('Too slow');
  await expect(page.getByText('the interval can be at most 15 seconds')).toBeVisible();
  await expect(page.getByText('raise nodes.online_seconds')).toBeVisible();
  await expect(save(page)).toBeDisabled();
  // Less jitter leaves room for a slower interval.
  await page.getByLabel('Jitter (percent)').fill('0');
  await heartbeat(page).fill('16');
  await expect(page.getByText('the interval can be up to 16 seconds')).toBeVisible();
  await expect(save(page)).toBeEnabled();
});

test('checks each value against its bounds', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await heartbeat(page).fill('2');
  await expect(page.getByText('Enter a whole number from 5 to 120.')).toBeVisible();
  await heartbeat(page).fill('ten');
  await expect(page.getByText('Enter a whole number from 5 to 120.')).toBeVisible();
  await heartbeat(page).fill('12');
  await page.getByLabel('First retry within (seconds)').fill('30');
  await page.getByLabel('Longest retry wait (seconds)').fill('20');
  await expect(page.getByText('This must be at least the first retry wait.')).toBeVisible();
  await page.getByLabel('Reason').fill('x');
  await expect(save(page)).toBeDisabled();
});

test('explains a refusal from control, which a change of values then passes', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await heartbeat(page).fill('12');
  await page.getByLabel('Reason').fill('Busy fleet');
  control.nextPacingChange = { status: 409, code: 'AGENT_PACING_EXCEEDS_ONLINE_WINDOW' };
  await save(page).click();
  await expect(page.getByText('This pacing is too slow for the online window')).toBeVisible();
  // Trying the same values again cannot pass, so the page offers no retry.
  await expect(page.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  await heartbeat(page).fill('11');
  await save(page).click();
  await expect(page.getByRole('status').filter({ hasText: 'Pacing saved' })).toBeVisible();
  expect(control.pacing.heartbeat_seconds).toBe(11);
});

test('shows what someone else saved when the change came too late', async ({ page }) => {
  await page.goto('/nodes/pacing');
  await heartbeat(page).fill('14');
  await page.getByLabel('Reason').fill('Late change');
  control.pacing = { ...control.pacing, heartbeat_seconds: 12, version: 2 };
  await save(page).click();
  await expect(page.getByText('This was changed by someone else')).toBeVisible();
  await expect(heartbeat(page)).toHaveValue('12');
  expect(control.pacing.heartbeat_seconds).toBe(12);
});

test('offers a retry when it cannot load', async ({ page }) => {
  control.pacingProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/nodes/pacing');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.pacingProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(heartbeat(page)).toHaveValue('10');
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/nodes/pacing');
  await expect(heartbeat(page)).toHaveCount(0);
});
