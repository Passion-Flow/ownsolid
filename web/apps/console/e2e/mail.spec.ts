// Email delivery: an administrator sees why an email failed and sends it again.
import {
  failedMail,
  mockControlPlane,
  secondFailedMail,
  sentMail,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { failedView } from './pages.ts';

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

  await failedRow.getByRole('button', { name: 'Send again to grace@example.test' }).click();
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
  await page.getByRole('button', { name: 'Clear the filters' }).click();
  await expect(page.getByRole('row', { name: /ada@example\.test/ })).toBeVisible();
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

test.describe('filter bar', () => {
  test('filters by recipient and type on the server', async ({ page }) => {
    await page.goto('/mail');
    await page.getByLabel('Filter by recipient').fill('GRACE');
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
    await page.getByLabel('Filter by recipient').fill('');
    await page.getByLabel('Email type').selectOption('password_reset');
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toHaveCount(0);
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toBeVisible();
  });

  test('remembers the screen in this browser', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
  });
});

test.describe('column chooser', () => {
  test('hides and shows columns, and keeps the one that names the row', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('columnheader', { name: 'Attempts' })).toBeVisible();
    await page.getByRole('button', { name: 'Choose the columns to show' }).click();
    await expect(page.getByRole('menuitemcheckbox', { name: 'Recipient' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    const attempts = page.getByRole('menuitemcheckbox', { name: 'Attempts' });
    await attempts.click();
    await expect(attempts).toHaveAttribute('aria-checked', 'false');
    await expect(page.getByRole('columnheader', { name: 'Attempts' })).toHaveCount(0);
    await attempts.click();
    await expect(page.getByRole('columnheader', { name: 'Attempts' })).toBeVisible();
  });
});

test.describe('saved views', () => {
  const viewsMenu = /^Views: /;

  test('saves the screen under a name, for any browser', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
    const dialog = page.getByRole('dialog', { name: 'Save this view' });
    await expect(dialog.getByLabel('View name')).toBeFocused();
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog.getByText('Enter a name for the view.')).toBeVisible();
    await dialog.getByLabel('View name').fill('Failures');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Views: Failures' })).toBeVisible();
    expect(control.savedViews).toHaveLength(1);
    expect(control.savedViews[0]?.state).toEqual({
      filters: { state: 'failed' },
      hiddenColumns: [],
    });
  });

  test('refuses a name already taken and keeps the dialog open', async ({ page }) => {
    control.savedViews = [failedView];
    await page.goto('/mail');
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
    const dialog = page.getByRole('dialog', { name: 'Save this view' });
    await dialog.getByLabel('View name').fill('Failures');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog.getByRole('alert')).toContainText('This name is taken');
  });

  test('retries a failed create under the same ID', async ({ page }) => {
    control.nextViewSave = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/mail');
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
    const dialog = page.getByRole('dialog', { name: 'Save this view' });
    await dialog.getByLabel('View name').fill('Mine');
    const ids: string[] = [];
    page.on('request', (request) => {
      if (request.method() === 'PUT') ids.push(request.url());
    });
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog.getByRole('alert')).toBeVisible();
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toHaveCount(0);
    expect(ids).toHaveLength(2);
    expect(ids[1]).toBe(ids[0]);
  });

  test('opens a view, marks changes, saves and discards them', async ({ page }) => {
    control.savedViews = [failedView];
    await page.goto('/mail');
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Failures' }).click();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);

    const filter = page.getByRole('group', { name: 'Show emails' });
    await filter.getByRole('button', { name: 'All' }).click();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toBeVisible();
    await page.getByRole('button', { name: /^Views: Failures, Unsaved changes/ }).click();
    await page.getByRole('menuitem', { name: 'Discard the changes' }).click();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);

    await filter.getByRole('button', { name: 'Sent' }).click();
    await page.getByRole('button', { name: /^Views: Failures, Unsaved changes/ }).click();
    await page.getByRole('menuitem', { name: 'Save the changes' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: 'View "Failures" saved' }),
    ).toBeVisible();
    expect(control.savedViews[0]?.state).toEqual({ filters: { state: 'sent' }, hiddenColumns: [] });
    expect(control.savedViews[0]?.revision).toBe(2);
    await expect(page.getByRole('button', { name: 'Views: Failures', exact: true })).toBeVisible();
  });

  test('ignores what a saved view names that the page no longer has', async ({ page }) => {
    control.savedViews = [
      {
        ...failedView,
        state: { filters: { state: 'failed', gone: 'x' }, hiddenColumns: ['gone', 'attempts'] },
      },
    ];
    await page.goto('/mail');
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Failures' }).click();
    await expect(page.getByRole('columnheader', { name: 'Attempts' })).toHaveCount(0);
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
  });

  test('renames and deletes a view, with an undo', async ({ page }) => {
    control.savedViews = [failedView];
    await page.goto('/mail');
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Failures' }).click();
    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Rename this view' }).click();
    const dialog = page.getByRole('dialog', { name: 'Rename the view' });
    await dialog.getByLabel('View name').fill('Broken');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('button', { name: 'Views: Broken' })).toBeVisible();

    await page.getByRole('button', { name: viewsMenu }).click();
    await page.getByRole('menuitem', { name: 'Delete this view' }).click();
    await expect(page.getByRole('button', { name: 'Views: Default view' })).toBeVisible();
    expect(control.savedViews).toEqual([]);
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect.poll(() => control.savedViews.map((view) => view.name)).toEqual(['Broken']);
  });

  test('works without views when the installation cannot store them', async ({ page }) => {
    control.savedViewsProblem = { status: 503, code: 'PREFERENCE_UNAVAILABLE' };
    await page.goto('/mail');
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
    await page.getByRole('button', { name: viewsMenu }).click();
    await expect(page.getByText('Saved views are not available')).toBeVisible();
    await expect(page.getByRole('menuitem', { name: 'Save as a new view' })).toHaveCount(0);
  });
});

