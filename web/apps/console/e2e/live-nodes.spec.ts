// Adds a real node from the console: the flow runs the commands the page shows, as written, on a
// disposable host, with the token the page showed, and then works with the node that joined. Skipped
// unless the environment says how; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { execSync } from 'node:child_process';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

// A shell on the host, reading its script from standard input.
const hostShell = process.env.OWNSOLID_LIVE_NODE_SHELL ?? '';
const kubernetesHostShell = process.env.OWNSOLID_LIVE_KUBERNETES_NODE_SHELL ?? '';
const kubernetesAgentOrigin = process.env.OWNSOLID_LIVE_KUBERNETES_AGENT_ORIGIN ?? '';

test.use(liveOptions);

const onHost = (script: string, shell = hostShell): string =>
  execSync(shell, { input: script, encoding: 'utf8', timeout: 60_000 });

const signIn = async (page: Page) => {
  await page.goto('/nodes');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Nodes', level: 1 })).toBeVisible();
};

test('adds a node with the commands on the page and works with it', async ({ page }) => {
  test.skip(origin === '' || hostShell === '', 'needs a disposable host');
  test.setTimeout(240_000);
  const problems: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
      problems.push(message.text());
    }
  });
  page.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      problems.push(`outside request: ${request.url()}`);
    }
  });
  const name = `live-${String(Date.now())}`;
  const renamed = `${name}-renamed`;
  await signIn(page);
  await expectAccessible(page);

  await page.getByRole('main').getByRole('button', { name: 'Add node' }).first().click();
  await page.getByLabel('Node name').fill(name);
  await page.getByRole('button', { name: 'Continue' }).click();
  const dialog = page.getByRole('dialog', { name: `Join ${name}` });
  const commands = dialog.locator('pre');
  await expect(commands).toHaveCount(3);
  await expectAccessible(page);

  const token = await dialog.locator('.font-mono').first().innerText();
  expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
  const [directories, store, join] = await Promise.all([
    commands.nth(0).innerText(),
    commands.nth(1).innerText(),
    commands.nth(2).innerText(),
  ]);
  expect(`${directories}${store}${join}`).not.toContain(token);

  // The token is listed as waiting, and a second one for another name can be revoked.
  onHost(directories);
  onHost(
    `install -m 0755 /opt/agent /usr/local/bin/agent\ninstall -m 0600 /opt/ca.pem /etc/ownsolid-agent/ca.pem\n`,
  );
  onHost(`printf '%s\\n' '${token}' | ${store}\n`);
  const lines = join.split('\n');
  const run = lines.pop() ?? '';
  onHost(`${lines.join('\n')}\nnohup ${run} >/var/log/agent.log 2>&1 &\n`);

  await expect(dialog).toContainText(`${name} has joined`, { timeout: 60_000 });
  await dialog.getByRole('button', { name: 'Open the node' }).click();
  const drawer = page.getByRole('dialog', { name });
  await expect(drawer).toContainText('Read only');
  await expect(drawer).toContainText('Online');
  await expectAccessible(page);

  // Facts the real agent read, and a real task.
  await drawer.getByRole('button', { name: 'Collect facts' }).click();
  await expect(drawer.getByRole('link', { name: 'Collect node facts' }).first()).toBeVisible();
  await expect(drawer.getByText('Succeeded').first()).toBeVisible({ timeout: 60_000 });
  await page.keyboard.press('Escape');

  // The filters and their counts come from the server.
  await expect(
    page.getByRole('group', { name: 'Show nodes' }).getByRole('button', { name: /^All \d+$/ }),
  ).toBeVisible();
  const row = page.getByRole('row', { name: new RegExp(name) });
  await page.getByLabel('Filter by name').fill(name);
  await expect(row).toContainText('Online');
  await page
    .getByRole('group', { name: 'Show nodes' })
    .getByRole('button', { name: /^Offline/ })
    .click();
  await expect(page.getByText('No nodes match')).toBeVisible();
  await page
    .getByRole('group', { name: 'Show nodes' })
    .getByRole('button', { name: /^Online/ })
    .click();
  await expect(row).toBeVisible();

  // A link opens the same list.
  await page.reload();
  await expect(row).toContainText('Online');
  await expect(page.getByLabel('Filter by name')).toHaveValue(name);

  // Disabling is undone.
  await page.getByRole('button', { name: `Open details of ${name}` }).click();
  await drawer.getByRole('button', { name: 'Disable' }).click();
  await expect(row).toContainText('Disabled');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(row).toContainText('Online');

  // A rename, on a node the agent keeps reporting from.
  await page.getByRole('button', { name: `Open details of ${name}` }).click();
  await drawer.getByRole('button', { name: 'Rename' }).click();
  await page
    .getByRole('dialog', { name: `Rename ${name}` })
    .getByLabel('Node name')
    .fill(renamed);
  await page
    .getByRole('dialog', { name: `Rename ${name}` })
    .getByRole('button', { name: 'Rename' })
    .click();
  await expect(page.getByRole('status').filter({ hasText: `Renamed to ${renamed}` })).toBeVisible();
  await page.getByLabel('Filter by name').fill(renamed);
  const renamedRow = page.getByRole('row', { name: new RegExp(renamed) });
  await expect(renamedRow).toContainText('Online');

  // A pacing change reaches the real agent with its next report, and so does its undo.
  const pacingLog = () => onHost('grep agent_pacing_changed /var/log/agent.log || true\n');
  await page.getByRole('link', { name: 'Agent pacing' }).click();
  await expect(page.getByRole('heading', { name: 'Agent pacing', level: 1 })).toBeVisible();
  const interval = page.getByLabel('Heartbeat interval (seconds)');
  const before = await interval.inputValue();
  await expectAccessible(page);
  await interval.fill('6');
  await page.getByLabel('Reason').fill('Live check of agent pacing');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Pacing saved' })).toBeVisible();
  await expect.poll(pacingLog, { timeout: 60_000 }).toContain('heartbeat_seconds=6 ');
  // Put back what was there; the undo itself is a mocked flow, since its toast is short-lived.
  await interval.fill(before);
  await page.getByLabel('Reason').fill('Live check done');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Pacing saved' })).toBeVisible();
  await expect
    .poll(() => pacingLog().trim().split('\n').at(-1) ?? '', { timeout: 60_000 })
    .toContain(`heartbeat_seconds=${before} `);
  await page.getByRole('navigation').getByRole('link', { name: 'Nodes', exact: true }).click();
  await page.getByLabel('Filter by name').fill(renamed);

  // A token nobody used is listed and revoked.
  const spare = `${name}-spare`;
  await page.getByRole('main').getByRole('button', { name: 'Add node' }).first().click();
  await page.getByLabel('Node name').fill(spare);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page
    .getByRole('dialog', { name: `Join ${spare}` })
    .getByRole('button', { name: 'Close' })
    .last()
    .click();
  const waiting = page.getByRole('region', { name: 'Waiting to join' });
  await waiting.getByRole('button', { name: `Revoke ${spare}` }).click();
  await expect(
    page.getByRole('status').filter({ hasText: `The token for ${spare} no longer works` }),
  ).toBeVisible();

  // Removal needs the name typed, and the node leaves the list.
  await page.getByRole('button', { name: `Open details of ${renamed}` }).click();
  await page.getByRole('dialog', { name: renamed }).getByRole('button', { name: 'Remove' }).click();
  const confirm = page.getByRole('dialog', { name: 'Remove this node' });
  await confirm.getByRole('textbox').first().fill(renamed);
  await confirm.getByRole('button', { name: 'Remove the node' }).click();
  await expect(page.getByRole('status').filter({ hasText: `${renamed} removed` })).toBeVisible();
  await expect(renamedRow).toHaveCount(0);
  await page.getByRole('combobox', { name: 'Status' }).selectOption('removed');
  await expect(renamedRow).toContainText('Removed');
  await expectAccessible(page);
  expect(problems).toEqual([]);
});

