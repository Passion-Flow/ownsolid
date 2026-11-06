// The sidebar: only the group in use is open, what a person opens goes when they move on, a heading only
// opens and closes, a filter skips the hierarchy, and the icon-only rail keeps every name.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  adminUser,
  expectAccessible,
  memberUser,
  mockControlPlane,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.users = [adminUser, memberUser];
});

const sidebar = (page: Page) => page.getByRole('complementary');
const nav = (page: Page) => sidebar(page).getByRole('navigation');
const heading = (page: Page, name: string) => nav(page).getByRole('button', { name, exact: true });
const link = (page: Page, name: string) => nav(page).getByRole('link', { name, exact: true });

test('only the group of the current page is open', async ({ page }) => {
  await page.goto('/tasks');
  await expect(link(page, 'Tasks')).toBeVisible();
  await expect(heading(page, 'Operations')).toHaveAttribute('aria-expanded', 'true');
  await expect(heading(page, 'Governance')).toHaveAttribute('aria-expanded', 'false');
  await expect(link(page, 'Audit trail')).toHaveCount(0);
  await expect(link(page, 'Tasks')).toHaveAttribute('aria-current', 'page');
  await expectAccessible(page);
});

test('the current page is marked by a bar and weight, not colour alone', async ({ page }) => {
  await page.goto('/tasks');
  const current = link(page, 'Tasks');
  await expect(current).toHaveCSS('font-weight', '600');
  const bar = await current.evaluate((element) => getComputedStyle(element, '::before').width);
  expect(bar).toBe('3px');
});

test('a heading only opens and closes, and never navigates', async ({ page }) => {
  await page.goto('/tasks');
  await heading(page, 'Governance').click();
  await expect(link(page, 'Audit trail')).toBeVisible();
  await expect(page).toHaveURL(/\/tasks$/);
  await heading(page, 'Governance').click();
  await expect(link(page, 'Audit trail')).toHaveCount(0);
  await expect(page).toHaveURL(/\/tasks$/);
});

test('what a person opened goes when they move to another page', async ({ page }) => {
  await page.goto('/tasks');
  await heading(page, 'Governance').click();
  await heading(page, 'System').click();
  await expect(link(page, 'License')).toBeVisible();
  await link(page, 'Audit trail').click();
  await expect(page).toHaveURL(/\/audit/);
  await expect(heading(page, 'Governance')).toHaveAttribute('aria-expanded', 'true');
  await expect(heading(page, 'System')).toHaveAttribute('aria-expanded', 'false');
  await expect(heading(page, 'Operations')).toHaveAttribute('aria-expanded', 'false');
});

test('a closed group still says it holds the current page', async ({ page }) => {
  await page.goto('/tasks');
  const operations = heading(page, 'Operations');
  await expect(operations).not.toHaveAttribute('aria-current', 'true');
  await operations.click();
  await expect(link(page, 'Tasks')).toHaveCount(0);
  await expect(operations).toHaveAttribute('aria-current', 'true');
  await expect(operations).toHaveCSS('font-weight', '700');
  await expectAccessible(page);
});

test('works from the keyboard', async ({ page }) => {
  await page.goto('/tasks');
  await heading(page, 'Governance').focus();
  await page.keyboard.press('Enter');
  await expect(link(page, 'Audit trail')).toBeVisible();
  await page.keyboard.press('Space');
  await expect(link(page, 'Audit trail')).toHaveCount(0);
  await expect(heading(page, 'Governance')).toBeFocused();
});

test('a page beneath a destination keeps its group open', async ({ page }) => {
  await page.goto('/users/workspaces');
  await expect(link(page, 'Users and workspaces')).toHaveAttribute('aria-current', 'page');
  await expect(heading(page, 'Governance')).toHaveAttribute('aria-expanded', 'true');
});

test.describe('the filter', () => {
  const filter = (page: Page) => nav(page).getByRole('searchbox', { name: 'Filter pages' });

  test('finds pages in closed groups, and Enter opens the first', async ({ page }) => {
    await page.goto('/');
    await expect(link(page, 'Audit trail')).toHaveCount(0);
    await filter(page).fill('AUDIT');
    await expect(link(page, 'Audit trail')).toBeVisible();
    await expect(link(page, 'Overview')).toHaveCount(0);
    await expect(nav(page).getByRole('status')).toHaveText('1 page');
    await expect(heading(page, 'Governance')).toHaveCount(0);
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/audit/);
    await expect(filter(page)).toHaveValue('');
    await expect(heading(page, 'Governance')).toHaveAttribute('aria-expanded', 'true');
  });

  test('Enter passes over a page that has not shipped', async ({ page }) => {
    await page.goto('/');
    await filter(page).fill('a');
    await expect(nav(page).locator('[aria-disabled]').first()).toBeVisible();
    await filter(page).fill('alerts');
    await expect(nav(page).locator('[aria-disabled]')).toHaveCount(1);
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/$/);
  });

  test('says when nothing matches, and Escape clears it', async ({ page }) => {
    await page.goto('/tasks');
    await filter(page).fill('no such page');
    await expect(nav(page).getByRole('status')).toHaveText('No pages match');
    await expect(nav(page).getByRole('link')).toHaveCount(0);
    await expectAccessible(page);
    await page.keyboard.press('Escape');
    await expect(filter(page)).toHaveValue('');
    await expect(link(page, 'Tasks')).toBeVisible();
    await expect(nav(page).getByRole('status')).toHaveCount(0);
  });
});

