// Publishing a deployment to the gateway from its page: a new gateway's credential shown once,
// retries that never make a second gateway, a key issued from the card, and stopping confirmed by
// typing (docs/research/gateway-publishing.md).
import {
  deploymentAlias,
  deploymentKey,
  mockControlPlane,
  node,
  nodeGatewayInstance,
  readyModel,
  runningDeployment,
  stoppedDeployment,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

const fresh = { ...runningDeployment, observation: null, observed_state: 'running' as const };

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [node];
  control.models = [readyModel];
  control.deployments = [runningDeployment];
});

/** The card on the deployment's Overview. */
function card(page: Page) {
  return page.getByRole('region', { name: 'On the gateway' });
}

async function openPublish(page: Page) {
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Publish to the gateway' }).click();
  return page.getByRole('dialog');
}

test('publishes to a new gateway, shows its credential once, then how to call it', async ({
  page,
}) => {
  const dialog = await openPublish(page);
  // The name is made from the model's name in the gateway's alphabet.
  await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('qwen3-0.6b');
  await expect(dialog).toContainText(`No gateway serves ${node.name} yet`);
  await dialog.getByLabel('Gateway address').fill('https://Gateway.Example.Test/');
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Published as qwen3-0.6b' })).toBeVisible();
  const made = control.gateways[0];
  expect(made?.client_origin).toBe('https://gateway.example.test');
  await expect(dialog).toContainText('It is shown only this once');
  await expect(dialog).toContainText(`gw1.${made?.id ?? ''}.`);
  await expect(dialog).toContainText(made?.id ?? 'missing');
  await expect(dialog).toContainText(node.id);
  expect(control.publishWrites.map((write) => write.kind)).toEqual(['provision', 'publish']);
  expect(control.publishWrites[0]?.body).toEqual({
    node_id: node.id,
    name: node.name,
    client_origin: 'https://gateway.example.test',
    reason: 'Serve the support team',
  });
  expect(control.publishWrites[1]?.body).toEqual({
    gateway_id: made?.id,
    name: 'qwen3-0.6b',
    deployment_id: runningDeployment.id,
    deployment_revision: runningDeployment.revision,
    reason: 'Serve the support team',
  });
  await dialog.getByRole('button', { name: 'Done' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page).getByText('Answering calls')).toBeVisible();
  await expect(card(page)).toContainText('https://gateway.example.test/v1');
  await expect(card(page)).toContainText('curl https://gateway.example.test/v1/chat/completions');
  await expect(page.getByRole('button', { name: 'Publish to the gateway' })).toHaveCount(0);
});

test("publishes to the node's gateway when it has one, and makes no other", async ({ page }) => {
  control.gateways = [nodeGatewayInstance];
  const dialog = await openPublish(page);
  await expect(dialog).toContainText(
    `${nodeGatewayInstance.name}, at ${nodeGatewayInstance.client_origin}`,
  );
  await expect(dialog.getByLabel('Gateway address')).toHaveCount(0);
  await dialog.getByLabel('Name', { exact: true }).fill('support-bot');
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('Published as support-bot')).toBeVisible();
  expect(control.publishWrites.map((write) => write.kind)).toEqual(['publish']);
  expect(control.publishWrites[0]?.body.gateway_id).toBe(nodeGatewayInstance.id);
});

test('checks the name and the address as typed, and sends nothing until they hold', async ({
  page,
}) => {
  const dialog = await openPublish(page);
  const name = dialog.getByLabel('Name', { exact: true });
  await name.fill('1st model');
  await name.blur();
  await expect(dialog.getByText('Start with a lower-case letter.')).toBeVisible();
  await name.fill('my model');
  await expect(dialog.getByText('Use only lower-case letters')).toBeVisible();
  await name.fill('my-model');
  await dialog.getByLabel('Gateway address').fill('http://gateway.example.test/v1');
  await dialog.getByLabel('Reason').fill('Serve');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog.getByText('Enter an HTTPS address with no path')).toBeVisible();
  expect(control.publishWrites).toEqual([]);
});

test('a failed publish keeps the new credential on screen and retries publishing only', async ({
  page,
}) => {
  control.aliases = [{ ...deploymentAlias, name: 'taken', deployment_id: stoppedDeployment.id }];
  const dialog = await openPublish(page);
  await dialog.getByLabel('Name', { exact: true }).fill('taken');
  await dialog.getByLabel('Gateway address').fill('https://gateway.example.test');
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog.getByText('This name is taken')).toBeVisible();
  await expect(dialog).toContainText('It is shown only this once');
  await dialog.getByLabel('Name', { exact: true }).fill('support-bot');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Published as support-bot' })).toBeVisible();
  // The credential is still the one made first: no second gateway was registered.
  await expect(dialog).toContainText(`gw1.${control.gateways[0]?.id ?? ''}.`);
  expect(control.gateways).toHaveLength(1);
  expect(control.publishWrites.map((write) => write.kind)).toEqual([
    'provision',
    'publish',
    'publish',
  ]);
});

