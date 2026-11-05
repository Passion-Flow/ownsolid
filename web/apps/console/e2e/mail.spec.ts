// Email delivery: an administrator sees why an email failed and sends it again.
import { failedMail, mockControlPlane, sentMail, type ControlPlane } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.mail = [failedMail, sentMail];
});

test('reaches the page from the navigation', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', { name: 'Email delivery' }).click();
  await expect(page.getByRole('heading', { name: 'Email delivery', level: 1 })).toBeFocused();
});

test('says why an email failed and sends it again', async ({ page }) => {
  await page.goto('/mail');
  const failedRow = page.getByRole('row', { name: /grace@example\.test/ });
  await expect(failedRow).toContainText('Failed');
  await expect(failedRow).toContainText('The mail server could not be reached');
  await expect(page.getByRole('row', { name: /ada@example\.test/ })).toContainText('Sent');

  await failedRow
    .getByRole('button', { name: 'Send again to grace@example.test' })
    .click();
  await expect(page.getByRole('status')).toContainText('Email queued again');
  await expect(page.getByRole('row', { name: /Waiting/ })).toContainText('grace@example.test');
  expect(control.resendKeys).toHaveLength(1);
});

test('retries a failed resend with the same key', async ({ page }) => {
  control.nextResend = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/mail');
  await page.getByRole('button', { name: /^Send again to/ }).click();
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('status')).toContainText('Email queued again');
  expect(control.resendKeys).toHaveLength(2);
  expect(control.resendKeys[0]).toMatch(/^[0-9a-f-]{36}$/);
  expect(control.resendKeys[1]).toBe(control.resendKeys[0]);
});

test('filters by status and explains an empty filter', async ({ page }) => {
  await page.goto('/mail');
  const filter = page.getByRole('group', { name: 'Show emails' });
  await filter.getByRole('button', { name: 'Failed' }).click();
  await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
  await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
  await filter.getByRole('button', { name: 'Waiting' }).click();
  await expect(page.getByText('No emails match')).toBeVisible();
});

test('recovers when the list could not load', async ({ page }) => {
  control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/mail');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.mailProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/mail');
  await expect(page.getByRole('row', { name: /grace@example\.test/ })).toHaveCount(0);
});
