// Publishing a deployment whose node's agent runs the gateway, on a real installation (GW-114,
// ADR 0052). A fixture readies a verified deployment on a node enrolled with gateway tasks; the
// browser publishes it without typing an address and sees no credential, waits while the agent
// starts the gateway, issues a key from the card, and the fixture calls the model through that
// gateway with the key. The fixture's node scripts the deployment, but its gateway is the real
// one, started by the agent's own gateway code, in front of a real model.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_AGENT_GATEWAY_COMMAND ?? '';
const name = `agw-${String(Date.now())}`;

test.skip(origin === '' || fixture === '', 'needs the agent gateway fixture');
test.use(liveOptions);

/** Runs the fixture with arguments and gives what it printed. */
function run(...args: string[]): string {
  return execFileSync('sh', ['-c', `${fixture} "$@"`, 'fixture', ...args], {
    encoding: 'utf8',
  }).trim();
}

async function signIn(page: Page, path: string) {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
}

test("publishes through the node's own gateway, with no address typed and no credential shown", async ({
  browser,
}) => {
  test.setTimeout(10 * 60_000);
  const [deployment, gatewayOrigin] = run('deploy', name).split(' ');
  const context = await browser.newContext();
  const page = await context.newPage();
  const outside: string[] = [];
  context.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      outside.push(request.url());
    }
  });
  await signIn(page, `/services/${deployment ?? ''}`);
  const card = page.getByRole('region', { name: 'On the gateway' });
  await card.getByRole('button', { name: 'Publish to the gateway' }).click();
  const publishing = page.getByRole('dialog', { name: 'Publish to the gateway' });
  await expect(publishing.getByText(/'s agent will start a gateway/)).toBeVisible();
  await expect(publishing.getByLabel('Gateway address')).toHaveCount(0);
  await publishing.getByLabel('Name', { exact: true }).fill(name);
  await publishing.getByLabel('Gateway name').fill(name);
  await publishing.getByLabel('Reason').fill('Live agent gateway run');
  await expectAccessible(page);
  await publishing.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(publishing.getByText(/Starting the gateway on/)).toBeVisible();
  await expect(page.getByText(`Published as ${name}`)).toBeVisible({ timeout: 120_000 });
  await expect(page.getByText(/gw1\./)).toHaveCount(0);

  await expect(card.getByText('Answering calls')).toBeVisible();
  await expect(card).toContainText(`${gatewayOrigin ?? ''}/v1`);
  await card.getByRole('button', { name: 'Issue a key' }).click();
  const issuing = page.getByRole('dialog', { name: 'Issue a key' });
  await issuing.getByLabel('Name', { exact: true }).fill(name);
  await issuing.getByLabel('Reason').fill('Live agent gateway run');
  await issuing.getByRole('button', { name: 'Issue', exact: true }).click();
  const issued = page.getByRole('dialog', { name: `${name} is issued` });
  const secret = (await issued.getByText(/^pk1\./).textContent()) ?? '';
  expect(secret).toMatch(/^pk1\.[0-9a-f-]{36}\./);
  await issued.getByRole('button', { name: 'Done' }).click();

  // The key reaches the real model through the gateway the agent started.
  expect(run('chat', name, secret)).toMatch(/\S/);
  expect(outside).toEqual([]);
  await context.close();
});
