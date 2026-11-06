// Global search: part of a name in the command menu finds nodes, people, tasks and workspaces, each
// opening its detail. One failed source holds back only itself, and a later answer always wins.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  adminUser,
  expectAccessible,
  finishedTask,
  memberUser,
  mockControlPlane,
  node,
  teamWorkspace,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page, type Route } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [adminUser, memberUser];
  control.tasks = [finishedTask];
});

async function openMenu(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
  await page.keyboard.press('Control+k');
}

async function search(page: Page, text: string) {
  await openMenu(page);
  await page.getByRole('combobox').fill(text);
}

const menuStatus = (page: Page) =>
  page.getByRole('dialog', { name: 'Search and commands' }).getByRole('status');
const row = (page: Page, name: string | RegExp) => page.getByRole('option', { name });

test('finds each kind of thing and says what it is', async ({ page }) => {
  await search(page, node.name.slice(2));
  await expect(row(page, `${node.name} Online Node`)).toBeVisible();
  await expect(row(page, /Collect node facts/)).toContainText(node.name);
  await expect(row(page, /Task$/)).toBeVisible();
  await expect(menuStatus(page)).toHaveText('2 results');

  await page.getByRole('combobox').fill('GRACE');
  await expect(row(page, 'Grace Hopper grace@example.test Person')).toBeVisible();

  await page.getByRole('combobox').fill(teamWorkspace.name.toUpperCase());
  await expect(row(page, `${teamWorkspace.name} Workspace`)).toBeVisible();
  await expectAccessible(page);
});

test('opens a node from the keyboard, even one beyond the first page of the list', async ({
  page,
}) => {
  const others = Array.from({ length: 200 }, (_, index) => ({
    ...node,
    id: `6f1c7a1e-0000-4000-8000-1${String(index).padStart(11, '0')}`,
    name: `other-${String(index)}`,
  }));
  control.nodes = [...others, node];
  control.tasks = [];
  await search(page, node.name);
  await expect(row(page, new RegExp(`^${node.name}`))).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp(`/nodes\\?open=${node.id}$`));
  await expect(page.getByRole('dialog').getByRole('heading', { name: node.name })).toBeVisible();
});

test('opens a person, a task and a workspace', async ({ page }) => {
  await search(page, 'grace');
  await row(page, /^Grace Hopper/).click();
  await expect(page).toHaveURL(new RegExp(`/users\\?open=${memberUser.id}$`));
  await expect(page.getByRole('dialog', { name: 'Grace Hopper' })).toBeVisible();

  await search(page, node.name);
  await row(page, /Task$/).click();
  await expect(page).toHaveURL(new RegExp(`/tasks/${finishedTask.task.id}$`));

  await search(page, teamWorkspace.name);
  await row(page, new RegExp(`^${teamWorkspace.name}`)).click();
  await expect(page).toHaveURL(new RegExp(`/users/workspaces/${teamWorkspace.id}$`));
});

test('a pasted task ID finds that task', async ({ page }) => {
  await search(page, finishedTask.task.id.toUpperCase());
  await expect(row(page, /Task$/)).toContainText(node.name);
  await page.getByRole('combobox').fill('6f1c7a1e-0000-4000-8000-ffffffffffff');
  await expect(menuStatus(page)).toHaveText('No results');
  await expect(page.getByRole('listbox').getByText('No results')).toBeVisible();
});

test('more than a few matches lead to the page that lists them all', async ({ page }) => {
  control.nodes = Array.from({ length: 7 }, (_, index) => ({
    ...node,
    id: `6f1c7a1e-0000-4000-8000-2${String(index).padStart(11, '0')}`,
    name: `rack-${String(index)}`,
  }));
  await search(page, 'rack');
  await expect(row(page, /^rack-/)).toHaveCount(5);
  await row(page, 'See all nodes matching rack').click();
  await expect(page).toHaveURL(/\/nodes\?name=rack$/);
  await expect(page.getByLabel('Filter by name')).toHaveValue('rack');
  await expect(page.getByRole('link', { name: 'rack-6' }).or(page.getByText('rack-6'))).toHaveCount(
    1,
  );
});

test('one source failing leaves the others, and it can be retried', async ({ page }) => {
  control.usersProblem = { status: 500, code: 'INTERNAL_ERROR' };
  await search(page, 'gr');
  control.nodes = [{ ...node, name: 'gr-node' }];
  await page.getByRole('combobox').fill('gra');
  await expect(row(page, 'People could not be searched. Select to try again.')).toBeVisible();
  await page.getByRole('combobox').fill('gr');
  await expect(row(page, /^gr-node/)).toBeVisible();
  await expect(row(page, 'People could not be searched. Select to try again.')).toBeVisible();

  control.usersProblem = null;
  await row(page, 'People could not be searched. Select to try again.').click();
  await expect(row(page, /^Grace Hopper/)).toBeVisible();
  await expect(page.getByRole('combobox')).toHaveValue('gr');
});

