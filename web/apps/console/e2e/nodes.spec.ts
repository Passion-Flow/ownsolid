// The Nodes page: an administrator sees the fleet, finds a node, changes it and adds another.
import {
  disabledNode,
  expectAccessible,
  joinToken,
  mockControlPlane,
  node,
  offlineNode,
  runningTask,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [{ ...node }, { ...offlineNode }, { ...disabledNode }];
});

const row = (page: Page, name: string) => page.getByRole('row', { name: new RegExp(name) });
const open = (page: Page, name: string) =>
  page.getByRole('button', { name: `Open details of ${name}` }).click();

test.describe('the list', () => {
  test('is reached from the navigation and shows each node as it stands', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Infrastructure', exact: true }).click();
    await page.getByRole('link', { name: 'Nodes', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Nodes', level: 1 })).toBeFocused();
    await expect(row(page, 'gpu-a02')).toContainText('Online');
    await expect(row(page, 'gpu-a02')).toContainText('ubuntu 24.04');
    await expect(row(page, 'gpu-a02')).toContainText('128 CPUs');
    await expect(row(page, 'gpu-a03')).toContainText('Offline');
    await expect(row(page, 'cpu-s01')).toContainText('Disabled');
  });

  test('says how to begin when there are no nodes', async ({ page }) => {
    control.nodes = [];
    await page.goto('/nodes');
    await expect(page.getByText('No nodes yet')).toBeVisible();
    await page.getByRole('main').getByRole('button', { name: 'Add node' }).last().click();
    await expect(page.getByRole('dialog', { name: 'Add a node' })).toBeVisible();
  });

  test('filters on the server, shows the counts and keeps the filter in the link', async ({
    page,
  }) => {
    await page.goto('/nodes');
    const filter = page.getByRole('group', { name: 'Show nodes' });
    await expect(filter.getByRole('button', { name: 'All 3' })).toBeVisible();
    await filter.getByRole('button', { name: 'Offline 1' }).click();
    await expect(page).toHaveURL(/connectivity=offline/);
    await expect(row(page, 'gpu-a03')).toBeVisible();
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
    await page.reload();
    await expect(row(page, 'gpu-a03')).toBeVisible();
    await expect(row(page, 'gpu-a02')).toHaveCount(0);

    await filter.getByRole('button', { name: 'Not responding 0' }).click();
    await expect(page.getByText('No nodes match')).toBeVisible();
    await page.getByRole('button', { name: 'Clear the filters' }).click();
    await expect(row(page, 'gpu-a02')).toBeVisible();
  });

  test('a tile is a figure and the filter that shows those rows', async ({ page }) => {
    await page.goto('/nodes');
    const group = page.getByRole('group', { name: 'Show nodes' });
    const offline = group.getByRole('button', { name: 'Offline' });
    await expect(offline).toHaveText(/1$/);
    await expect(group.getByRole('button', { name: 'All' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await offline.click();
    await expect(page).toHaveURL(/connectivity=offline/);
    await expect(offline).toHaveAttribute('aria-pressed', 'true');
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
    await expectAccessible(page);

    // The address opens the same view, and a second press goes back to everything.
    await page.reload();
    await expect(offline).toHaveAttribute('aria-pressed', 'true');
    await offline.click();
    await expect(page).not.toHaveURL(/connectivity=/);
    await expect(row(page, 'gpu-a02')).toBeVisible();
    await expect(group.getByRole('button', { name: 'All' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  test('tiles still filter when the figures cannot be read', async ({ page }) => {
    await page.route('**/admin/nodes/summary', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/problem+json',
        body: JSON.stringify({ code: 'INTERNAL_ERROR', title: 'x', status: 500 }),
      }),
    );
    await page.goto('/nodes');
    const offline = page.getByRole('group', { name: 'Show nodes' }).getByRole('button', {
      name: 'Offline',
    });
    await expect(offline).toHaveText('Offline');
    await offline.click();
    await expect(page).toHaveURL(/connectivity=offline/);
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
  });

  test('filters by name and by status', async ({ page }) => {
    await page.goto('/nodes');
    await page.getByLabel('Filter by name').fill('GPU');
    await expect(page).toHaveURL(/name=GPU/);
    await expect(row(page, 'cpu-s01')).toHaveCount(0);
    await expect(row(page, 'gpu-a02')).toBeVisible();
    await page.getByLabel('Filter by name').fill('');
    await page.getByRole('combobox', { name: 'Status' }).selectOption('disabled');
    await expect(row(page, 'cpu-s01')).toBeVisible();
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
  });

  test('keeps removed nodes out unless asked for', async ({ page }) => {
    control.nodes = [{ ...node }, { ...offlineNode, status: 'removed' }];
    await page.goto('/nodes');
    await expect(row(page, 'gpu-a02')).toBeVisible();
    await expect(row(page, 'gpu-a03')).toHaveCount(0);
    await page.getByRole('combobox', { name: 'Status' }).selectOption('removed');
    await expect(row(page, 'gpu-a03')).toContainText('Removed');
    await open(page, 'gpu-a03');
    const drawer = page.getByRole('dialog', { name: 'gpu-a03' });
    await expect(drawer.getByRole('button', { name: 'Disable' })).toHaveCount(0);
    await expect(drawer.getByRole('button', { name: 'Remove' })).toHaveCount(0);
  });

  test('offers a retry when it cannot load, and keeps the rows when a poll fails', async ({
    page,
  }) => {
    control.nodesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/nodes');
    await page.getByRole('button', { name: 'Try again' }).waitFor();
    control.nodesProblem = null;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(row(page, 'gpu-a02')).toBeVisible();

    control.pollMilliseconds = 1_000;
    await page.reload();
    await expect(row(page, 'gpu-a02')).toBeVisible();
    control.nodesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
    await expect(row(page, 'gpu-a02')).toBeVisible();
  });

  test('reads the fleet in the background, so an open page does not hold the session', async ({
    page,
  }) => {
    control.pollMilliseconds = 1_000;
    await page.goto('/nodes');
    await expect.poll(() => control.nodePolls.length).toBeGreaterThan(1);
    expect(control.nodePolls.every(Boolean)).toBe(true);
  });

  test('is not part of the portal', async ({ page }) => {
    control.audience = 'portal';
    await page.goto('/nodes');
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
  });
});

test.describe('a node', () => {
  test('opens with its facts and tasks, from a link too', async ({ page }) => {
    control.tasks = [{ ...runningTask, task: { ...runningTask.task, node_id: node.id } }];
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    const drawer = page.getByRole('dialog', { name: 'gpu-a02' });
    await expect(drawer).toContainText('gpu-a02.lab.internal');
    await expect(drawer).toContainText('2.5 TB free of 4 TB at /');
    await expect(drawer).toContainText('Research Lab');
    await expect(drawer).toContainText('Read only');
    await expect(drawer.getByRole('link', { name: 'Collect node facts' })).toBeVisible();
    await expect(page).toHaveURL(/open=/);
    await page.reload();
    await expect(page.getByRole('dialog', { name: 'gpu-a02' })).toBeVisible();
  });

  test('from a link is found even when the list does not hold it', async ({ page }) => {
    await page.goto(`/nodes?connectivity=offline&open=${node.id}`);
    await expect(page.getByRole('dialog', { name: 'gpu-a02' })).toBeVisible();
  });

  test('that does not exist says so', async ({ page }) => {
    await page.goto('/nodes?open=6f1c7a1e-0000-4000-8000-000000000099');
    await expect(page.getByRole('dialog', { name: 'Node details' })).toContainText('Not found');
  });

  test('that went quiet says what to check', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a03');
    const drawer = page.getByRole('dialog', { name: 'gpu-a03' });
    await expect(drawer).toContainText('Last heard from');
    await expect(drawer).toContainText('the agent is running');
  });

  test('collects its facts again and shows the task', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    const drawer = page.getByRole('dialog', { name: 'gpu-a02' });
    await expect(drawer).toContainText('This node has run no tasks yet');
    await drawer.getByRole('button', { name: 'Collect facts' }).click();
    await expect(drawer.getByRole('link', { name: 'Collect node facts' })).toBeVisible();
    await expect(drawer).toContainText('The node was asked to read its facts again');
    expect(control.taskStartKeys).toHaveLength(1);
  });

  test('is disabled at once and can be put back', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    const drawer = page.getByRole('dialog', { name: 'gpu-a02' });
    await drawer.getByRole('button', { name: 'Disable' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'gpu-a02 disabled' })).toBeVisible();
    await expect(row(page, 'gpu-a02')).toContainText('Disabled');
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect(row(page, 'gpu-a02')).toContainText('Online');
    expect(control.nodes[0]?.status).toBe('active');
    expect(control.nodeReasons).toEqual(['Disabled from the console', 'Undone from the console']);
  });

  test('that is disabled offers to enable it', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'cpu-s01');
    const drawer = page.getByRole('dialog', { name: 'cpu-s01' });
    await expect(drawer.getByRole('button', { name: 'Collect facts' })).toHaveCount(0);
    await drawer.getByRole('button', { name: 'Enable' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'cpu-s01 enabled' })).toBeVisible();
    await expect(row(page, 'cpu-s01')).toContainText('Online');
  });

  test('is renamed, and a taken name is explained', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    await page.getByRole('button', { name: 'Rename' }).click();
    const dialog = page.getByRole('dialog', { name: 'Rename gpu-a02' });
    await expect(dialog.getByRole('button', { name: 'Rename' })).toBeDisabled();
    control.nextNodeChange = { status: 409, code: 'NODE_NAME_EXISTS' };
    await dialog.getByLabel('Node name').fill('gpu-a09');
    await dialog.getByRole('button', { name: 'Rename' }).click();
    await expect(dialog).toContainText('This name is taken');
    await dialog.getByRole('button', { name: 'Rename' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Renamed to gpu-a09' })).toBeVisible();
    await expect(row(page, 'gpu-a09')).toBeVisible();
  });

  test('changed by someone else is explained', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    control.nextNodeChange = { status: 409, code: 'VERSION_CONFLICT' };
    await page.getByRole('dialog').getByRole('button', { name: 'Disable' }).click();
    await expect(page.getByRole('dialog')).toContainText('This was changed by someone else');
  });

  test('is removed only after its name is typed', async ({ page }) => {
    await page.goto('/nodes');
    await open(page, 'gpu-a02');
    await page.getByRole('button', { name: 'Remove' }).click();
    const dialog = page.getByRole('dialog', { name: 'Remove this node' });
    const submit = dialog.getByRole('button', { name: 'Remove the node' });
    await expect(submit).toBeDisabled();
    await dialog.getByRole('textbox').first().fill('gpu-a02');
    await expect(submit).toBeEnabled();
    await submit.click();
    await expect(page.getByRole('status').filter({ hasText: 'gpu-a02 removed' })).toBeVisible();
    await expect(row(page, 'gpu-a02')).toHaveCount(0);
    expect(control.nodeReasons).toEqual(['No longer needed']);
  });
});