test.describe('bulk actions', () => {
  test.beforeEach(() => {
    control.mail = [failedMail, secondFailedMail, sentMail];
  });

  const select = (name: string) => ({ name: `Select ${name}` });

  test('offers selection only where an action applies', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('checkbox', select('grace@example.test'))).toBeVisible();
    await expect(page.getByRole('checkbox', select('ada@example.test'))).toHaveCount(0);
    await expect(page.getByRole('toolbar')).toHaveCount(0);
  });

  test('has a header checkbox in three states', async ({ page }) => {
    await page.goto('/mail');
    const all = page.getByRole('checkbox', { name: 'Select all rows' });
    await page.getByRole('checkbox', select('grace@example.test')).check();
    await expect(all).not.toBeChecked();
    expect(await all.evaluate((box) => (box as HTMLInputElement).indeterminate)).toBe(true);
    await all.click();
    await expect(all).toBeChecked();
    await expect(page.getByRole('toolbar')).toContainText('2 selected');
    await all.click();
    await expect(page.getByRole('toolbar')).toHaveCount(0);
  });

  test('sends every selected email again in one action', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('checkbox', { name: 'Select all rows' }).check();
    await page.getByRole('toolbar').getByRole('button', { name: 'Send again' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: '2 emails queued again' }),
    ).toBeVisible();
    expect(control.resendKeys).toHaveLength(2);
    await expect(page.getByRole('toolbar')).toHaveCount(0);
  });

  test('keeps the rows that failed selected and retries only those', async ({ page }) => {
    control.nextResend = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/mail');
    await page.getByRole('checkbox', { name: 'Select all rows' }).check();
    await page.getByRole('toolbar').getByRole('button', { name: 'Send again' }).click();
    const report = page.getByRole('status').filter({ hasText: 'could not be sent' });
    await expect(report).toContainText('1 queued again; 1 could not be sent');
    await expect(page.getByRole('toolbar')).toContainText('1 selected');
    const sent = control.resendKeys.length;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: '1 email queued again' }),
    ).toBeVisible();
    expect(control.resendKeys).toHaveLength(sent + 1);
  });

  test('ends the selection with Clear or Escape, and when a filter hides the rows', async ({
    page,
  }) => {
    await page.goto('/mail');
    const row = page.getByRole('checkbox', select('grace@example.test'));
    await row.check();
    await page.getByRole('button', { name: 'Clear the selection' }).click();
    await expect(page.getByRole('toolbar')).toHaveCount(0);
    await row.check();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('toolbar')).toHaveCount(0);
    await row.check();
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Sent' })
      .click();
    await expect(page.getByRole('toolbar')).toHaveCount(0);
  });
});

