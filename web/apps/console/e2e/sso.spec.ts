// Single sign-on: an administrator adds a Gitea connector with the callback URLs to register, tests
// which part of it is wrong, turns it on and off and replaces its credentials, each with a reason.
// The installation's configuration decides what can be added, and the page says so.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  activeConnector,
  giteaOrigin,
  mockControlPlane,
  retiredConnector,
  ssoOn,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
});

const clientId = 'a331d64b-0f90-4a69-8b99-c6b7624421a8';
const toast = (page: Page, text: string | RegExp) =>
  page.getByRole('status').filter({ hasText: text });
const drawerOf = (page: Page, name: string) => page.getByRole('dialog', { name });

async function openConnector(page: Page, name = activeConnector.name) {
  await page.goto('/sso');
  await page.getByRole('button', { name: `Open ${name}` }).click();
  return drawerOf(page, name);
}

test('is reached from the navigation and starts empty with a way to add one', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Governance', exact: true }).click();
  await page.getByRole('navigation').getByRole('link', { name: 'Single sign-on' }).click();
  await expect(page.getByRole('heading', { name: 'Single sign-on', level: 1 })).toBeFocused();
  await expect(page.getByText('No connectors yet')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add connector' })).toHaveCount(2);
});

test('adds a connector that starts off, after showing the callback URL to register', async ({
  page,
}) => {
  await page.goto('/sso#add');
  const dialog = page.getByRole('dialog', { name: 'Add a connector' });
  const callback = dialog.getByText(
    /^https:\/\/ownsolid\.example\.test\/api\/v1\/auth\/sso\/.+\/callback$/,
  );
  await expect(callback).toBeVisible();
  const url = (await callback.textContent()) ?? '';
  await expect(dialog.getByLabel('Provider address')).toHaveValue(giteaOrigin);
  await dialog.getByLabel('Name').fill('Company Gitea');
  await dialog.getByLabel('Client ID', { exact: true }).fill('not-a-client-id');
  await expect(dialog.getByText(/A Gitea client ID looks like/)).toBeVisible();
  await dialog.getByLabel('Client ID', { exact: true }).fill(clientId.toUpperCase());
  await dialog.getByLabel('Client secret').fill('gto_synthetic');
  const submit = dialog.getByRole('button', { name: 'Add connector' });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel('Reason').fill('Staff sign in with Gitea');
  await submit.click();
  const drawer = drawerOf(page, 'Company Gitea');
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText('Off', { exact: true })).toBeVisible();
  await expect(drawer.getByText(url)).toBeVisible();
  await expect(toast(page, 'Added Company Gitea. Test it, then turn it on.')).toBeVisible();
  const [written] = control.ssoWrites;
  expect(written).toMatchObject({
    name: 'Company Gitea',
    origin: giteaOrigin,
    client_id: clientId,
    enabled: false,
    has_secret: true,
    reason: 'Staff sign in with Gitea',
  });
  expect(url).toContain(String(written?.id));
  expect(control.ssoKeys[0]).toMatch(/^[0-9a-f-]{36}$/);
});

test('keeps one key when adding is retried after a failure', async ({ page }) => {
  control.nextSsoChange = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/sso#add');
  const dialog = page.getByRole('dialog', { name: 'Add a connector' });
  await dialog.getByLabel('Name').fill('Company Gitea');
  await dialog.getByLabel('Client ID', { exact: true }).fill(clientId);
  await dialog.getByLabel('Client secret').fill('gto_synthetic');
  await dialog.getByLabel('Reason').fill('Staff sign in with Gitea');
  await dialog.getByRole('button', { name: 'Add connector' }).click();
  await dialog.getByRole('button', { name: 'Try again' }).click();
  await expect(drawerOf(page, 'Company Gitea')).toBeVisible();
  expect(control.ssoKeys).toHaveLength(1);
  expect(control.ssoConnectorList).toHaveLength(1);
});