test.describe('several nodes', () => {
  test('are disabled together, and undone together', async ({ page }) => {
    await page.goto('/nodes');
    await page.getByRole('checkbox', { name: 'Select gpu-a02' }).check();
    await page.getByRole('checkbox', { name: 'Select gpu-a03' }).check();
    const bar = page.getByRole('toolbar');
    await bar.getByRole('button', { name: 'Disable' }).click();
    await expect(page.getByRole('status').filter({ hasText: '2 nodes disabled' })).toBeVisible();
    expect(control.nodes.filter((item) => item.status === 'disabled')).toHaveLength(3);
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect(row(page, 'gpu-a02')).toContainText('Online');
    await expect(row(page, 'gpu-a03')).toContainText('Offline');
  });

  test('keep the ones that failed selected and can try again', async ({ page }) => {
    await page.goto('/nodes');
    await page.getByRole('checkbox', { name: 'Select all' }).check();
    control.nextNodeChange = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.getByRole('toolbar').getByRole('button', { name: 'Disable' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: '1 disabled, 1 refused' }),
    ).toBeVisible();
    await expect(page.getByRole('toolbar')).toContainText('1 selected');
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('status').filter({ hasText: '1 nodes disabled' })).toBeVisible();
  });

  test('collect their facts together', async ({ page }) => {
    await page.goto('/nodes');
    await page.getByRole('checkbox', { name: 'Select gpu-a02' }).check();
    await page.getByRole('checkbox', { name: 'Select cpu-s01' }).check();
    await page.getByRole('toolbar').getByRole('button', { name: 'Collect facts' }).click();
    // A disabled node runs nothing, so only the working one is asked.
    await expect(page.getByRole('status').filter({ hasText: '1 nodes were asked' })).toBeVisible();
    expect(control.taskStartKeys).toHaveLength(1);
  });
});

