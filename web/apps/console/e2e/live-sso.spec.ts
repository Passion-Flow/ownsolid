// Runs the Single sign-on page against a real installation and a real Gitea: adds a connector, finds
// a wrong secret and a callback Gitea does not accept, passes the check, turns it on, signs in through
// Gitea, and ends that session by turning the connector off from a second browser. Each run leaves one
// connector turned off, since the API has no deletion, and one Gitea application. Skipped unless the
// environment describes both; docs/development/environment.md has the recipe.
import { execSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

const providerOrigin = process.env.OWNSOLID_LIVE_PROVIDER_ORIGIN ?? '';
const providerUser = process.env.OWNSOLID_LIVE_PROVIDER_USER ?? '';
const providerPasswordFile = process.env.OWNSOLID_LIVE_PROVIDER_PASSWORD_FILE ?? '';
// Creates or changes an OAuth2 application at the provider, as a person would in Gitea's settings.
const appCommand = process.env.OWNSOLID_LIVE_GITEA_APP_COMMAND ?? '';

test.skip(origin === '' || appCommand === '', 'needs an installation with a real Gitea');
test.use(liveOptions);

interface GiteaApplication {
  id: number;
  client_id: string;
  client_secret: string;
}

function gitea(words: string[]): GiteaApplication {
  const text = execSync(`${appCommand} ${words.join(' ')}`, { encoding: 'utf8' });
  return JSON.parse(text) as GiteaApplication;
}

async function signIn(page: Page): Promise<void> {
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}

test('adds, tests and turns on a connector, and someone signs in with it', async ({
  page,
  browser,
}) => {
  test.setTimeout(240_000);
  const elsewhere: string[] = [];
  page.on('request', (request) => {
    const from = new URL(request.url()).origin;
    if (from !== origin && from !== providerOrigin) elsewhere.push(request.url());
  });
  const name = `Live Gitea ${String(Date.now())}`;
  const reason = `Live run ${name}`;

  await page.goto('/sso');
  await signIn(page);
  await expect(page.getByRole('heading', { name: 'Single sign-on', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Add connector' }).first().click();
  const add = page.getByRole('dialog', { name: 'Add a connector' });
  const callbacks = await add.getByText(/\/api\/v1\/auth\/sso\/.+\/callback$/).allTextContents();
  expect(callbacks).toHaveLength(2);
  const [portalCallback = '', adminCallback = ''] = callbacks;
  await expectAccessible(page);

  // Gitea knows only the portal's callback for now.
  const application = gitea(['create', portalCallback]);
  await add.getByLabel('Name').fill(name);
  await add.getByLabel('Provider address').selectOption(providerOrigin);
  await add.getByLabel('Client ID', { exact: true }).fill(application.client_id);
  await add.getByLabel('Client secret').fill('gto_this_secret_is_wrong');
  await add.getByLabel('Reason').fill(reason);
  await add.getByRole('button', { name: 'Add connector' }).click();
  const drawer = page.getByRole('dialog', { name });
  await expect(drawer.getByText('Off', { exact: true })).toBeVisible();

  const testButton = drawer.getByRole('button', { name: 'Test connection' });
  await testButton.click();
  await expect(drawer.getByText('Gitea refused the client ID or secret')).toBeVisible();
  await expectAccessible(page);

  const replaceSecret = async (clientSecret: string) => {
    await drawer.getByRole('button', { name: 'Edit' }).click();
    const edit = page.getByRole('dialog', { name: `Edit ${name}` });
    await edit.getByLabel('Replace the client ID and secret').check();
    await edit.getByLabel('Client ID', { exact: true }).fill(application.client_id);
    await edit.getByLabel('Client secret').fill(clientSecret);
    await edit.getByLabel('Reason').fill(reason);
    await edit.getByRole('button', { name: 'Save' }).click();
    await expect(edit).toBeHidden();
  };
  await replaceSecret(application.client_secret);

  await testButton.click();
  await expect(drawer.getByText('Gitea does not accept a callback URL')).toBeVisible();
  await expect(drawer.getByRole('alert')).toContainText(adminCallback);

  // Gitea's API issues a new secret with every change to an application.
  const updated = gitea(['update', String(application.id), portalCallback, adminCallback]);
  await replaceSecret(updated.client_secret);
  await testButton.click();
  await expect(drawer.getByText('Gitea accepted this connector')).toBeVisible();

  await drawer.getByRole('button', { name: 'Turn on' }).click();
  const on = page.getByRole('dialog', { name: `Turn on ${name}` });
  await on.getByLabel('Reason').fill(reason);
  await on.getByRole('button', { name: 'Turn on' }).click();
  await expect(drawer.getByText('On', { exact: true })).toBeVisible();

  // Each person links their account once, which proves the whole exchange with Gitea.
  await page.goto('/account');
  await page.getByRole('button', { name: `Link ${name}` }).click();
  const link = page.getByRole('dialog', { name: `Link ${name}` });
  await link.getByLabel('Current password').fill(adminPassword());
  await link.getByRole('button', { name: 'Continue to the provider' }).click();
  await page.waitForURL(`${providerOrigin}/**`);
  await page.locator('input[name="user_name"]').fill(providerUser);
  await page
    .locator('input[name="password"]')
    .fill(readFileSync(providerPasswordFile, 'utf8').trim());
  await page.locator('form[action*="login"] button.primary').click();
  const authorize = page.locator('button#authorize-app');
  const linked = page.getByText('The account is linked');
  await authorize.or(linked).first().waitFor();
  if (await authorize.isVisible()) await authorize.click();
  await expect(linked).toBeVisible();
  await page.getByRole('link', { name: 'Back to your account' }).click();

  // Signing out and back in through Gitea, with no ownsolid password.
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await page.getByRole('button', { name: `Continue with ${name}` }).click();
  const authorizeAgain = page.locator('button#authorize-app');
  const signedIn = page.getByRole('button', { name: 'Account menu' });
  const finish = page.getByRole('link', { name: /Continue|Go to/ });
  await authorizeAgain.or(signedIn).or(finish).first().waitFor();
  if (await authorizeAgain.isVisible()) await authorizeAgain.click();
  if (await finish.isVisible()) await finish.click();
  await expect(signedIn).toBeVisible();

  // Turning it off from another browser ends this session; password sign-in keeps working.
  const other = await browser.newContext(liveOptions);
  const otherPage = await other.newPage();
  await otherPage.goto('/sso');
  await signIn(otherPage);
  await otherPage.getByRole('button', { name: `Open ${name}` }).click();
  await otherPage.getByRole('dialog', { name }).getByRole('button', { name: 'Turn off' }).click();
  const off = otherPage.getByRole('dialog', { name: `Turn off ${name}` });
  await off.getByLabel(`Type ${name} to confirm`).fill(name);
  await off.getByLabel('Reason').fill(reason);
  await expectAccessible(otherPage);
  await off.getByRole('button', { name: 'Turn off' }).click();
  await expect(
    otherPage.getByRole('dialog', { name }).getByText('Off', { exact: true }),
  ).toBeVisible();

  await page.goto('/sso');
  await expect(page).toHaveURL(/\/sign-in/);
  await expect(page.getByRole('button', { name: `Continue with ${name}` })).toHaveCount(0);

  // The connector's own page holds who changed it.
  await otherPage
    .getByRole('dialog', { name })
    .getByRole('link', { name: 'Open the full page' })
    .click();
  await expect(otherPage.getByRole('heading', { name, level: 1 })).toBeVisible();
  await otherPage.getByRole('tab', { name: 'Activity' }).click();
  await expect(otherPage.getByRole('tabpanel').getByText('sso.connector_created')).toBeVisible();
  await expectAccessible(otherPage);

  await otherPage.goto('/audit');
  await expect(otherPage.getByText('sso.connector_tested').first()).toBeVisible();
  await other.close();
  expect(elsewhere).toEqual([]);
});
