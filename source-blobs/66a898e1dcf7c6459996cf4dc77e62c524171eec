// Workspaces: an administrator creates one, names its owner, changes who belongs and what each may do,
// always with a reason and a way back. The server's rules are explained where they bite.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  adminUser,
  defaultWorkspace,
  memberUser,
  mockControlPlane,
  plainMember,
  teamWorkspace,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [adminUser, memberUser];
  control.workspaceList = [{ ...defaultWorkspace }, { ...teamWorkspace }];
  control.members = {
    [defaultWorkspace.id]: [{ ...plainMember, role: 'owner' }],
    [teamWorkspace.id]: [
      { ...plainMember, user_id: adminUser.id, email: adminUser.email, role: 'owner' },
      { ...plainMember },
    ],
  };
});

const openTeam = (page: Page) => page.goto(`/users/workspaces/${teamWorkspace.id}`);
const toast = (page: Page, text: string | RegExp) =>
  page.getByRole('status').filter({ hasText: text });

test('is reached from the Users page, under its navigation item', async ({ page }) => {
  await page.goto('/users');
  await page.getByRole('link', { name: 'Workspaces', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Workspaces', level: 1 })).toBeFocused();
  await expect(
    page.getByRole('navigation').getByRole('link', { name: 'Users and workspaces' }),
  ).toHaveAttribute('aria-current', 'page');
  const row = page.getByRole('row', { name: new RegExp(defaultWorkspace.name) });
  await expect(row).toContainText('Default');
  await expect(row).toContainText('Active');
});

test('creates a workspace owned by the person creating it', async ({ page }) => {
  await page.goto('/users/workspaces');
  await page.getByRole('button', { name: 'Create a workspace' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create a workspace' });
  await expect(dialog.getByLabel("Owner's email")).toHaveValue(adminUser.email);
  await dialog.getByLabel('Name').fill('Speech');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Speech', level: 1 })).toBeVisible();
  await expect(toast(page, 'Speech created')).toBeVisible();
});

test('says when no account has the owner address, and keeps the form', async ({ page }) => {
  await page.goto('/users/workspaces#create');
  const dialog = page.getByRole('dialog', { name: 'Create a workspace' });
  await expect(page).toHaveURL(/\/users\/workspaces$/);
  await dialog.getByLabel('Name').fill('Speech');
  await dialog.getByLabel("Owner's email").fill('nobody@example.test');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(dialog.getByText('No account has this address.')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveValue('Speech');
  await dialog.getByLabel("Owner's email").fill(memberUser.email);
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Speech', level: 1 })).toBeVisible();
});

test('explains a name that is taken', async ({ page }) => {
  await page.goto('/users/workspaces#create');
  const dialog = page.getByRole('dialog', { name: 'Create a workspace' });
  await dialog.getByLabel('Name').fill(teamWorkspace.name);
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(dialog.getByText('That name is taken')).toBeVisible();
  await expect(dialog.getByLabel('Name')).toHaveValue(teamWorkspace.name);
});

test('says when the installation holds as many workspaces as it allows', async ({ page }) => {
  await page.route('**/api/v1/admin/workspaces', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({
          status: 409,
          contentType: 'application/json',
          json: {
            type: 'about:blank',
            title: 'RESOURCE_LIMIT_REACHED',
            status: 409,
            code: 'RESOURCE_LIMIT_REACHED',
            request_id: '6f1c7a1e-0000-4000-8000-0000000000ff',
          },
        })
      : route.fallback(),
  );
  await page.goto('/users/workspaces#create');
  const dialog = page.getByRole('dialog', { name: 'Create a workspace' });
  await dialog.getByLabel('Name').fill('Vision');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('The limit is reached');
  await expect(dialog.getByLabel('Name')).toHaveValue('Vision');
});

test('renames with a reason, and undoes it', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: 'Rename' }).click();
  const dialog = page.getByRole('dialog', { name: 'Rename this workspace' });
  const submit = dialog.getByRole('button', { name: 'Rename' });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel('Name').fill('Vision');
  await expect(submit).toBeDisabled();
  await dialog.getByLabel('Reason').fill('Team renamed');
  await submit.click();
  await expect(page.getByRole('heading', { name: 'Vision', level: 1 })).toBeVisible();
  expect(control.workspaceReasons).toEqual(['Team renamed']);
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(page.getByRole('heading', { name: teamWorkspace.name, level: 1 })).toBeVisible();
  expect(control.workspaceReasons).toEqual(['Team renamed', 'Undone from the console']);
});

test('disables and enables with a reason, and undoes it', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: 'Disable' }).click();
  const dialog = page.getByRole('dialog', { name: `Disable ${teamWorkspace.name}` });
  await expect(dialog).toContainText('Nothing is deleted');
  await dialog.getByLabel('Reason').fill('Project ended');
  await dialog.getByRole('button', { name: 'Disable the workspace' }).click();
  await expect(page.getByRole('main').getByText('Disabled', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(page.getByRole('button', { name: 'Disable' })).toBeVisible();

  await page.getByRole('button', { name: 'Disable' }).click();
  await dialog.getByLabel('Reason').fill('Project ended');
  await dialog.getByRole('button', { name: 'Disable the workspace' }).click();
  await page.getByRole('button', { name: 'Enable' }).click();
  const enable = page.getByRole('dialog', { name: `Enable ${teamWorkspace.name}` });
  await enable.getByLabel('Reason').fill('Project resumed');
  await enable.getByRole('button', { name: 'Enable the workspace' }).click();
  await expect(page.getByRole('button', { name: 'Disable' })).toBeVisible();
});

test('does not offer to disable the default workspace, and says why', async ({ page }) => {
  await page.goto(`/users/workspaces/${defaultWorkspace.id}`);
  await expect(page.getByText('it cannot be disabled')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Disable' })).toBeHidden();
});

test('adds someone, explains who cannot be added, and undoes it', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: 'Add a person' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add a person' });
  await dialog.getByLabel('Email').fill('ghost@nowhere.test');
  await dialog.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(dialog.getByText('No account has this address.')).toBeVisible();
  await dialog.getByLabel('Email').fill(plainMember.email);
  await dialog.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(dialog.getByText('Already a member')).toBeVisible();
  await dialog.getByLabel('Email').fill('alan@example.test');
  await dialog.getByLabel('Role').selectOption('viewer');
  await dialog.getByRole('button', { name: 'Add', exact: true }).click();
  const row = page.getByRole('row', { name: /alan@example.test/ });
  await expect(row).toContainText('Viewer');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(row).toBeHidden();
});

test('changes a role, and undoes it', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: `Change role for ${plainMember.email}` }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Role').selectOption('viewer');
  await dialog.getByLabel('Reason').fill('Read only now');
  await dialog.getByRole('button', { name: 'Change the role' }).click();
  const row = page.getByRole('row', { name: new RegExp(plainMember.email) });
  await expect(row).toContainText('Viewer');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(row).toContainText('Member');
});

