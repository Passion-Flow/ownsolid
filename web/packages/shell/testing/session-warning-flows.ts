// The warning before a session ends, in a real browser on both entrances. The server's
// answers are scripted, so nothing here waits out a real timeout.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import { account, freshSession, mockControlPlane, type ControlPlane } from './control-plane.ts';

const idleTitle = 'You are about to be signed out';
const limitTitle = 'This session is reaching its time limit';
// The page looks again on the mock's five-second poll interval.
const nextPoll = { timeout: 8_000 };

async function open(page: Page): Promise<void> {
  await page.goto('/');
  await page.getByRole('heading', { name: 'Welcome, Ada Lovelace' }).waitFor();
}

/** Registers the warning flows in the current spec file. */
export function sessionWarningFlows(audience: ControlPlane['audience']): void {
  let control: ControlPlane;

  test.beforeEach(async ({ page }) => {
    control = await mockControlPlane(page, audience);
    control.signedIn = true;
  });

  test('does not warn while the session has plenty left', async ({ page }) => {
    await open(page);
    await expect.poll(() => control.sessionReads.length).toBeGreaterThan(0);
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('marks its own checks and the notification poll as background', async ({ page }) => {
    await open(page);
    await expect.poll(() => control.notificationPolls.length).toBeGreaterThan(0);
    await expect.poll(() => control.sessionReads.length).toBeGreaterThan(0);
    expect(control.notificationPolls.every(Boolean)).toBe(true);
    expect(control.sessionReads.every(Boolean)).toBe(true);
  });

  test('warns with a countdown, and one key keeps the session', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    const dialog = page.getByRole('dialog', { name: idleTitle });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('timer')).toHaveText(/^1:[34]\d$/);
    const stay = dialog.getByRole('button', { name: 'Stay signed in' });
    await expect(stay).toBeFocused();

    await page.keyboard.press('Enter');
    await expect(dialog).toHaveCount(0);
    expect(control.sessionReads.at(-1)).toBe(false);
    expect(control.session.idle_remaining_seconds).toBe(freshSession.idle_timeout_seconds);
  });

  test('counts Escape as being here', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    await expect(page.getByRole('dialog', { name: idleTitle })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(control.session.idle_remaining_seconds).toBe(freshSession.idle_timeout_seconds);
  });

  test('does not treat a stray mouse movement as staying', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    const dialog = page.getByRole('dialog', { name: idleTitle });
    await expect(dialog).toBeVisible();
    await page.mouse.move(20, 20);
    await page.mouse.move(60, 60);
    await page.waitForTimeout(5_500);
    await expect(dialog).toBeVisible();
    expect(control.sessionReads.every(Boolean)).toBe(true);
  });

  test('extends the session while the person is working, without a warning', async ({ page }) => {
    control.session.warning_seconds = 2;
    await open(page);
    await expect.poll(() => control.sessionReads.length).toBeGreaterThan(0);
    await page.mouse.move(40, 40);
    await expect.poll(() => control.sessionReads.includes(false), nextPoll).toBe(true);
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('closes the warning when another tab kept the session going', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    await expect(page.getByRole('dialog', { name: idleTitle })).toBeVisible();
    control.session = { ...freshSession };
    await expect(page.getByRole('dialog')).toHaveCount(0, nextPoll);
  });

  test('explains a failed attempt to stay and tries again', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    const dialog = page.getByRole('dialog', { name: idleTitle });
    await expect(dialog).toBeVisible();
    await page.route('**/api/v1/me/session', (route) => route.abort('connectionrefused'), {
      times: 1,
    });
    await dialog.getByRole('button', { name: 'Stay signed in' }).click();
    await expect(dialog.getByRole('alert')).toBeVisible();
    await dialog.getByRole('button', { name: 'Try again' }).click();
    await expect(dialog).toHaveCount(0);
  });

  test('signs out from the warning', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    await page
      .getByRole('dialog', { name: idleTitle })
      .getByRole('button', { name: 'Sign out' })
      .click();
    await expect(page).toHaveURL(/\/sign-in$/);
    await expect(page.getByText('Your session has ended')).toHaveCount(0);
  });

  test('says why, and returns to the page, when the session ended', async ({ page }) => {
    control.session.idle_remaining_seconds = 3;
    await page.goto('/account');
    const dialog = page.getByRole('dialog', { name: idleTitle });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('timer')).toHaveText('0:00', nextPoll);
    control.signedIn = false;
    await expect(page).toHaveURL(/\/sign-in\?redirect=/, nextPoll);
    await expect(page.getByText('Your session has ended')).toBeVisible();

    control.session = { ...freshSession };
    await page.getByLabel('Email').fill(account.email);
    await page.getByLabel('Password').fill(account.password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
    await expect(page.getByText('Your session has ended')).toHaveCount(0);
  });

  test('keeps the warning up while the server cannot be reached', async ({ page }) => {
    control.session.idle_remaining_seconds = 100;
    await open(page);
    const dialog = page.getByRole('dialog', { name: idleTitle });
    await expect(dialog).toBeVisible();
    const reads = control.sessionReads.length;
    await page.route('**/api/v1/me/session', (route) => route.abort('connectionrefused'));
    await page.waitForTimeout(5_500);
    await expect(dialog).toBeVisible();
    await expect(page).not.toHaveURL(/\/sign-in/);
    expect(control.sessionReads).toHaveLength(reads);
  });

  test('warns about the time limit, which staying cannot extend', async ({ page }) => {
    control.session.absolute_remaining_seconds = 100;
    await open(page);
    const dialog = page.getByRole('dialog', { name: limitTitle });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Sign in again' })).toBeVisible();
    await dialog.getByRole('button', { name: 'Keep working' }).click();
    await expect(dialog).toHaveCount(0);
    expect(control.sessionReads.every(Boolean)).toBe(true);
    await page.waitForTimeout(5_500);
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('is complete in Chinese', async ({ page }) => {
    await open(page);
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: chinese['locale.zh-CN'] }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', /zh/);
    control.session.idle_remaining_seconds = 100;
    await page.reload();
    const dialog = page.getByRole('dialog', { name: chinese['session.idle.title'] });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: chinese['session.idle.stay'] })).toBeVisible();
    await expect(dialog.getByText(chinese['session.remaining'])).toBeVisible();
  });
}