test.describe('the icon-only rail', () => {
  const collapse = (page: Page) =>
    sidebar(page).getByRole('button', { name: 'Collapse navigation' });
  const expand = (page: Page) => sidebar(page).getByRole('button', { name: 'Expand navigation' });

  test('keeps every name, marks the current page and shows the name on focus', async ({ page }) => {
    await page.goto('/tasks');
    await collapse(page).click();
    await expect(expand(page)).toBeVisible();
    await expect(page).toHaveURL(/\/tasks$/);
    await expect(nav(page).getByRole('searchbox')).toHaveCount(0);
    await expect(link(page, 'Audit trail')).toBeVisible();
    await expect(link(page, 'Tasks')).toHaveAttribute('aria-current', 'page');
    const width = await sidebar(page).evaluate((element) => element.getBoundingClientRect().width);
    expect(width).toBe(56);
    await expectAccessible(page);

    await link(page, 'Audit trail').focus();
    const tip = sidebar(page).getByText('Audit trail', { exact: true }).last();
    await expect(tip).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(sidebar(page).locator('.fixed')).toHaveCount(0);
    await link(page, 'Audit trail').click();
    await expect(page).toHaveURL(/\/audit/);
  });

  test('shows the name on hover, and it stays while the pointer moves onto it', async ({
    page,
  }) => {
    await page.goto('/tasks');
    await collapse(page).click();
    await link(page, 'Nodes').hover();
    const tip = sidebar(page).locator('.fixed');
    await expect(tip).toHaveText('Nodes');
    await tip.hover();
    await expect(tip).toHaveText('Nodes');
    await page.mouse.move(900, 500);
    await expect(tip).toHaveCount(0);
  });

  test('follows the account, and comes back after a reload', async ({ page }) => {
    await page.goto('/tasks');
    await collapse(page).click();
    await expect.poll(() => control.preferences.navigation_collapsed).toBe(true);
    expect(control.preferenceWrites.at(-1)).toMatchObject({ navigation_collapsed: true });
    expect(control.preferenceWrites.at(-1)).not.toHaveProperty('theme');
    await page.reload();
    await expect(expand(page)).toBeVisible();
    await expand(page).click();
    await expect.poll(() => control.preferences.navigation_collapsed).toBe(false);
    await expect(collapse(page)).toBeVisible();
  });

  test('a choice made in another browser arrives with the account', async ({ page }) => {
    control.preferences.navigation_collapsed = true;
    await page.goto('/tasks');
    await expect(expand(page)).toBeVisible();
    await expect(nav(page).getByRole('searchbox')).toHaveCount(0);
  });

  test('can be chosen from the command menu', async ({ page }) => {
    await page.goto('/tasks');
    await expect(collapse(page)).toBeVisible();
    await page.keyboard.press('Control+k');
    await page.getByRole('option', { name: 'Collapse navigation' }).click();
    await expect(expand(page)).toBeVisible();
  });

  test('never applies to the drawer of a narrow screen', async ({ page }) => {
    control.preferences.navigation_collapsed = true;
    await page.setViewportSize({ width: 800, height: 700 });
    await page.goto('/tasks');
    await page.getByRole('button', { name: 'Open navigation' }).click();
    const drawer = page.getByRole('dialog');
    await expect(drawer.getByRole('link', { name: 'Tasks', exact: true })).toContainText('Tasks');
    await expect(drawer.getByRole('button', { name: 'Expand navigation' })).toHaveCount(0);
    await expect(drawer.getByRole('searchbox', { name: 'Filter pages' })).toBeVisible();
    await drawer.getByRole('button', { name: 'Governance', exact: true }).click();
    await drawer.getByRole('link', { name: 'Audit trail', exact: true }).click();
    await expect(page).toHaveURL(/\/audit/);
    await expect(drawer).toBeHidden();
  });
});

