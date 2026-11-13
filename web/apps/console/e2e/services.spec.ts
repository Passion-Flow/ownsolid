// Model services: seeing how deployments are doing, deploying a model with the cost shown first,
// and what happens when any of that is refused (docs/research/model-services.md).
import {
  deploymentAlias,
  deploymentKey,
  failedDeployment,
  freeGpu,
  mockControlPlane,
  node,
  openPlacement,
  readyModel,
  runningDeployment,
  smallerGpu,
  staleDeployment,
  stalePlacement,
  stoppedDeployment,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

let control: ControlPlane;

const title = `${readyModel.name} on ${node.name}`;

const servingNode: typeof node = {
  ...node,
  execution_profile: 'docker_inference',
  capabilities: ['collect_facts.v1', 'inference.observe.v1'],
};

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [node];
  control.models = [readyModel];
});

test('is reached from the navigation, and says what it is for when empty', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Models', exact: true }).click();
  await page.getByRole('navigation').getByRole('link', { name: 'Model services' }).click();
  await expect(page.getByRole('heading', { name: 'Model services', level: 1 })).toBeFocused();
  await expect(page.getByRole('heading', { name: 'Nothing deployed' })).toBeVisible();
  await page.getByRole('main').getByRole('link', { name: 'Deploy a model' }).first().click();
  await expect(page.getByRole('heading', { name: 'Deploy a model', level: 1 })).toBeVisible();
});

test('opens the flow from the d shortcut', async ({ page }) => {
  await page.goto('/services');
  await expect(page.getByRole('heading', { name: 'Nothing deployed' })).toBeVisible();
  await page.keyboard.press('d');
  await expect(page.getByRole('heading', { name: 'Deploy a model', level: 1 })).toBeVisible();
});