test('removes someone, and undoing brings back their role', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: `Remove ${plainMember.email}` }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('Their account stays');
  await dialog.getByLabel('Reason').fill('Left the team');
  await dialog.getByRole('button', { name: 'Remove' }).click();
  const row = page.getByRole('row', { name: new RegExp(plainMember.email) });
  await expect(row).toBeHidden();
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(row).toContainText('Member');
});

test('explains why the last owner cannot go, and keeps the dialog', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: `Remove ${adminUser.email}` }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Reason').fill('Leaving');
  await dialog.getByRole('button', { name: 'Remove' }).click();
  await expect(dialog.getByText('A workspace needs an owner')).toBeVisible();
  await expect(dialog.getByLabel('Reason')).toHaveValue('Leaving');
  await expect(page.getByRole('row', { name: new RegExp(adminUser.email) })).toBeVisible();
});

test('catches up when someone else changed it first', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: 'Disable' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Reason').fill('Project ended');
  control.nextWorkspaceChange = { status: 409, code: 'VERSION_CONFLICT' };
  control.workspaceList[1] = { ...teamWorkspace, name: 'Renamed elsewhere', version: 9 };
  await dialog.getByRole('button', { name: 'Disable the workspace' }).click();
  await expect(dialog.getByRole('alert')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('heading', { name: 'Renamed elsewhere', level: 1 })).toBeVisible();
});

test('a failed change keeps the form and can be retried', async ({ page }) => {
  await openTeam(page);
  await page.getByRole('button', { name: 'Rename' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Name').fill('Vision');
  await dialog.getByLabel('Reason').fill('Team renamed');
  control.nextWorkspaceChange = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await dialog.getByRole('button', { name: 'Rename' }).click();
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'Vision', level: 1 })).toBeVisible();
});

test('the members cannot load: the rest stays, with a retry', async ({ page }) => {
  control.membersProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await openTeam(page);
  await expect(page.getByRole('heading', { name: teamWorkspace.name, level: 1 })).toBeVisible();
  const retry = page.getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  control.membersProblem = null;
  await retry.click();
  await expect(page.getByRole('row', { name: new RegExp(plainMember.email) })).toBeVisible();
});

test('the list cannot load: offers a retry', async ({ page }) => {
  await page.goto('/users');
  control.workspacesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/users/workspaces');
  const retry = page.getByRole('main').getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  control.workspacesProblem = null;
  await retry.click();
  await expect(page.getByRole('link', { name: teamWorkspace.name })).toBeVisible();
});

test('says when there are none, and how to start', async ({ page }) => {
  control.workspaceList = [];
  await page.goto('/users/workspaces');
  await expect(page.getByText('No workspaces yet')).toBeVisible();
});

test('says when a workspace does not exist, with a way back', async ({ page }) => {
  await page.goto('/users/workspaces/6f1c7a1e-0000-4000-8000-0000000000ee');
  await expect(page.getByRole('heading', { name: 'Not found', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: 'All workspaces' }).click();
  await expect(page.getByRole('heading', { name: 'Workspaces', level: 1 })).toBeVisible();
});

test('the switcher follows a new workspace', async ({ page }) => {
  await page.goto('/users/workspaces#create');
  const dialog = page.getByRole('dialog', { name: 'Create a workspace' });
  await dialog.getByLabel('Name').fill('Speech');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Speech', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: /^Workspace: / }).click();
  await expect(page.getByRole('menuitem', { name: /Speech/ })).toBeVisible();
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto(`/users/workspaces/${teamWorkspace.id}`);
  await expect(page.getByRole('button', { name: 'Add a person' })).toHaveCount(0);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await openTeam(page);
    await expect(page.getByRole('button', { name: chinese['workspace.add.open'] })).toBeVisible();
    await expect(page.getByRole('heading', { name: chinese['workspace.members'] })).toBeVisible();
  });
});