test('registers a Kubernetes Agent from a private unicast address', async ({ page }, testInfo) => {
  test.skip(
    origin === '' || kubernetesHostShell === '' || kubernetesAgentOrigin === '',
    'needs a disposable Kubernetes Agent host and its Control HTTPS origin',
  );
  test.setTimeout(120_000);
  const name = `live-kube-${String(Date.now())}`;
  await signIn(page);
  await page.getByRole('main').getByRole('button', { name: 'Add node' }).first().click();
  const form = page.getByRole('dialog', { name: 'Add a node' });
  await form.getByLabel('Node name').fill(name);
  const role = form.getByLabel('What it will do');
  await expect(role.getByRole('option', { name: 'Run Kubernetes workloads' })).toBeEnabled();
  await role.selectOption('kubernetes_node');
  await form.getByLabel('Agent HTTPS address').fill(kubernetesAgentOrigin);
  await form.getByRole('button', { name: 'Issue join token' }).click();

  const steps = page.getByRole('dialog', { name: `Join ${name}` });
  const commands = steps.locator('pre');
  await expect(commands).toHaveCount(3);
  await expect(steps).toContainText('Waiting for the node to join');
  const secret = steps.locator('.font-mono').first();
  const token = await secret.innerText();
  expect(/^[A-Za-z0-9_-]{43}$/.test(token)).toBe(true);
  const [directories, store, join] = await Promise.all([
    commands.nth(0).innerText(),
    commands.nth(1).innerText(),
    commands.nth(2).innerText(),
  ]);
  const lines = join.split('\n');
  const run = lines.pop();
  expect(run).toBe('sudo agent run --config /etc/ownsolid-agent/agent.json');
  onHost(
    `set -eu
${directories}
install -m 0755 /opt/agent /usr/local/bin/agent
install -m 0600 /opt/ca.pem /etc/ownsolid-agent/ca.pem
printf '%s\\n' '${token}' | ${store}
${lines.join('\n')}
nohup ${run} >/var/log/agent.log 2>&1 &
`,
    kubernetesHostShell,
  );

  await expect(steps).toContainText(`${name} has joined`, { timeout: 60_000 });
  await testInfo.attach('unicast-registration-joined', {
    body: await page.screenshot({ animations: 'disabled', mask: [secret] }),
    contentType: 'image/png',
  });
  await steps.getByRole('button', { name: 'Open the node' }).click();
  const node = page.getByRole('dialog', { name });
  await expect(node).toContainText('Kubernetes node');
  await expect(node).toContainText('Online');
  await node.getByRole('button', { name: 'Collect facts' }).click();
  await expect(node.getByText('Succeeded').first()).toBeVisible({ timeout: 60_000 });
});