test.describe('adding a node', () => {
  const add = async (page: Page, name: string) => {
    await page.goto('/nodes');
    await page.getByRole('button', { name: 'Add node' }).click();
    const dialog = page.getByRole('dialog', { name: 'Add a node' });
    await expect(dialog.getByRole('button', { name: 'Continue' })).toBeDisabled();
    await dialog.getByLabel('Node name').fill(name);
    await dialog.getByRole('button', { name: 'Continue' }).click();
  };

  test('shows the token once, keeps it out of the commands and sees the node join', async ({
    page,
  }) => {
    control.pollMilliseconds = 1_000;
    await add(page, 'edge-e02');
    const dialog = page.getByRole('dialog', { name: 'Join edge-e02' });
    await expect(dialog).toContainText('scripted-join-token-only-shown-once-0123456');
    await expect(dialog).toContainText('It is shown only once');
    const commands = dialog.locator('pre');
    await expect(commands).toHaveCount(3);
    for (const block of await commands.all()) {
      await expect(block).not.toContainText('scripted-join-token');
    }
    await expect(commands.nth(2)).toContainText(new URL(page.url()).origin);
    await expect(commands.nth(2)).toContainText('agent enroll --config');
    await expect(dialog).toContainText('Waiting for the node to join');

    control.nodes.unshift({
      ...node,
      id: '6f1c7a1e-0000-4000-8000-000000000034',
      name: 'edge-e02',
      created_at: '2026-11-06T08:00:01Z',
    });
    await expect(dialog).toContainText('edge-e02 has joined');
    await dialog.getByRole('button', { name: 'Open the node' }).click();
    await expect(page.getByRole('dialog', { name: 'edge-e02' })).toBeVisible();
  });

  test('explains a refusal and can try again', async ({ page }) => {
    control.nextMint = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await add(page, 'edge-e02');
    const dialog = page.getByRole('dialog', { name: 'Add a node' });
    await dialog.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('dialog', { name: 'Join edge-e02' })).toBeVisible();
  });

  test('a token not used is listed and can be revoked', async ({ page }) => {
    control.joinTokens = [{ ...joinToken }];
    await page.goto('/nodes');
    const waiting = page.getByRole('region', { name: 'Waiting to join' });
    await expect(waiting).toContainText('edge-e01');
    await waiting.getByRole('button', { name: 'Revoke edge-e01' }).click();
    await expect(
      page.getByRole('status').filter({ hasText: 'The token for edge-e01 no longer works' }),
    ).toBeVisible();
    await expect(waiting).toHaveCount(0);
  });

  test('a token that cannot be revoked says why, and the list catches up', async ({ page }) => {
    control.joinTokens = [{ ...joinToken }];
    await page.goto('/nodes');
    control.nextRevoke = { status: 409, code: 'NODE_JOIN_STATE_CONFLICT' };
    await page.getByRole('button', { name: 'Revoke edge-e01' }).click();
    await expect(page.getByText('This token can no longer be revoked')).toBeVisible();
  });
});