test('tells each failed check apart, with the next step', async ({ page }) => {
  control.ssoConnectorList = [{ ...activeConnector }];
  const drawer = await openConnector(page);
  const outcomes = [
    ['passed', 'Gitea accepted this connector'],
    ['credentials_rejected', 'Gitea refused the client ID or secret'],
    [
      'callback_not_registered',
      `/api/v1/auth/sso/${activeConnector.id}/callback to the application`,
    ],
    ['unreachable', `No answer from ${giteaOrigin}`],
    ['certificate_untrusted', "Gitea's certificate is not trusted"],
    ['unexpected_answer', 'Error code from the provider: invalid_grant.'],
  ] as const;
  for (const [outcome, text] of outcomes) {
    control.ssoChecks[activeConnector.id] = outcome;
    await drawer.getByRole('button', { name: 'Test connection' }).click();
    await expect(
      drawer.getByRole(outcome === 'passed' ? 'status' : 'alert').filter({ hasText: text }),
    ).toBeVisible();
  }
});

test('marks a connector whose provider left the configuration and does not offer turning it on', async ({
  page,
}) => {
  control.ssoConnectorList = [{ ...retiredConnector }];
  await page.goto('/sso');
  await expect(page.getByRole('row', { name: /Old Gitea/ })).toContainText('Provider not approved');
  const drawer = await openConnector(page, retiredConnector.name);
  await expect(drawer.getByText('Nobody can sign in with this connector')).toBeVisible();
  const turnOn = drawer.getByRole('button', { name: 'Turn on' });
  await expect(turnOn).toHaveAttribute('aria-disabled', 'true');
  await turnOn.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog', { name: 'Turn on Old Gitea' })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Test connection' }).click();
  await expect(drawer.getByText('This provider is no longer approved')).toBeVisible();
});