test('issues a 48-node CSV and observes real Agent enrollment', async ({ page }, testInfo) => {
  test.skip(origin === '' || hostShell === '', 'needs a disposable host and Control');
  test.setTimeout(240_000);
  const outside = new Set<string>();
  page.on('request', (request) => {
    if (!request.url().startsWith(origin + '/') && !request.url().startsWith('blob:')) {
      outside.add(new URL(request.url()).origin);
    }
  });
  await signIn(page);
  await page.getByRole('link', { name: 'Register from CSV' }).click();
  const prefix = `live-bulk-${Date.now()}`;
  const rows = Array.from(
    { length: 48 },
    (_, index) =>
      `${prefix}-${String(index + 1).padStart(2, '0')},${index < 24 ? 'Rack A' : 'Rack B'}`,
  );
  await page.locator('#enrollment-csv').setInputFiles({
    name: 'bulk-enrollment.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(['node_name,rack', ...rows].join('\n')),
  });
  await page.getByRole('button', { name: 'Issue tokens', exact: true }).click();
  await expect(page.getByText('0 of 24 joined · 24 awaiting join', { exact: true })).toHaveCount(
    2,
    {
      timeout: 60_000,
    },
  );
  const first = page.getByRole('listitem').filter({ hasText: `${prefix}-01` });
  const token = await first.locator('.font-mono').innerText();
  expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
  await page.getByText('Offline install commands', { exact: true }).click();
  const commands = page.getByRole('main').locator('pre');
  await expect(commands).toHaveCount(3);
  const [directories, store, join] = await commands.allTextContents();
  expect(`${directories}${store}${join}`).not.toContain(token);
  onHost(directories ?? '');
  onHost(
    'install -m 0755 /opt/agent /usr/local/bin/agent\ninstall -m 0600 /opt/ca.pem /etc/ownsolid-agent/ca.pem\n',
  );
  onHost(`printf '%s\\n' '${token}' | ${store}\n`);
  const lines = (join ?? '').split('\n');
  const run = lines.pop() ?? '';
  onHost(`${lines.join('\n')}\nnohup ${run} >/var/log/agent.log 2>&1 &\n`);

  await expect(first.getByRole('link', { name: 'Open node' })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByText('1 of 24 joined · 23 awaiting join', { exact: true })).toBeVisible();
  await page.getByText('Offline install commands', { exact: true }).click();
  await page.getByLabel('Row status', { exact: true }).selectOption('consumed');
  await expectAccessible(page);
  await testInfo.attach('bulk-enrollment-real-agent', {
    body: await page.screenshot({ fullPage: true }),
    contentType: 'image/png',
  });
  await first.getByRole('link', { name: 'Open node' }).click();
  await expect(page.getByRole('heading', { name: `${prefix}-01`, level: 1 })).toBeVisible();
  expect([...outside]).toEqual([]);
});
