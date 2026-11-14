// The Users page: an administrator finds an account, disables and enables it, and invites people.
import {
  adminUser,
  memberUser,
  mockControlPlane,
  pendingInvitation,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [{ ...memberUser }, { ...adminUser }];
});

const row = (page: Page, email: string) => page.getByRole('row', { name: new RegExp(email) });
const open = (page: Page, name: string) =>
  page.getByRole('button', { name: `Open details of ${name}` }).click();

test('is reached from the navigation and lists every account', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Governance', exact: true }).click();
  await page.getByRole('link', { name: 'Users and workspaces' }).click();
  await expect(page.getByRole('heading', { name: 'Users', level: 1 })).toBeFocused();
  await expect(row(page, 'grace@example.test')).toContainText('Member');
  await expect(row(page, 'grace@example.test')).toContainText('\u7b80\u4f53\u4e2d\u6587');
  await expect(row(page, 'ada@example.test')).toContainText('Platform administrator');
});

test('filters by status and email from the server, also from a link', async ({ page }) => {
  await page.goto('/users');
  await page.getByLabel('Filter by email').fill('GRACE');
  await expect(page).toHaveURL(/email=GRACE/);
  await expect(row(page, 'ada@example.test')).toHaveCount(0);
  await expect(row(page, 'grace@example.test')).toBeVisible();
  await page.reload();
  await expect(row(page, 'ada@example.test')).toHaveCount(0);
  await page
    .getByRole('group', { name: 'Show accounts' })
    .getByRole('button', { name: 'Disabled' })
    .click();
  await expect(page.getByText('No accounts match')).toBeVisible();
  await page.getByRole('button', { name: 'Clear the filters' }).click();
  await expect(row(page, 'ada@example.test')).toBeVisible();
});

test('disables an account with a reason and undoes it', async ({ page }) => {
  await page.goto('/users');
  await open(page, 'Grace Hopper');
  const drawer = page.getByRole('dialog', { name: 'Grace Hopper' });
  await expect(drawer).toContainText('Not yet');
  await drawer.getByRole('button', { name: 'Disable' }).click();
  const dialog = page.getByRole('dialog', { name: 'Disable grace@example.test' });
  await expect(dialog).toContainText('its sessions end at once');
  await expect(dialog.getByRole('button', { name: 'Disable the account' })).toBeDisabled();
  await dialog.getByLabel('Reason').fill('Left the company');
  await dialog.getByRole('button', { name: 'Disable the account' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'grace@example.test disabled' }),
  ).toBeVisible();
  await expect(row(page, 'grace@example.test')).toContainText('Disabled');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(row(page, 'grace@example.test')).toContainText('Active');
  expect(control.userReasons).toEqual(['Left the company', 'Undone from the console']);
});

test('enables a disabled account at once', async ({ page }) => {
  control.users = [{ ...memberUser, status: 'disabled' }, { ...adminUser }];
  await page.goto('/users');
  await open(page, 'Grace Hopper');
  await page.getByRole('dialog').getByRole('button', { name: 'Enable' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'grace@example.test enabled' }),
  ).toBeVisible();
  await expect(row(page, 'grace@example.test')).toContainText('Active');
});

test('explains why the first administrator cannot be disabled', async ({ page }) => {
  await page.goto('/users');
  await open(page, 'Grace Hopper');
  await page.getByRole('dialog').getByRole('button', { name: 'Disable' }).click();
  const dialog = page.getByRole('dialog', { name: 'Disable grace@example.test' });
  await dialog.getByLabel('Reason').fill('Test');
  control.nextStatusChange = { status: 409, code: 'PROTECTED_ACCOUNT' };
  await dialog.getByRole('button', { name: 'Disable the account' }).click();
  await expect(dialog).toContainText('This account cannot be disabled');
});

test('does not offer to disable your own account', async ({ page }) => {
  await page.goto('/users');
  await open(page, 'Ada Lovelace');
  const drawer = page.getByRole('dialog', { name: 'Ada Lovelace' });
  await expect(drawer).toContainText('This is your account');
  await expect(drawer.getByRole('button', { name: 'Disable' })).toHaveCount(0);
});

