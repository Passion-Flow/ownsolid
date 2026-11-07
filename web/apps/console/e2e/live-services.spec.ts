// Model services on a real installation. The first flow joins a real agent with the configuration
// the console wrote and reaches the refusal a host with no GPU earns. The second joins a scripted
// node that reports a synthetic GPU, so a real control plane and database carry a deployment through
// its life; no model runs. Needs a ready model; docs/development/environment.md has the recipe.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Locator, type Page } from '@playwright/test';

const agentCommand = process.env.OWNSOLID_LIVE_SERVING_AGENT_COMMAND ?? '';
const scriptedCommand = process.env.OWNSOLID_LIVE_SCRIPTED_NODE_COMMAND ?? '';
const modelName = 'Qwen3-0.6B';
const stamp = Date.now();
const agentNode = `serve-real-${String(stamp)}`;
const scriptedNode = `serve-scripted-${String(stamp)}`;

test.skip(origin === '' || agentCommand === '' || scriptedCommand === '', 'needs serving nodes');
test.use(liveOptions);
test.describe.configure({ mode: 'serial' });

const signIn = async (page: Page, path: string) => {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
};

/** Adds a node that serves models, and gives what the dialog showed. */
async function addServingNode(
  page: Page,
  name: string,
): Promise<{ steps: Locator; token: string; config: string }> {
  const dialog = page.getByRole('dialog', { name: 'Add a node' });
  await dialog.getByLabel('Node name').fill(name);
  await dialog.getByLabel('What it will do').selectOption({ label: 'Serve models' });
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const steps = page.getByRole('dialog', { name: `Join ${name}` });
  await expect(steps).toContainText('sudo docker load -i ownsolid-serving.tar');
  const text = (await steps.textContent()) ?? '';
  const config = /\{"control_origin".*?\}\}/.exec(text)?.[0] ?? '';
  const token = (await steps.getByRole('status').first().locator('.font-mono').textContent()) ?? '';
  expect(config).toContain('"execution_profile":"docker_inference"');
  expect(token).not.toBe('');
  return { steps, token, config };
}

test('a real agent joins as the console says, and a host with no GPU is refused', async ({
  page,
}) => {
  test.setTimeout(5 * 60_000);
  await signIn(page, '/nodes#add');
  const { steps, token, config } = await addServingNode(page, agentNode);
  execFileSync('sh', ['-c', agentCommand, 'join', config], { input: token });
  await expect(steps.getByText(`${agentNode} has joined.`)).toBeVisible({ timeout: 60_000 });
  await page.goto('/services/new');
  await expect(page.getByRole('heading', { name: 'Deploy a model', level: 1 })).toBeVisible();
  await expect(page.getByText('Nothing here can take this model now')).toBeVisible({
    timeout: 60_000,
  });
  await expect(page.getByRole('link', { name: agentNode })).toBeVisible({ timeout: 60_000 });
  await expect(
    page.getByRole('listitem').filter({ hasText: agentNode }).getByText('No NVIDIA driver answers'),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
  await expectAccessible(page);
});

test('a deployment goes through its life on the real control plane', async ({ browser }) => {
  test.setTimeout(10 * 60_000);
  const context = await browser.newContext();
  const page = await context.newPage();
  const outside: string[] = [];
  context.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      outside.push(request.url());
    }
  });
  await signIn(page, '/nodes#add');
  const { steps, token } = await addServingNode(page, scriptedNode);
  execFileSync('sh', ['-c', scriptedCommand, 'join', scriptedNode], { input: token });
  await expect(steps.getByText(`${scriptedNode} has joined.`)).toBeVisible({ timeout: 60_000 });

  await page.goto('/services/new');
  const plan = page.getByText(`of ${scriptedNode}, with`);
  await expect(plan).toBeVisible({ timeout: 60_000 });
  await expect(page.getByText('What it takes')).toBeVisible();
  await expect(page.getByText(/^All of Synthetic GPU/)).toBeVisible();
  await expectAccessible(page);
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();

  const title = `${modelName} on ${scriptedNode}`;
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  await expect(page.getByRole('main').getByText('Starting').first()).toBeVisible();
  await expect(page.getByText('Yes, a test generation passed')).toBeVisible({ timeout: 60_000 });
  await expectAccessible(page);
  const address = page.url();

  await page.goto('/services');
  await expect(page.getByRole('row', { name: new RegExp(scriptedNode) })).toContainText('Running');
  await expect(page.getByRole('row', { name: new RegExp(scriptedNode) })).toContainText(
    '1 of 1 ready',
  );

  await page.goto(address);
  await page.getByRole('button', { name: 'Check' }).click();
  await expect(page.getByText(`Checking ${modelName}`)).toBeVisible();
  await expect(page.getByText('Yes, a test generation passed')).toBeVisible({ timeout: 60_000 });

  const stop = page.getByRole('button', { name: 'Stop', exact: true });
  await expect(stop).toBeVisible({ timeout: 60_000 });
  await stop.click();
  const stopping = page.getByRole('dialog', { name: `Stop ${modelName}` });
  await expect(stopping).toContainText('No gateway name sends traffic here');
  await stopping.getByRole('textbox').first().fill(modelName);
  await stopping.getByRole('button', { name: 'Stop deployment' }).click();
  const start = page.getByRole('button', { name: 'Start', exact: true });
  await expect(start).toBeVisible({ timeout: 60_000 });
  await start.click();
  await expect(page.getByText('Yes, a test generation passed')).toBeVisible({ timeout: 60_000 });

  await page.getByRole('tab', { name: 'Activity' }).click();
  await expect(page.getByRole('main').getByRole('table')).toBeVisible();
  await page.getByRole('tab', { name: 'Overview' }).click();

  const remove = page.getByRole('button', { name: 'Remove', exact: true });
  await expect(remove).toBeEnabled({ timeout: 60_000 });
  await remove.click();
  const removing = page.getByRole('dialog', { name: `Remove ${modelName}` });
  await removing.getByRole('textbox').first().fill(modelName);
  await removing.getByRole('button', { name: 'Remove deployment' }).click();
  await expect(page.getByRole('heading', { name: 'Model services', level: 1 })).toBeVisible();
  // The list follows the removal: the row shows it, then goes once the node confirms.
  const row = page.getByRole('row', { name: new RegExp(scriptedNode) });
  await expect(row).toContainText('Removing');
  await expect(row).toHaveCount(0, { timeout: 60_000 });
  expect(outside).toEqual([]);
  await context.close();
});