test('turns a connector off and on again, saying who is signed out', async ({ page }) => {
  control.ssoConnectorList = [{ ...activeConnector }];
  const drawer = await openConnector(page);
  await drawer.getByRole('button', { name: 'Turn off' }).click();
  const off = page.getByRole('dialog', { name: 'Turn off Company Gitea' });
  await expect(off).toContainText('signed out at once');
  await expect(off.getByRole('button', { name: 'Turn off' })).toBeDisabled();
  await off.getByLabel('Type Company Gitea to confirm').fill('company gitea');
  await off.getByRole('button', { name: 'Turn off' }).click();
  await expect(off.getByText('Give a reason; it is kept in the audit trail.')).toBeVisible();
  expect(control.ssoWrites).toHaveLength(0);
  await off.getByLabel('Reason').fill('Gitea is being replaced');
  await off.getByRole('button', { name: 'Turn off' }).click();
  await expect(toast(page, 'Company Gitea is off.')).toBeVisible();
  await expect(drawer.getByText('Off', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Turn on' }).click();
  const on = page.getByRole('dialog', { name: 'Turn on Company Gitea' });
  await on.getByLabel('Reason').fill('Gitea is back');
  await on.getByRole('button', { name: 'Turn on' }).click();
  await expect(toast(page, 'Company Gitea is on.')).toBeVisible();
  await expect(drawer.getByText('On', { exact: true })).toBeVisible();
  expect(control.ssoReasons).toEqual(['Gitea is being replaced', 'Gitea is back']);
  expect(control.ssoWrites.map((write) => write.enabled)).toEqual([false, true]);
});

test('replaces the credentials, warning that people are signed out', async ({ page }) => {
  control.ssoConnectorList = [{ ...activeConnector }];
  const drawer = await openConnector(page);
  await drawer.getByRole('button', { name: 'Edit' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit Company Gitea' });
  await expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled();
  await dialog.getByLabel('Replace the client ID and secret').check();
  await expect(
    dialog.getByText(/Everyone signed in with this connector is signed out/),
  ).toBeVisible();
  await dialog.getByLabel('Client ID', { exact: true }).fill(clientId);
  await dialog.getByLabel('Client secret').fill('gto_rotated');
  await dialog.getByLabel('Reason').fill('Secret rotated');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(toast(page, 'Saved Company Gitea.')).toBeVisible();
  expect(control.ssoWrites[0]).toMatchObject({
    name: activeConnector.name,
    enabled: true,
    client_id: clientId,
    has_secret: true,
  });
});

test('renames without touching the credentials', async ({ page }) => {
  control.ssoConnectorList = [{ ...activeConnector }];
  const drawer = await openConnector(page);
  await drawer.getByRole('button', { name: 'Edit' }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit Company Gitea' });
  await dialog.getByLabel('Name').fill('Gitea');
  await dialog.getByLabel('Reason').fill('Shorter name');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(toast(page, 'Saved Gitea.')).toBeVisible();
  expect(control.ssoWrites[0]).toMatchObject({ name: 'Gitea', client_id: null, has_secret: false });
});

test('explains a stale revision and succeeds on retry with the current one', async ({ page }) => {
  control.ssoConnectorList = [{ ...activeConnector }];
  const drawer = await openConnector(page);
  // Someone else changes the connector while the dialog is open.
  const stored = control.ssoConnectorList[0];
  if (stored) stored.revision += 1;
  await drawer.getByRole('button', { name: 'Turn off' }).click();
  const off = page.getByRole('dialog', { name: 'Turn off Company Gitea' });
  await off.getByLabel('Type Company Gitea to confirm').fill('Company Gitea');
  await off.getByLabel('Reason').fill('Gitea is being replaced');
  await off.getByRole('button', { name: 'Turn off' }).click();
  await expect(off.getByText('This changed while you were working')).toBeVisible();
  await off.getByRole('button', { name: 'Turn off' }).click();
  await expect(toast(page, 'Company Gitea is off.')).toBeVisible();
});

test('says single sign-on is off for the installation, and still lists what exists', async ({
  page,
}) => {
  control.ssoConfiguration = { ...ssoOn, enabled: false };
  control.ssoConnectorList = [{ ...activeConnector }];
  await page.goto('/sso#add');
  await expect(page.getByText('Single sign-on is off for this installation')).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Add a connector' })).toHaveCount(0);
  const add = page.getByRole('button', { name: 'Add connector' });
  await expect(add).toHaveAttribute('aria-disabled', 'true');
  await expect(add).toHaveAccessibleDescription(/An operator turns it on/);
  await add.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog', { name: 'Add a connector' })).toHaveCount(0);
  await expect(page.getByRole('row', { name: /Company Gitea/ })).toBeVisible();
});

test('says when no provider is approved', async ({ page }) => {
  control.ssoConfiguration = { ...ssoOn, providers: [] };
  await page.goto('/sso');
  await expect(page.getByText('No provider is approved')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add connector' })).toHaveAttribute(
    'aria-disabled',
    'true',
  );
});

test('shows the list when the configuration cannot be read, and offers a retry', async ({
  page,
}) => {
  control.ssoConfigurationProblem = { status: 503, code: 'SERVICE_NOT_READY' };
  control.ssoConnectorList = [{ ...activeConnector }];
  await page.goto('/sso');
  await expect(page.getByRole('row', { name: /Company Gitea/ })).toBeVisible();
  const retry = page.getByRole('button', { name: 'Try again' });
  await retry.waitFor();
  // Not knowing the configuration is not the same as single sign-on being off.
  await page.getByRole('button', { name: 'Open Company Gitea' }).click();
  await expect(drawerOf(page, 'Company Gitea')).toBeVisible();
  await expect(page.getByText('Single sign-on is off for this installation')).toHaveCount(0);
  await page.keyboard.press('Escape');
  control.ssoConfigurationProblem = null;
  await retry.click();
  await expect(page.getByRole('button', { name: 'Add connector' })).not.toHaveAttribute(
    'aria-disabled',
  );
});

test('explains a list that cannot be read, and recovers', async ({ page }) => {
  control.ssoConnectorsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/sso');
  const retry = page.getByRole('button', { name: 'Try again' });
  await retry.waitFor();
  control.ssoConnectorsProblem = null;
  await retry.click();
  await expect(page.getByText('No connectors yet')).toBeVisible();
});

test('says when a linked connector does not exist', async ({ page }) => {
  await page.goto('/sso?open=6f1c7a1e-0000-4000-8000-0000000003ee');
  await expect(drawerOf(page, 'Connector not found')).toContainText('does not exist');
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/sso');
  await expect(page.getByRole('button', { name: 'Add connector' })).toHaveCount(0);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    control.ssoConnectorList = [{ ...activeConnector }];
    await page.goto('/sso');
    await expect(page.getByRole('heading', { name: chinese['sso.title'], level: 1 })).toBeVisible();
    await page
      .getByRole('button', {
        name: chinese['sso.detail.open'].replace('{name}', activeConnector.name),
      })
      .click();
    await page.getByRole('button', { name: chinese['sso.check.open'] }).click();
    await expect(page.getByText(chinese['sso.check.passed.title'])).toBeVisible();
  });
});
