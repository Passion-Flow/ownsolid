// A person and a single sign-on connector on their own pages: reached from the drawer or a link that names
// them, with what was done to them, and the same actions as the drawer.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  activeConnector,
  adminUser,
  expectAccessible,
  memberUser,
  mockControlPlane,
  ssoOn,
  systemEvent,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [adminUser, memberUser];
});

test.describe('a person', () => {
  const path = `/users/${memberUser.id}`;
  const title = 'Grace Hopper';

  test('opens from the drawer and shows the details', async ({ page }) => {
    await page.goto(`/users?open=${memberUser.id}`);
    await page.getByRole('dialog').getByRole('link', { name: 'Open the full page' }).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
    await expect(page.getByText('grace@example.test').first()).toBeVisible();
    await expect(
      page.getByRole('navigation').getByRole('link', { name: 'Users and workspaces' }),
    ).toHaveAttribute('aria-current', 'page');
    await expectAccessible(page);
  });

  test('tells what was done to them from what they did', async ({ page }) => {
    control.auditEvents = [
      { ...systemEvent, occurred_at: memberUser.created_at, target_id: memberUser.id },
    ];
    await page.goto(`${path}?tab=activity`);
    await expect(page.getByRole('tabpanel').getByRole('row')).toHaveCount(2);
    expect(control.auditQueries.at(-1)?.get('target_id')).toBe(memberUser.id);
    await expectAccessible(page);
    await page.getByRole('tab', { name: 'Their actions' }).click();
    await expect(page.getByText('No activity yet')).toBeVisible();
    expect(control.auditQueries.at(-1)?.get('actor_user_id')).toBe(memberUser.id);
    expect(control.auditQueries.at(-1)?.has('target_id')).toBe(false);
  });

  test('can be disabled from the page, which stays and offers undo', async ({ page }) => {
    await page.goto(path);
    await page.getByRole('main').getByRole('button', { name: 'Disable' }).click();
    const dialog = page.getByRole('dialog', { name: 'Disable grace@example.test' });
    await dialog.getByRole('textbox').fill('Left the company');
    await dialog.getByRole('button', { name: 'Disable' }).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole('main').getByRole('button', { name: 'Enable' })).toBeVisible();
    await page.getByRole('button', { name: 'Undo' }).click();
    await expect(page.getByRole('main').getByRole('button', { name: 'Disable' })).toBeVisible();
  });

  test('your own account says so and cannot be disabled', async ({ page }) => {
    await page.goto(`/users/${adminUser.id}`);
    await expect(
      page.getByRole('heading', { name: adminUser.display_name, level: 1 }),
    ).toBeVisible();
    await expect(page.getByRole('main').getByRole('button', { name: 'Disable' })).toHaveCount(0);
  });

  test('one that does not exist says so, and a failure can be retried', async ({ page }) => {
    await page.goto('/users/6f1c7a1e-0000-4000-8000-ffffffffffff');
    await expect(
      page.getByRole('heading', { name: 'This account could not be found', level: 1 }),
    ).toBeVisible();
    await expect(page.getByRole('link', { name: 'All users' })).toBeVisible();
    await expectAccessible(page);

    let failing = true;
    await page.route(/\/admin\/users\/[0-9a-f-]{36}$/, (route) =>
      failing
        ? route.fulfill({
            status: 503,
            contentType: 'application/problem+json',
            body: JSON.stringify({ code: 'INTERNAL_ERROR', title: 'x', status: 503 }),
          })
        : route.fallback(),
    );
    await page.goto(path);
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
    failing = false;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  });
});

test.describe('a connector', () => {
  const path = `/sso/${activeConnector.id}`;

  test.beforeEach(() => {
    control.ssoConfiguration = { ...ssoOn };
    control.ssoConnectorList = [{ ...activeConnector }];
  });

  test('opens from the drawer with its check and details', async ({ page }) => {
    await page.goto(`/sso?open=${activeConnector.id}`);
    await page.getByRole('dialog').getByRole('link', { name: 'Open the full page' }).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole('heading', { name: activeConnector.name, level: 1 })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Test connection' })).toBeVisible();
    await expect(page.getByText(activeConnector.client_id)).toBeVisible();
    await expectAccessible(page);
  });

  test('its activity lists who changed it', async ({ page }) => {
    control.auditEvents = [
      { ...systemEvent, occurred_at: activeConnector.created_at, target_id: activeConnector.id },
    ];
    await page.goto(`${path}?tab=activity`);
    await expect(page.getByRole('tabpanel').getByRole('row')).toHaveCount(2);
    expect(control.auditQueries.at(-1)?.get('target_id')).toBe(activeConnector.id);
    await expectAccessible(page);
  });

  test('one that does not exist says so, and a failure can be retried', async ({ page }) => {
    await page.goto('/sso/6f1c7a1e-0000-4000-8000-ffffffffffff');
    await expect(
      page.getByRole('heading', { name: 'This connector could not be found', level: 1 }),
    ).toBeVisible();

    control.ssoConnectorsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto(path);
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
    control.ssoConnectorsProblem = null;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('heading', { name: activeConnector.name, level: 1 })).toBeVisible();
  });
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('names the tabs', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto(`/users/${memberUser.id}`);
    await expect(page.getByRole('tab', { name: chinese['resource.tab.actions'] })).toBeVisible();
  });
});