test('a source the person may not use is left out without a word', async ({ page }) => {
  control.usersProblem = { status: 403, code: 'ACCESS_DENIED' };
  await search(page, node.name);
  await expect(row(page, new RegExp(`^${node.name}`))).toBeVisible();
  await expect(page.getByText('could not be searched')).toHaveCount(0);
});

test('a late answer to an old search does not replace a newer one', async ({ page }) => {
  const held: Route[] = [];
  await page.route(/\/admin\/nodes\?(.*&)?q=old(&|$)/, (route) => {
    held.push(route);
  });
  control.nodes = [{ ...node, name: 'older-than-new' }];
  await search(page, 'old');
  await expect(menuStatus(page)).toHaveText('Searching');
  await page.getByRole('combobox').fill('older');
  await expect(row(page, /^older-than-new/)).toBeVisible();
  // The menu may already have cancelled it, which is as good.
  await Promise.all(
    held.map((route) =>
      route
        .fulfill({ json: { data: { items: [{ ...node, name: 'old-node' }], next_cursor: null } } })
        .catch(() => undefined),
    ),
  );
  await expect(row(page, /^old-node/)).toHaveCount(0);
  await expect(row(page, /^older-than-new/)).toBeVisible();
});

test('a slower answer goes below what the highlight is on', async ({ page }) => {
  const held: Route[] = [];
  await page.route(/\/admin\/nodes\?(.*&)?q=/, (route) => {
    held.push(route);
  });
  await search(page, node.name);
  const task = row(page, /Task$/);
  await expect(task).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => held.length).toBeGreaterThan(0);
  await Promise.all(
    held.map((route) => route.fulfill({ json: { data: { items: [node], next_cursor: null } } })),
  );
  await expect(row(page, new RegExp(`^${node.name}.*Node$`))).toBeVisible();
  await expect(task).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/tasks\//);
});

test('closing the menu forgets the search', async ({ page }) => {
  await search(page, 'grace');
  await expect(row(page, /^Grace Hopper/)).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('Control+k');
  await expect(page.getByRole('combobox')).toHaveValue('');
  await expect(row(page, /^Grace Hopper/)).toHaveCount(0);
  await expect(row(page, 'Keyboard shortcuts')).toBeVisible();
});

test('what the menu already has still matches, and nothing is asked before typing', async ({
  page,
}) => {
  const asked: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).searchParams.has('q')) asked.push(request.url());
  });
  await openMenu(page);
  await expect(row(page, 'Sign out')).toBeVisible();
  expect(asked).toEqual([]);
  await page.getByRole('combobox').fill('sign out');
  await expect(row(page, 'Sign out')).toBeVisible();
  await expect(row(page, /^Overview/)).toHaveCount(0);
});

test('a space alone asks nothing', async ({ page }) => {
  const asked: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).searchParams.has('q')) asked.push(request.url());
  });
  await search(page, '   ');
  await expect(row(page, 'Sign out')).toBeVisible();
  await expect(menuStatus(page)).not.toHaveText('Searching');
  expect(asked).toEqual([]);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('Chinese names are found, and the menu speaks Chinese', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    control.nodes = [{ ...node, name: '\u8ba1\u7b97\u8282\u70b9-01' }];
    await page.goto('/');
    await page.getByRole('main').waitFor();
    await page.keyboard.press('Control+k');
    await page.getByRole('combobox').fill('\u7b97\u8282');
    await expect(row(page, /^\u8ba1\u7b97\u8282\u70b9-01/)).toContainText(
      chinese['command.kind.node'],
    );
  });
});

test.describe('the pages search as the menu does', () => {
  test('tasks by the name of their node', async ({ page }) => {
    await page.goto('/tasks');
    await page.getByLabel('Filter by node name').fill(node.name.toUpperCase().slice(2));
    await expect(page).toHaveURL(/node=/);
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toBeVisible();
    await page.getByLabel('Filter by node name').fill('absent');
    await expect(page.getByText('No tasks match')).toBeVisible();
    await page.getByRole('button', { name: 'Show all tasks' }).click();
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toBeVisible();
  });

  test('workspaces by name, and an empty result says so', async ({ page }) => {
    await page.goto('/users/workspaces');
    await page.getByLabel('Filter by name').fill(teamWorkspace.name.toUpperCase());
    await expect(page).toHaveURL(/name=/);
    await expect(page.getByRole('link', { name: teamWorkspace.name })).toBeVisible();
    await expect(page.getByRole('row')).toHaveCount(2);
    await page.getByLabel('Filter by name').fill('absent');
    await expect(page.getByText('No workspaces match')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Create a workspace' })).toHaveCount(1);
    await page.getByRole('button', { name: 'Show all workspaces' }).click();
    await expect(page.getByRole('row')).not.toHaveCount(2);
  });

  test('people by part of an address or by name', async ({ page }) => {
    await page.goto('/users');
    await page.getByLabel('Filter by email or name').fill('hopper');
    await expect(page.getByText('grace@example.test')).toBeVisible();
    await expect(page.getByText(adminUser.email)).toHaveCount(0);
  });
});
