// Publishing a deployment to the gateway on a real installation. A fixture readies a verified
// deployment on a scripted node that has no gateway; the browser publishes it from its page, which
// registers the node's gateway and shows its credential once, then issues a key from the same card
// and stops publishing. The fixture stands in for the gateway process (GW-114) with that credential
// and calls over the real /gateway/v1 protocol; no model runs.
import { execFileSync } from 'node:child_process';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const fixture = process.env.OWNSOLID_LIVE_GATEWAY_COMMAND ?? '';
const gatewayOrigin = process.env.OWNSOLID_LIVE_GATEWAY_ORIGIN ?? '';
const name = `pub-${String(Date.now())}`;

test.skip(
  origin === '' || fixture === '' || gatewayOrigin === '',
  'needs the gateway fixture and an address for the gateway',
);
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

test('publishes a deployment from its page, calls it with a key issued there, and stops', async ({
  browser,
}) => {
  test.setTimeout(10 * 60_000);
  const deployment = run('deploy', name);
  const context = await browser.newContext();
  const page = await context.newPage();
  const outside: string[] = [];
  context.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      outside.push(request.url());
    }
  });
  await signIn(page, `/services/${deployment}`);
  const card = page.getByRole('region', { name: 'On the gateway' });
  await card.getByRole('button', { name: 'Publish to the gateway' }).click();
  const publishing = page.getByRole('dialog', { name: 'Publish to the gateway' });
  await expect(publishing.getByText(/No gateway serves .* yet/)).toBeVisible();
  await publishing.getByLabel('Name', { exact: true }).fill(name);
  await publishing.getByLabel('Gateway address').fill(gatewayOrigin);
  await publishing.getByLabel('Gateway name').fill(name);
  await publishing.getByLabel('Reason').fill('Live publishing run');
  await expectAccessible(page);
  await publishing.getByRole('button', { name: 'Publish', exact: true }).click();

  const published = page.getByRole('dialog', { name: `Published as ${name}` });
  await expect(published.getByText('It is shown only this once.')).toBeVisible();
  const credential = (await published.getByText(/^gw1\./).textContent()) ?? '';
  const match = /^gw1\.([0-9a-f-]{36})\./.exec(credential);
  expect(match).not.toBeNull();
  const gateway = match?.[1] ?? '';
  await expect(published).toContainText(gateway);
  await expectAccessible(page);
  await published.getByRole('button', { name: 'Done' }).click();

  await expect(card.getByText('Answering calls')).toBeVisible();
  await expect(card).toContainText(`${gatewayOrigin}/v1`);
  await expect(card).toContainText(`"model":"${name}"`);

  await card.getByRole('button', { name: 'Issue a key' }).click();
  const issuing = page.getByRole('dialog', { name: 'Issue a key' });
  await expect(issuing.getByLabel('Model').locator('option:checked')).toHaveText(name);
  await issuing.getByLabel('Name', { exact: true }).fill(name);
  await issuing.getByLabel('Reason').fill('Live publishing run');
  await issuing.getByRole('button', { name: 'Issue', exact: true }).click();
  const issued = page.getByRole('dialog', { name: `${name} is issued` });
  const secret = (await issued.getByText(/^pk1\./).textContent()) ?? '';
  expect(secret).toMatch(/^pk1\.[0-9a-f-]{36}\./);
  await issued.getByRole('button', { name: 'Done' }).click();

  // The credential the page showed is the gateway's real identity: with it, calls are admitted.
  const state = run('adopt', name, credential);
  expect(run('call', state, secret, '2')).toBe('2');

  await card.getByRole('button', { name: 'Stop publishing' }).click();
  const stopping = page.getByRole('dialog', { name: `Stop publishing ${name}?` });
  await expect(stopping).toContainText(name);
  await stopping.getByLabel(/^Type/).fill(name);
  await stopping.getByLabel('Reason').fill('Live publishing run');
  await expectAccessible(page);
  await stopping.getByRole('button', { name: 'Stop publishing' }).click();
  await expect(page.getByText(`Stopped publishing ${name}`)).toBeVisible();
  await expect(card.getByRole('button', { name: 'Publish to the gateway' })).toBeVisible();
  // The name no longer admits calls.
  expect(() => run('call', state, secret, '1')).toThrow();

  await page.goto('/audit');
  await expect(
    page.getByRole('main').getByText('Model published to the gateway').first(),
  ).toBeVisible();
  expect(outside).toEqual([]);
  await context.close();
});