test('opens an account from a link, and says when it does not exist', async ({ page }) => {
  await page.goto(`/users?status=disabled&open=${memberUser.id}`);
  await expect(page.getByRole('dialog', { name: 'Grace Hopper' })).toBeVisible();
  await page.goto('/users?open=6f1c7a1e-0000-4000-8000-000000000099');
  await expect(page.getByRole('dialog', { name: 'Account details' })).toContainText('Not found');
});

test('offers a retry when the list cannot load', async ({ page }) => {
  control.usersProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/users');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.usersProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(row(page, 'grace@example.test')).toBeVisible();
});

test('invites someone, and explains an address that already has an account', async ({ page }) => {
  await page.goto('/users');
  await page.getByRole('button', { name: 'Invite' }).click();
  const dialog = page.getByRole('dialog', { name: 'Invite someone' });
  await dialog.getByLabel('Email address').fill('grace@example.test');
  control.nextInvite = { status: 409, code: 'ACCOUNT_EXISTS' };
  await dialog.getByRole('button', { name: 'Send the invitation' }).click();
  await expect(dialog).toContainText('This address already has an account');
  await dialog.getByLabel('Email address').fill('alan@example.test');
  await dialog.getByLabel('Email language').selectOption('zh-CN');
  await dialog.getByRole('button', { name: 'Send the invitation' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Invitation sent to alan@example.test' }),
  ).toBeVisible();
  const invitations = page.getByRole('region', { name: 'Invitations' });
  await expect(invitations).toContainText('alan@example.test');
  expect(control.invitations[0]?.locale).toBe('zh-CN');
});

test('retries an invitation with the same key', async ({ page }) => {
  await page.goto('/users');
  await page.getByRole('button', { name: 'Invite' }).click();
  const dialog = page.getByRole('dialog', { name: 'Invite someone' });
  await dialog.getByLabel('Email address').fill('alan@example.test');
  control.nextInvite = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await dialog.getByRole('button', { name: 'Send the invitation' }).click();
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Invitation sent' })).toBeVisible();
  expect(control.invitationKeys).toHaveLength(2);
  expect(control.invitationKeys[1]).toBe(control.invitationKeys[0]);
});

test('shows a waiting invitation whose email failed, resends and revokes it', async ({ page }) => {
  control.invitations = [{ ...pendingInvitation }];
  await page.goto('/users');
  const invitations = page.getByRole('region', { name: 'Invitations' });
  await expect(invitations).toContainText('the email could not be sent');
  await invitations.getByRole('button', { name: 'Send again alan@example.test' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Invitation sent again' })).toBeVisible();
  await expect(invitations).not.toContainText('the email could not be sent');

  await invitations.getByRole('button', { name: 'Revoke alan@example.test' }).click();
  const confirm = page.getByRole('dialog', { name: 'Revoke this invitation' });
  await confirm.getByRole('textbox').first().fill('alan@example.test');
  await confirm.getByRole('button', { name: 'Revoke the invitation' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Invitation to alan@example.test revoked' }),
  ).toBeVisible();
  await expect(invitations).toHaveCount(0);
  expect(control.revocations[0]).toMatchObject({ reason: 'Revoked from the Users page' });
});

test('explains a resend of an invitation that changed meanwhile', async ({ page }) => {
  control.invitations = [{ ...pendingInvitation }];
  await page.goto('/users');
  control.nextInvitationResend = { status: 409, code: 'INVITATION_STATE_CONFLICT' };
  await page.getByRole('button', { name: 'Send again alan@example.test' }).click();
  await expect(page.getByText('The invitation changed')).toBeVisible();
});

test('lists an expired invitation, which can be sent again but not revoked', async ({ page }) => {
  control.invitations = [{ ...pendingInvitation, status: 'expired' }];
  await page.goto('/users');
  const invitations = page.getByRole('region', { name: 'Invitations' });
  await expect(invitations).toContainText('Expired');
  await expect(invitations.getByRole('button', { name: /^Revoke/ })).toHaveCount(0);
  await invitations.getByRole('button', { name: 'Send again alan@example.test' }).click();
  await expect(invitations).toContainText('Waiting');
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/users');
  await expect(row(page, 'grace@example.test')).toHaveCount(0);
});