test('a lost answer that carried the credential: revoke that gateway and register another', async ({
  page,
}) => {
  control.publishFailures = ['lost'];
  const dialog = await openPublish(page);
  await dialog.getByLabel('Gateway address').fill('https://gateway.example.test');
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(dialog.getByText('its credential cannot be shown again')).toBeVisible();
  // The retry reused the first request's key, so the server answered with the same gateway.
  expect(control.publishWrites.map((write) => write.kind)).toEqual(['provision', 'provision']);
  expect(control.publishWrites[1]?.key).toBe(control.publishWrites[0]?.key);
  expect(control.gateways).toHaveLength(1);
  await dialog.getByRole('button', { name: 'Revoke it and register another' }).click();
  await expect(dialog.getByText('its credential cannot be shown again')).toHaveCount(0);
  expect(control.gateways[0]?.state).toBe('revoked');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Published as qwen3-0.6b' })).toBeVisible();
  const second = control.gateways[0];
  expect(second?.state).toBe('active');
  await expect(dialog).toContainText(`gw1.${second?.id ?? ''}.`);
  expect(control.publishWrites.map((write) => write.kind)).toEqual([
    'provision',
    'provision',
    'revokeGateway',
    'provision',
    'publish',
  ]);
  expect(control.publishWrites[3]?.key).not.toBe(control.publishWrites[0]?.key);
  expect(control.publishWrites[4]?.body.gateway_id).toBe(second?.id);
});

test('a gateway someone registered meanwhile is read and published to', async ({ page }) => {
  const dialog = await openPublish(page);
  await expect(dialog.getByLabel('Gateway address')).toBeVisible();
  control.gateways = [nodeGatewayInstance];
  await dialog.getByLabel('Gateway address').fill('https://other.example.test');
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog).toContainText(`${nodeGatewayInstance.name}, at`);
  await expect(dialog.getByText('This node already has a gateway')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(control.publishWrites.map((write) => write.kind)).toEqual(['provision', 'publish']);
  expect(control.publishWrites[1]?.body.gateway_id).toBe(nodeGatewayInstance.id);
});

test('a refusal is explained and the publish can be tried again', async ({ page }) => {
  control.gateways = [nodeGatewayInstance];
  control.publishFailures = [{ status: 503, code: 'STORAGE_UNAVAILABLE' }];
  const dialog = await openPublish(page);
  await dialog.getByLabel('Reason').fill('Serve the support team');
  await dialog.getByRole('button', { name: 'Publish', exact: true }).click();
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(dialog).toHaveCount(0);
  expect(control.aliases.filter((alias) => alias.disabled_at === null)).toHaveLength(1);
  // The retry reused its key: a lost answer cannot publish twice.
  expect(control.publishWrites[1]?.key).toBe(control.publishWrites[0]?.key);
});

test('says what must happen before a deployment can be published', async ({ page }) => {
  control.deployments = [fresh];
  await page.goto(`/services/${fresh.id}`);
  await expect(card(page).getByText('Choose Verify on this page')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Publish to the gateway' })).toHaveCount(0);
  control.deployments = [stoppedDeployment];
  await page.goto(`/services/${stoppedDeployment.id}`);
  await expect(card(page).getByText('It can be published once it is running')).toBeVisible();
});

test('issues a key for the published name from the card', async ({ page }) => {
  control.gateways = [nodeGatewayInstance];
  control.aliases = [
    {
      ...deploymentAlias,
      id: '6f1c7a1e-0000-4000-8000-0000000000b0',
      name: 'other',
      deployment_id: stoppedDeployment.id,
    },
    deploymentAlias,
  ];
  await page.goto(`/services/${runningDeployment.id}`);
  await card(page).getByRole('button', { name: 'Issue a key' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Model', { exact: true })).toHaveValue(deploymentAlias.id);
  await dialog.getByLabel('Name', { exact: true }).fill('Support bot');
  await dialog.getByLabel('Reason').fill('Access');
  await dialog.getByRole('button', { name: 'Issue', exact: true }).click();
  await expect(dialog.getByRole('button', { name: 'Done' })).toBeVisible();
  expect(control.keyWrites[0]?.body.alias_id).toBe(deploymentAlias.id);
});

test('stopping publishing names the keys that stop and confirms by typing', async ({ page }) => {
  control.gateways = [nodeGatewayInstance];
  control.aliases = [deploymentAlias];
  control.gatewayKeys = [deploymentKey];
  await page.goto(`/services/${runningDeployment.id}`);
  await card(page).getByRole('button', { name: 'Stop publishing' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText(`${deploymentKey.label} ${deploymentKey.prefix}`);
  await expect(dialog).toContainText('cannot be published again');
  const submit = dialog.getByRole('button', { name: 'Stop publishing' });
  await dialog.getByLabel('Reason').fill('Retired');
  await expect(submit).toBeDisabled();
  await dialog.getByLabel(/^Type/).fill(deploymentAlias.name);
  await submit.click();
  await expect(dialog).toHaveCount(0);
  expect(control.publishWrites).toEqual([
    {
      kind: 'disable',
      key: '',
      body: { expected_revision: deploymentAlias.revision, reason: 'Retired' },
    },
  ]);
  await expect(page.getByRole('button', { name: 'Publish to the gateway' })).toBeVisible();
});

test('a name changed meanwhile is explained, and the card reads it again', async ({ page }) => {
  control.gateways = [nodeGatewayInstance];
  control.aliases = [deploymentAlias];
  await page.goto(`/services/${runningDeployment.id}`);
  await card(page).getByRole('button', { name: 'Stop publishing' }).click();
  control.aliases = [{ ...deploymentAlias, revision: 2 }];
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Reason').fill('Retired');
  await dialog.getByLabel(/^Type/).fill(deploymentAlias.name);
  await dialog.getByRole('button', { name: 'Stop publishing' }).click();
  await expect(dialog.getByText('This was changed by someone else')).toBeVisible();
  expect(control.aliases[0]?.disabled_at).toBeNull();
});