test.describe('pinned pages', () => {
  const pinnedList = (page: Page) => nav(page).getByRole('list').first();

  test('a pin sits at the top, stays in its group, and comes off', async ({ page }) => {
    await page.goto('/tasks');
    await expect(nav(page).getByText('Pinned', { exact: true })).toHaveCount(0);
    await nav(page).getByRole('button', { name: 'Pin Tasks' }).focus();
    await expect(nav(page).getByRole('button', { name: 'Pin Tasks' })).toHaveCSS('opacity', '1');
    await page.keyboard.press('Enter');
    await expect.poll(() => control.preferences.pinned_destinations).toEqual(['/tasks']);
    expect(control.preferenceWrites.at(-1)).toEqual(
      expect.objectContaining({ pinned_destinations: ['/tasks'] }),
    );
    await expect(nav(page).getByText('Pinned', { exact: true })).toBeVisible();
    await expect(nav(page).getByRole('link', { name: 'Tasks', exact: true })).toHaveCount(2);
    await expectAccessible(page);

    // From a closed group, in the order chosen.
    await heading(page, 'Governance').click();
    await nav(page).getByRole('button', { name: 'Pin Audit trail' }).click();
    await expect.poll(() => control.preferences.pinned_destinations).toEqual(['/tasks', '/audit']);
    await expect(pinnedList(page).getByRole('link')).toHaveText(['Tasks', 'Audit trail']);
    await pinnedList(page).getByRole('link', { name: 'Audit trail' }).click();
    await expect(page).toHaveURL(/\/audit/);
    await expect(pinnedList(page).getByRole('link', { name: 'Audit trail' })).toHaveAttribute(
      'aria-current',
      'page',
    );

    // Taking the last one off keeps focus in the sidebar.
    await pinnedList(page).getByRole('button', { name: 'Unpin Tasks' }).click();
    await pinnedList(page).getByRole('button', { name: 'Unpin Audit trail' }).click();
    await expect.poll(() => control.preferences.pinned_destinations).toEqual([]);
    await expect(nav(page).getByText('Pinned', { exact: true })).toHaveCount(0);
    await expect(nav(page).getByRole('searchbox')).toBeFocused();
  });

  test('a page that has not shipped cannot be pinned', async ({ page }) => {
    await page.goto('/tasks');
    await expect(nav(page).getByRole('button', { name: 'Pin Monitoring' })).toHaveCount(0);
  });

  test('pins arrive with the account, and a pin whose page has gone is left out', async ({
    page,
  }) => {
    control.preferences.pinned_destinations = ['/gone', '/license'];
    await page.goto('/tasks');
    await expect(pinnedList(page).getByRole('link')).toHaveText(['License']);
    await page.reload();
    await expect(pinnedList(page).getByRole('link')).toHaveText(['License']);
  });

  test('the rail shows pins first, with their names', async ({ page }) => {
    control.preferences.pinned_destinations = ['/license'];
    control.preferences.navigation_collapsed = true;
    await page.goto('/tasks');
    await expect(sidebar(page).getByRole('list', { name: 'Pinned' })).toContainText('License');
    await expectAccessible(page);
  });

  test('a filter looks through the groups, not the pins', async ({ page }) => {
    control.preferences.pinned_destinations = ['/license'];
    await page.goto('/tasks');
    await nav(page).getByRole('searchbox').fill('license');
    await expect(nav(page).getByRole('link', { name: 'License', exact: true })).toHaveCount(1);
    await expect(nav(page).getByText('Pinned', { exact: true })).toHaveCount(0);
  });

  test('the limit says what to do', async ({ page }) => {
    control.preferences.pinned_destinations = Array.from(
      { length: 12 },
      (_, index) => `/p${String(index)}`,
    );
    await page.goto('/tasks');
    await nav(page).getByRole('button', { name: 'Pin Tasks' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Unpin one' })).toBeVisible();
    expect(control.preferences.pinned_destinations).toHaveLength(12);
  });

  test('the command menu pins the current page', async ({ page }) => {
    await page.goto('/users/workspaces');
    await expect(link(page, 'Users and workspaces')).toBeVisible();
    await page.keyboard.press('Control+k');
    await page.getByRole('option', { name: 'Pin Users and workspaces' }).click();
    await expect.poll(() => control.preferences.pinned_destinations).toEqual(['/users']);
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('option', { name: 'Unpin Users and workspaces' })).toBeVisible();
  });

  test('the account page has no pin', async ({ page }) => {
    await page.goto('/account');
    await page.getByRole('heading', { name: 'Account', level: 1 }).waitFor();
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('option', { name: /^Pin / })).toHaveCount(0);
  });
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('the filter reads the names on screen', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    await page.goto('/');
    await nav(page)
      .getByRole('searchbox', { name: chinese['nav.filter'] })
      .fill(chinese['nav.audit']);
    await expect(link(page, chinese['nav.audit'])).toBeVisible();
    await expect(nav(page).getByRole('status')).toHaveText(
      chinese['nav.filter.count_other'].replace('{count}', '1'),
    );
  });
});
