// The Nodes page: an administrator sees the fleet, finds a node, changes it and adds another.
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
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

const kubernetesNode = {
  ...node,
  execution_profile: 'kubernetes_node',
  capabilities: [
    'collect_facts.v1',
    'kubernetes.join.v1',
    'kubernetes.token.v1',
    'kubernetes.observe.v1',
  ],
} satisfies typeof node;

for (const locale of ['en', 'zh-CN'] as const) {
  test(`shows Kubernetes profiles on node and registration reads (${locale})`, async ({ page }) => {
    const zh = locale === 'zh-CN';
    control.preferences.locale = locale;
    const profile = zh ? chinese['nodes.profile.kubernetes_node'] : 'Kubernetes node';
    const waiting = {
      ...joinToken,
      execution_profile: 'kubernetes_node',
    } satisfies typeof joinToken;
    control.nodes = [kubernetesNode, offlineNode, disabledNode];
    control.joinTokens = [waiting];

    await page.goto('/nodes');
    await expect(row(page, node.name)).toContainText(profile);
    await expect(row(page, node.name)).toContainText(zh ? chinese['nodes.state.online'] : 'Online');
    await expect(row(page, offlineNode.name)).toContainText(
      zh ? chinese['nodes.profile.read_only'] : 'Read only',
    );
    const pending = page.getByRole('region', {
      name: zh ? chinese['nodes.pending.title'] : 'Waiting to join',
    });
    await expect(
      pending.getByRole('listitem').filter({ hasText: waiting.node_name }),
    ).toContainText(profile);

    await page
      .getByRole('button', {
        name: (zh ? chinese['nodes.detail.open'] : 'Open details of {name}').replace(
          '{name}',
          node.name,
        ),
      })
      .click();
    const drawer = page.getByRole('dialog', { name: node.name });
    await expect(drawer.getByText(profile, { exact: true })).toBeVisible();
    await drawer
      .getByRole('link', {
        name: zh ? chinese['resource.openPage'] : 'Open the full page',
      })
      .click();
    await expect(page).toHaveURL(new RegExp(`/nodes/${node.id}$`));
    await expect(page.getByRole('tabpanel').getByText(profile, { exact: true })).toBeVisible();
  });
}

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

  test('the add dialog names the systems that can join, and works without them', async ({
    page,
  }) => {
    await page.goto('/nodes#add');
    const dialog = page.getByRole('dialog', { name: 'Add a node' });
    await expect(dialog).toContainText('This release joins nodes running: Linux, x86-64.');
    await dialog.getByRole('link', { name: 'See what is supported' }).click();
    await expect(page.getByRole('heading', { name: 'What is supported', level: 1 })).toBeFocused();

    control.capabilitiesProblem = { status: 503, code: 'SERVICE_NOT_READY' };
    await page.goto('/nodes#add');
    const again = page.getByRole('dialog', { name: 'Add a node' });
    await expect(again.getByLabel('Node name')).toBeVisible();
    await expect(again).not.toContainText('This release joins');
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

  for (const locale of ['en', 'zh-CN'] as const) {
    test(`registers a Kubernetes node with its own Agent address (${locale})`, async ({
      page,
      context,
    }, testInfo) => {
      control.preferences.locale = locale;
      control.pollMilliseconds = 1_000;
      const copy = locale === 'en' ? english : chinese;
      const name = 'kube-edge-01';
      const agent = 'https://agents.example.internal:8443';
      await context.grantPermissions(['clipboard-read', 'clipboard-write']);
      await page.route('**/admin/cell-clusters', (route) =>
        route.fulfill({ json: { data: { items: [] } } }),
      );
      await page.goto('/nodes#add');
      const form = page.getByRole('dialog', { name: copy['nodes.add.title'] });
      const role = form.getByLabel(copy['nodes.add.role']);
      const option = role.getByRole('option', { name: copy['nodes.add.role.kubernetes_node'] });
      await expect(option).toBeEnabled();
      await role.selectOption('kubernetes_node');
      await form.getByLabel(copy['nodes.add.name']).fill(name);
      const address = form.getByLabel(copy['nodes.add.kubernetes.origin']);
      const proceed = form.getByRole('button', { name: copy['nodes.add.kubernetes.submit'] });
      await expect(proceed).toBeDisabled();
      for (const invalid of [
        'http://agent.internal',
        `${agent}/api`,
        `${agent}?`,
        'https://user@agent.internal',
      ]) {
        await address.fill(invalid);
        await expect(address).toHaveAttribute('aria-invalid', 'true');
        await expect(proceed).toBeDisabled();
      }
      await address.fill(`${agent}/`);
      await expect(form).toContainText(copy['nodes.add.kubernetes.originHint']);
      await expectAccessible(page);
      await testInfo.attach('registration-form', {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });
      const posted = page.waitForRequest(
        (request) => request.method() === 'POST' && request.url().endsWith('/node-join-tokens'),
      );
      await proceed.click();
      expect((await posted).postDataJSON()).toEqual({
        workspace_id: node.workspace_id,
        node_name: name,
        execution_profile: 'kubernetes_node',
      });
      const steps = page.getByRole('dialog', {
        name: copy['nodes.add.stepsTitle'].replace('{name}', name),
      });
      const commands = steps.locator('pre');
      await expect(commands).toHaveCount(3);
      await expect(steps).toContainText(copy['nodes.add.kubernetes.enrolled']);
      await expect(
        steps.locator('span[aria-hidden="true"]').filter({ hasText: copy['nodes.add.waiting'] }),
      ).toBeInViewport();
      const join = commands.nth(2);
      const script = await join.innerText();
      expect(JSON.parse(script.split('\n')[1] ?? '')).toEqual({
        control_origin: agent,
        ca_file: '/etc/ownsolid-agent/ca.pem',
        state_directory: '/var/lib/ownsolid-agent',
        disk_path: '/',
        join_token_file: '/etc/ownsolid-agent/join-token',
        execution_profile: 'kubernetes_node',
      });
      expect(script).toContain('sudo agent enroll --config /etc/ownsolid-agent/agent.json');
      expect(script).toContain('sudo agent run --config /etc/ownsolid-agent/agent.json');
      for (const command of await commands.all()) {
        await expect(command).not.toContainText('scripted-join-token');
        await expect(command).not.toContainText('docker');
      }
      await steps
        .getByRole('button', {
          name: copy['action.copyNamed'].replace('{name}', copy['nodes.add.step.joinLabel']),
        })
        .click();
      await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(script);
      await expectAccessible(page);
      await join.scrollIntoViewIfNeeded();
      await testInfo.attach('registration-commands', {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });

      control.nodes.unshift({
        ...kubernetesNode,
        id: '6f1c7a1e-0000-4000-8000-000000000034',
        name,
        created_at: '2026-11-06T08:00:01Z',
      });
      await expect(steps).toContainText(copy['nodes.add.joined'].replace('{name}', name));
      await steps.getByRole('button', { name: copy['nodes.add.openNode'] }).click();
      await expect(page.getByRole('dialog', { name })).toContainText(
        copy['nodes.profile.kubernetes_node'],
      );
    });
  }

  for (const { status, code, reason } of [
    { status: 404, code: 'RESOURCE_NOT_FOUND', reason: 'nodes.add.kubernetes.olderControl' },
    {
      status: 503,
      code: 'KUBERNETES_CELLS_UNCONFIGURED',
      reason: 'nodes.add.kubernetes.unconfigured',
    },
    {
      status: 409,
      code: 'KUBERNETES_CLUSTER_STORAGE_UNSUPPORTED',
      reason: 'nodes.add.kubernetes.storage',
    },
    { status: 403, code: 'ACCESS_DENIED', reason: 'nodes.add.kubernetes.permission' },
  ] as const) {
    test(`keeps Kubernetes visible with its reason for ${code}`, async ({ page }, testInfo) => {
      await page.route('**/admin/cell-clusters', (route) =>
        route.fulfill({ status, json: { code } }),
      );
      await page.goto('/nodes#add');
      const dialog = page.getByRole('dialog', { name: 'Add a node' });
      const role = dialog.getByLabel('What it will do');
      const kube = role.getByRole('option', { name: 'Run Kubernetes workloads' });
      await expect(kube).toBeDisabled();
      await expect(kube).toHaveAttribute('aria-disabled', 'true');
      await expect(role).toHaveAccessibleDescription(new RegExp(english[reason]));
      await expect(role.getByRole('option', { name: 'Report its facts' })).toBeEnabled();
      await expect(role.getByRole('option', { name: 'Serve models' })).toBeEnabled();
      if (status === 404) {
        await testInfo.attach('registration-unavailable', {
          body: await page.screenshot({ animations: 'disabled' }),
          contentType: 'image/png',
        });
      }
    });
  }

  test('can retry a failed Kubernetes check and explains a later token refusal', async ({
    page,
  }) => {
    let checks = 0;
    await page.route('**/admin/cell-clusters', (route) => {
      checks += 1;
      return checks === 1
        ? route.fulfill({ status: 503, json: { code: 'STORAGE_UNAVAILABLE' } })
        : route.fulfill({ json: { data: { items: [] } } });
    });
    control.nextMint = { status: 409, code: 'AGENT_PROFILE_REJECTED' };
    await page.goto('/nodes#add');
    const dialog = page.getByRole('dialog', { name: 'Add a node' });
    await expect(dialog).toContainText(english['nodes.add.kubernetes.checkFailed']);
    await dialog.getByRole('button', { name: 'Try again' }).click();
    const role = dialog.getByLabel('What it will do');
    await expect(role.getByRole('option', { name: 'Run Kubernetes workloads' })).toBeEnabled();
    await role.selectOption('kubernetes_node');
    await dialog.getByLabel('Node name').fill('kube-edge-01');
    await dialog.getByLabel('Agent HTTPS address').fill('https://agents.example.internal');
    await dialog.getByRole('button', { name: 'Issue join token' }).click();
    await expect(dialog.getByRole('alert')).toContainText('Kubernetes token refused');
    await expect(dialog.getByRole('alert')).toContainText('PostgreSQL');
    expect(control.joinTokens).toHaveLength(0);
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