test('tiles count and filter, from the address, and name what hid everything', async ({ page }) => {
  control.deployments = [runningDeployment, failedDeployment, staleDeployment, stoppedDeployment];
  await page.goto('/services');
  const tiles = page.getByRole('group', { name: 'Filter by state' });
  await expect(tiles.getByRole('button', { name: /Degraded/ })).toContainText('2');
  await tiles.getByRole('button', { name: /Degraded/ }).click();
  await expect(page).toHaveURL(/phase=degraded/);
  await expect(page.getByRole('row')).toHaveCount(3);
  await expect(page.getByRole('row', { name: /Failed/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Not reporting/ })).toBeVisible();
  await page.getByRole('searchbox', { name: 'Search deployments' }).fill('nothing like this');
  await expect(page.getByRole('heading', { name: 'No deployment matches' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('row')).toHaveCount(5);
});

test('says traffic is not collected rather than showing a number', async ({ page }) => {
  control.deployments = [runningDeployment];
  await page.goto('/services');
  await expect(page.getByRole('row', { name: /1 of 1 ready/ })).toBeVisible();
  await expect(page.getByText('Requests and latency are not collected yet')).toBeVisible();
});

test('a failed read says what happened and retries', async ({ page }) => {
  control.deploymentsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/services');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.deploymentsProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'Nothing deployed' })).toBeVisible();
});

test('previews a deployment in a drawer, and opens its own page', async ({ page }) => {
  control.deployments = [runningDeployment];
  await page.goto('/services');
  await page.getByRole('button', { name: `Show ${title}` }).click();
  const drawer = page.getByRole('dialog', { name: title });
  await expect(drawer).toContainText('Yes, a test generation passed');
  await drawer.getByRole('link', { name: 'Open the full page' }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
});

test('deploys from a model, stating the cost before the commit', async ({ page }) => {
  await page.goto(`/library/${readyModel.id}`);
  await page.getByRole('link', { name: 'Deploy a model' }).click();
  await expect(page).toHaveURL(new RegExp(`model=${readyModel.id}`));
  await expect(page.getByText(`On ${freeGpu.name} of ${node.name}`)).toBeVisible();
  await expect(page.getByText(`All of ${freeGpu.name}`)).toBeVisible();
  await expect(page.getByText('its other GPUs (1) stay idle')).toBeVisible();
  await expect(page.getByText('0 eligible GPUs on other nodes')).toBeVisible();
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  await expect(page.getByRole('main').getByText('Starting').first()).toBeVisible();
  expect(control.deployments[0]?.gpu_uuid).toBe(freeGpu.uuid);
});

test('lets a person choose another GPU, and keeps the choice in the address', async ({ page }) => {
  await page.goto('/services/new');
  await page.getByText('Advanced settings').click();
  await page.getByLabel('GPU', { exact: true }).selectOption({ index: 1 });
  await expect(page).toHaveURL(/gpu=/);
  await expect(page.getByText(`On ${smallerGpu.name} of ${node.name}`)).toContainText('you chose');
  await page.reload();
  await expect(page.getByText(`On ${smallerGpu.name} of ${node.name}`)).toBeVisible();
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  expect(control.deployments[0]?.gpu_uuid).toBe(smallerGpu.uuid);
});

test('shows what cannot be chosen yet, each with its reason', async ({ page }) => {
  await page.goto('/services/new');
  await page.getByText('Advanced settings').click();
  await expect(page.getByText('More than one replica arrives with SRV-105.')).toBeVisible();
  await expect(page.getByText('Choosing an engine arrives with SRV-101.')).toBeVisible();
  await expect(page.getByText('Autoscaling arrives with SRV-106.')).toBeVisible();
});

test('a lost answer retried with the same key makes one deployment', async ({ page }) => {
  control.createFailures = ['lost'];
  await page.goto('/services/new');
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  expect(new Set(control.createKeys).size).toBe(1);
  expect(control.createKeys.length).toBeGreaterThan(1);
  expect(control.deployments).toHaveLength(1);
});

test('a retry on another GPU is a new request, not a conflict with the first', async ({ page }) => {
  control.createFailures = [{ status: 409, code: 'INFERENCE_GPU_BUSY' }];
  await page.goto('/services/new');
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await expect(page.getByText('Another process is using this GPU')).toBeVisible();
  await page.getByText('Advanced settings').click();
  await page.getByLabel('GPU', { exact: true }).selectOption({ index: 1 });
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  expect(new Set(control.createKeys).size).toBe(2);
});

test('a refused deployment is explained and leaves the form', async ({ page }) => {
  control.createFailures = [{ status: 409, code: 'INFERENCE_RESOURCE_BUSY' }];
  await page.goto('/services/new');
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  await expect(page.getByText('This node or GPU already serves a deployment')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Deploy a model', level: 1 })).toBeVisible();
  expect(control.deployments).toHaveLength(0);
});

test('with nothing eligible, gives each reason and follows a fresh read', async ({ page }) => {
  control.pollMilliseconds = 1_000;
  control.placement = stalePlacement;
  control.placementAfterRead = openPlacement;
  await page.goto('/services/new');
  await expect(page.getByText('Nothing here can take this model now')).toBeVisible();
  await expect(page.getByText('GPUs have not been read recently')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Read GPUs' }).click();
  await expect(page.getByText(`On ${freeGpu.name} of ${node.name}`)).toBeVisible();
});

test('with no node having reported, asks one to read its GPUs', async ({ page }) => {
  control.pollMilliseconds = 1_000;
  control.nodes = [servingNode];
  control.placement = { ...openPlacement, candidates: [], recommended: null };
  control.placementAfterRead = openPlacement;
  await page.goto('/services/new');
  await expect(page.getByText('No node of this workspace has reported its GPUs')).toBeVisible();
  await page.getByRole('button', { name: 'Read GPUs' }).click();
  await expect(page.getByText(`On ${freeGpu.name} of ${node.name}`)).toBeVisible();
});

test('asks only a node of this workspace that can serve to read its GPUs', async ({ page }) => {
  control.placement = { ...openPlacement, candidates: [], recommended: null };
  await page.goto('/services/new');
  await expect(
    page.getByText('No node of this workspace that serves models is online.'),
  ).toBeVisible();
  await page.getByRole('link', { name: 'Add a node that serves models' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add a node' });
  await dialog.getByLabel('Node name').fill('gpu-b01');
  await dialog.getByLabel('What it will do').selectOption({ label: 'Serve models' });
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const steps = page.getByRole('dialog', { name: 'Join gpu-b01' });
  await expect(steps).toContainText('"execution_profile":"docker_inference"');
  await expect(steps).toContainText('/run/docker.sock');
  await expect(steps).toContainText('sudo docker load -i ownsolid-serving.tar');
  expect(control.joinTokens[0]?.execution_profile).toBe('docker_inference');
});

test('with no model, sends the person to the Model library', async ({ page }) => {
  control.models = [];
  await page.goto('/services/new');
  await expect(page.getByRole('heading', { name: 'No model to deploy' })).toBeVisible();
  await page.getByRole('link', { name: 'Open the Model library' }).click();
  await expect(page.getByRole('heading', { name: 'Model library', level: 1 })).toBeVisible();
});

test('stopping names what breaks and confirms by typing', async ({ page }) => {
  control.deployments = [runningDeployment];
  control.aliases = [deploymentAlias];
  control.gatewayKeys = [deploymentKey];
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'More actions' }).click();
  await page.getByRole('menuitem', { name: 'Stop', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText(deploymentAlias.name);
  await expect(dialog).toContainText(`${deploymentKey.label} ${deploymentKey.prefix}`);
  const submit = dialog.getByRole('button', { name: 'Stop deployment' });
  await expect(submit).toBeDisabled();
  await dialog.getByRole('textbox').first().fill(readyModel.name);
  await submit.click();
  await expect(dialog).toHaveCount(0);
  expect(control.actions).toEqual([
    { action: 'stop', revision: 2, reason: 'Stopped from the console' },
  ]);
  await expect(page.getByRole('main').getByText('Stopping').first()).toBeVisible();
});

test('two people acting at once: the second is told and sees the new state', async ({ page }) => {
  control.deployments = [runningDeployment];
  await page.goto(`/services/${runningDeployment.id}`);
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  control.deployments = [{ ...runningDeployment, revision: 3 }];
  await page.getByRole('button', { name: 'Check' }).click();
  await expect(page.getByText('This was changed by someone else')).toBeVisible();
  await expect.poll(() => control.actions.length).toBe(0);
  await page.getByRole('button', { name: 'Check' }).click();
  await expect
    .poll(() => control.actions)
    .toEqual([{ action: 'observe', revision: 3, reason: 'Checked from the console' }]);
});

test('removing returns to the list', async ({ page }) => {
  control.deployments = [runningDeployment];
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'More actions' }).click();
  await page.getByRole('menuitem', { name: 'Remove' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('No gateway name sends traffic here');
  await dialog.getByRole('textbox').first().fill(readyModel.name);
  await dialog.getByRole('button', { name: 'Remove deployment' }).click();
  await expect(page.getByRole('heading', { name: 'Model services', level: 1 })).toBeVisible();
  expect(control.actions[0]?.action).toBe('remove');
});

test('explains a failure the node reported, in words', async ({ page }) => {
  control.deployments = [failedDeployment];
  await page.goto(`/services/${failedDeployment.id}`);
  await expect(page.getByText('The model ran out of GPU memory')).toBeVisible();
  await expect(page.getByRole('main').getByText('INFERENCE_GPU_OOM')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start' })).toBeVisible();
});

test('shows traffic, logs, scaling and rollback as not ready, with reasons', async ({ page }) => {
  control.deployments = [runningDeployment];
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'More actions' }).click();
  await expect(page.getByRole('menuitem', { name: 'Scale' })).toHaveAttribute(
    'aria-disabled',
    'true',
  );
  await page.keyboard.press('Escape');
  await expect(page.getByText('Scaling is not ready yet', { exact: false })).toBeVisible();
  await expect(page.getByText('Rolling back is not ready yet')).toBeVisible();
  await page.getByRole('tab', { name: 'Logs' }).click();
  await expect(page.getByText('Logs are not ready yet')).toBeVisible();
  await page.getByRole('link', { name: 'Open the last task' }).waitFor();
});

test('a deployment that is gone says so', async ({ page }) => {
  await page.goto(`/services/${runningDeployment.id}`);
  await expect(
    page.getByRole('heading', { name: 'This deployment does not exist', level: 1 }),
  ).toBeVisible();
});

test('a model lists its deployments, or offers the first', async ({ page }) => {
  await page.goto(`/library/${readyModel.id}?tab=deployments`);
  await expect(page.getByRole('heading', { name: 'Not deployed' })).toBeVisible();
  control.deployments = [runningDeployment];
  await page.reload();
  await page.getByRole('link', { name: node.name }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
});