test('fits a phone without scrolling the page sideways', async ({ page }) => {
  control.mail = [failedMail, secondFailedMail, sentMail];
  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto('/mail');
  await expect(page.getByRole('checkbox', { name: 'Select all rows' })).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBe(0);
});

test.describe('detail drawer', () => {
  test('shows an email and returns focus to its row', async ({ page }) => {
    await page.goto('/mail');
    const open = page.getByRole('button', {
      name: 'Show details of the email to grace@example.test',
    });
    await open.click();
    const drawer = page.getByRole('dialog', { name: 'grace@example.test' });
    await expect(drawer).toContainText('Invitation');
    await expect(drawer).toContainText(failedMail.id);
    await page.keyboard.press('Escape');
    await expect(drawer).toHaveCount(0);
    await expect(open).toBeFocused();
  });

  test('sends a failed email again', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('button', { name: 'Show details of the email to grace@example.test' })
      .click();
    const drawer = page.getByRole('dialog', { name: 'grace@example.test' });
    await drawer.getByRole('button', { name: 'Send again' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Email queued again' })).toBeVisible();
    expect(control.resendKeys).toHaveLength(1);
  });

  test('offers no resend for an email that was delivered', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('button', { name: 'Show details of the email to ada@example.test' })
      .click();
    const drawer = page.getByRole('dialog', { name: 'ada@example.test' });
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole('button', { name: 'Send again' })).toHaveCount(0);
  });
});

test.describe('shareable links', () => {
  test.beforeEach(() => {
    control.mail = [failedMail, secondFailedMail, sentMail];
  });

  test('puts the filters, the sort and the open drawer in the address', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    await page.getByLabel('Filter by recipient').fill('he');
    await expect(page).toHaveURL(/[?&]recipient=he(&|$)/);
    await page.getByRole('button', { name: /^Created/ }).click();
    await page
      .getByRole('button', { name: 'Show details of the email to hedy@example.test' })
      .click();
    const url = new URL(page.url());
    expect(Object.fromEntries(url.searchParams)).toEqual({
      order: 'oldest',
      recipient: 'he',
      state: 'failed',
      open: secondFailedMail.id,
    });
  });

  test('shows what a link names to someone who has never been here', async ({ page }) => {
    await page.goto(`/mail?state=failed&order=oldest&open=${failedMail.id}`);
    await expect(page.getByRole('dialog', { name: 'grace@example.test' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page).not.toHaveURL(/open=/);
    const rows = page.getByRole('row');
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(1)).toContainText('hedy@example.test');
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
    await expect(page.getByRole('columnheader', { name: /^Created/ })).toHaveAttribute(
      'aria-sort',
      'ascending',
    );
  });

  test('lets a link win over what this browser remembers', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Sent' })
      .click();
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toHaveCount(0);
    await page.goto('/mail?state=failed');
    await expect(page.getByRole('row', { name: /grace@example\.test/ })).toBeVisible();
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toHaveCount(0);
  });

  test('shows the remembered screen in the address when there is no link', async ({ page }) => {
    await page.goto('/mail');
    await page
      .getByRole('group', { name: 'Show emails' })
      .getByRole('button', { name: 'Failed' })
      .click();
    await page.goto('/mail');
    await expect(page).toHaveURL(/\?state=failed$/);
  });

  test('ignores what a link names that the page does not have', async ({ page }) => {
    await page.goto('/mail?state=gone&purpose=spam&open=..%2F..&utm=x');
    await expect(page.getByRole('row', { name: /ada@example\.test/ })).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('keeps the address to a single history entry', async ({ page }) => {
    await page.goto('/');
    await page.goto('/mail');
    const filter = page.getByRole('group', { name: 'Show emails' });
    await filter.getByRole('button', { name: 'Failed' }).click();
    await filter.getByRole('button', { name: 'Sent' }).click();
    await page.goBack();
    await expect(page.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  });

  test('copies the link', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.goto('/mail?state=failed');
    await page.getByRole('button', { name: 'Copy a link to this list' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Link copied' })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(page.url());
  });
});
