// The models a person may call (S-304): which can be called now, what is behind each name, and what
// the page says when part of that cannot be read (docs/research/portal-model-catalogue.md).
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import {
  expectAccessible,
  deploymentAlias,
  derivedModel,
  mockControlPlane,
  readyModel,
  runningDeployment,
  unavailableAlias,
  type ControlPlane,
  defaultWorkspace,
  teamWorkspace,
  expectFocusVisible,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

/** Every API path the page called, without the origin. */
let called: string[];

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
  control.aliases = [deploymentAlias, unavailableAlias];
  control.deployments = [runningDeployment];
  control.models = [readyModel];
  called = [];
  page.on('request', (request) => {
    const { pathname } = new URL(request.url());
    if (pathname.startsWith('/api/')) called.push(pathname);
  });
});

for (const [locale, text] of [
  ['en', english],
  ['zh-CN', chinese],
] as const) {
  test.describe(`workspace description ${locale}`, () => {
    test.use({ locale });
    test('reads only the selected workspace description as text and exposes no editor', async ({
      page,
    }, info) => {
      if (locale === 'zh-CN') await page.setViewportSize({ width: 390, height: 844 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      const first = 'Support requests.\n<img src="https://outside.example.invalid/a">';
      const second = 'Research only; verify every answer.';
      const foreign = {
        ...deploymentAlias,
        workspace_id: teamWorkspace.id,
        description: second,
        description_revision: 4,
      };
      control.aliases = [{ ...deploymentAlias, description: first, description_revision: 2 }];
      await page.route('**/distribution/aliases?*', (route) => {
        const workspace = new URL(route.request().url()).pathname.split('/').at(-3);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              items: workspace === defaultWorkspace.id ? control.aliases : [foreign],
              next_cursor: null,
            },
          }),
        });
      });
      const outside: string[] = [];
      page.on('request', (request) => {
        if (request.url().startsWith('https://outside.example.invalid/'))
          outside.push(request.url());
      });
      await page.goto('/models');
      const row = page.getByRole('row', { name: new RegExp(deploymentAlias.name) });
      await row.getByRole('button').first().click();
      const drawer = page.getByRole('dialog', { name: deploymentAlias.name, exact: true });
      await expect(drawer.getByText(first, { exact: true })).toBeVisible();
      await expect(drawer.locator('img, textarea')).toHaveCount(0);
      await expect(
        drawer.getByRole('button', { name: text['services.gateway.description.edit'] }),
      ).toHaveCount(0);
      await expectAccessible(page);
      await expectFocusVisible(page);
      await info.attach(`portal-description-read-${locale}`, {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });
      await page.keyboard.press('Escape');
      const switchName = text['workspace.switch'].replace('{name}', defaultWorkspace.name);
      await page.getByRole('button', { name: switchName, exact: true }).click();
      await page.getByRole('menuitem', { name: teamWorkspace.name, exact: true }).click();
      await row.getByRole('button').first().click();
      await expect(drawer.getByText(second, { exact: true })).toBeVisible();
      await expect(drawer.getByText(first, { exact: true })).toHaveCount(0);
      await page.keyboard.press('Escape');
      await page
        .getByRole('button', {
          name: text['workspace.switch'].replace('{name}', teamWorkspace.name),
          exact: true,
        })
        .click();
      await page.getByRole('menuitem', { name: defaultWorkspace.name, exact: true }).click();
      await row.getByRole('button').first().click();
      await expect(drawer.getByText(first, { exact: true })).toBeVisible();
      expect(outside).toEqual([]);
      expect(control.publishWrites).toEqual([]);
    });
  });
}

async function open(page: Page, path = '/models') {
  await page.goto(path);
  await page.getByRole('heading', { name: 'Models', level: 1 }).waitFor();
}

test('is reached from the navigation and lists what may be called', async ({ page }) => {
  await page.goto('/');
  const navigation = page.getByRole('navigation');
  await navigation.getByRole('button', { name: 'AI tools' }).click();
  await navigation.getByRole('link', { name: 'Models' }).click();
  await expect(page.getByRole('heading', { name: 'Models', level: 1 })).toBeVisible();
  const row = page.getByRole('row', { name: /support-chat/ });
  await expect(row.getByText('Qwen3-0.6B')).toBeVisible();
  await expect(row.getByText('Published model')).toBeVisible();
  await expect(row.getByText('2,048 tokens')).toBeVisible();
  await expect(row.getByText('Can be called')).toBeVisible();
  await expect(
    page.getByRole('row', { name: /archive-search/ }).getByText('Not available'),
  ).toBeVisible();
});

test('reads only the portal paths of the chosen workspace', async ({ page }) => {
  await open(page);
  await page.getByRole('row', { name: /support-chat/ }).waitFor();
  expect(called.filter((path) => path.includes('/admin'))).toEqual([]);
  const workspace = deploymentAlias.workspace_id;
  for (const read of ['distribution/aliases', 'deployments', 'models']) {
    expect(called.some((path) => path.endsWith(`/workspaces/${workspace}/${read}`))).toBe(true);
  }
});

test('leaves out a name that can never be called again', async ({ page }) => {
  control.aliases = [
    deploymentAlias,
    { ...unavailableAlias, name: 'withdrawn', disabled_at: '2026-11-07T00:00:00Z' },
  ];
  await open(page);
  await page.getByRole('row', { name: /support-chat/ }).waitFor();
  await expect(page.getByRole('row', { name: /withdrawn/ })).toHaveCount(0);
});

test('says what a fine-tuned model comes from', async ({ page }) => {
  control.deployments = [{ ...runningDeployment, model_id: derivedModel.id }];
  control.models = [derivedModel];
  await open(page);
  await page.getByRole('button', { name: 'Show support-chat' }).click();
  const drawer = page.getByRole('dialog', { name: 'support-chat' });
  await expect(drawer.getByText('Fine-tuned here')).toBeVisible();
  await expect(drawer.getByText('Qwen/Qwen3-0.6B')).toBeVisible();
});

test('shows what a predecessor knows about calling a model and leads to a key', async ({
  page,
}) => {
  await open(page);
  await page.getByRole('button', { name: 'Show support-chat' }).click();
  const drawer = page.getByRole('dialog', { name: 'support-chat' });
  await expect(drawer.getByText('https://gateway.example.test/v1')).toBeVisible();
  await expect(drawer.getByText('Apache-2.0')).toBeVisible();
  await expect(drawer.getByText('Chat and text generation', { exact: false })).toBeVisible();
  await expect(drawer.getByText('The administrator has not added a description yet.')).toHaveCount(
    0,
  );
  await expect(drawer.getByText('Price', { exact: true })).toHaveCount(0);
  const issue = drawer.getByRole('link', { name: 'Issue a key' });
  await expect(issue).toHaveAttribute('href', /\/keys#issue$/);
  await issue.click();
  // The keys page opens the dialog and then drops the hash, so the address ends without it.
  await expect(page.getByRole('dialog', { name: 'Issue a key' })).toBeVisible();
  await expect(page).toHaveURL(/\/keys$/);
});

test('says what to expect from a model that cannot be called now', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: 'Show archive-search' }).click();
  const drawer = page.getByRole('dialog', { name: 'archive-search' });
  await expect(drawer.getByText('This model cannot be called now')).toBeVisible();
  await expect(
    drawer.getByText('until an administrator starts it', { exact: false }),
  ).toBeVisible();
});

test('keeps trying a model visible but not usable, with the reason', async ({ page }) => {
  await open(page);
  const trying = page.getByRole('button', { name: 'Try a model' });
  await expect(trying).toHaveAttribute('aria-disabled', 'true');
  await expect(trying).toHaveAccessibleDescription('The playground is not ready in this release.');
  await page.getByRole('link', { name: 'Issue a key' }).click();
  await expect(page.getByRole('dialog', { name: 'Issue a key' })).toBeVisible();
});

test('filters by tile and search, in the address', async ({ page }) => {
  await open(page);
  await page.getByRole('button', { name: /Not available/ }).click();
  await expect(page).toHaveURL(/tile=unavailable/);
  await expect(page.getByRole('row', { name: /support-chat/ })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('row', { name: /archive-search/ })).toBeVisible();
  await page.getByRole('button', { name: /Not available/ }).click();
  await page.getByLabel('Search models').fill('nothing here');
  await expect(page.getByRole('heading', { name: 'No models match these filters' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('row', { name: /support-chat/ })).toBeVisible();
});

test('finds a model by what is behind its name', async ({ page }) => {
  await open(page);
  await page.getByLabel('Search models').fill('qwen3');
  await expect(page.getByRole('row', { name: /support-chat/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /archive-search/ })).toBeVisible();
});

test('says so when the list cannot be read, and tries again', async ({ page }) => {
  let refuse = true;
  await page.route(/\/distribution\/aliases\?/, (route) =>
    refuse
      ? route.fulfill({
          status: 503,
          contentType: 'application/problem+json',
          body: JSON.stringify({ title: 'x', status: 503, code: 'STORAGE_UNAVAILABLE' }),
        })
      : route.fallback(),
  );
  await open(page);
  const retry = page.getByRole('button', { name: 'Try again' });
  await retry.waitFor();
  await expect(page.getByRole('row', { name: /support-chat/ })).toHaveCount(0);
  refuse = false;
  await retry.click();
  await expect(page.getByRole('row', { name: /support-chat/ })).toBeVisible();
});

test('keeps the names when what is behind them cannot be read, and recovers', async ({ page }) => {
  control.deploymentsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await open(page);
  const row = page.getByRole('row', { name: /support-chat/ });
  const retry = page.getByRole('button', { name: 'Try again' });
  await retry.waitFor();
  await expect(page.getByText('Some model details could not load')).toBeVisible();
  await expect(row.getByText('Not known').first()).toBeVisible();
  await expect(row.getByText('Can be called')).toBeVisible();
  control.deploymentsProblem = null;
  await retry.click();
  await expect(row.getByText('Qwen3-0.6B')).toBeVisible();
  await expect(page.getByText('Some model details could not load')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Try again' })).toHaveCount(0);
});

test('does not call a fact unknown while it is still loading', async ({ page }) => {
  let hold = true;
  await page.route(/\/workspaces\/[0-9a-f-]{36}\/models$/, async (route) => {
    while (hold) await new Promise((done) => setTimeout(done, 20));
    await route.fallback();
  });
  await open(page);
  const row = page.getByRole('row', { name: /support-chat/ });
  await row.getByText('Can be called').waitFor();
  await expect(row.getByText('Not known')).toHaveCount(0);
  hold = false;
  await expect(row.getByText('Qwen3-0.6B')).toBeVisible();
});

test('says who publishes models when there are none', async ({ page }) => {
  control.aliases = [];
  await open(page);
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
});

test('says what to do without a workspace', async ({ page }) => {
  control.workspaceList = [];
  control.sessionWorkspace = null;
  await open(page);
  await expect(page.getByRole('heading', { name: 'No workspace yet' })).toBeVisible();
  const issue = page.getByRole('button', { name: 'Issue a key' });
  await expect(issue).toHaveAttribute('aria-disabled', 'true');
  await expect(issue).toHaveAccessibleDescription(/administrator adds you to a workspace/);
});

test.describe('with a browser that prefers Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('distinguishes a description from an explicit empty description', async ({ page }, info) => {
    const description = 'Review support requests.\n<img src="https://outside.example.invalid/a">';
    control.aliases = [
      { ...deploymentAlias, description, description_revision: 1 },
      { ...unavailableAlias, description: '', description_revision: 1 },
    ];
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/models');
    const described = page.getByRole('dialog', { name: deploymentAlias.name });
    await page
      .getByRole('row', { name: new RegExp(deploymentAlias.name) })
      .getByRole('button')
      .first()
      .click();
    await expect(
      described.getByText(chinese['portalModels.detail.goodFor'], { exact: true }),
    ).toBeVisible();
    const text = described.getByText('Review support requests.', { exact: false });
    await expect(text).toBeVisible();
    expect(await text.textContent()).toBe(description);
    await expect(described.locator('img')).toHaveCount(0);
    await expectAccessible(page);
    await info.attach('portal-model-description-zh-cn', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    await page.keyboard.press('Escape');
    const empty = page.getByRole('dialog', { name: unavailableAlias.name });
    await page
      .getByRole('row', { name: new RegExp(unavailableAlias.name) })
      .getByRole('button')
      .first()
      .click();
    await expect(empty.getByText(chinese['portalModels.detail.goodFor.chat'])).toBeVisible();
    await expect(empty.getByText(chinese['portalModels.detail.goodFor.empty'])).toBeVisible();
  });

  test('explains and recovers missing facts in Simplified Chinese', async ({ page }, info) => {
    control.deploymentsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/models');
    await expect(
      page.getByRole('heading', { name: chinese['portalModels.title'], level: 1 }),
    ).toBeVisible();
    const row = page.getByRole('row', { name: /support-chat/ });
    await expect(row.getByText(chinese['portalModels.state.callable'])).toBeVisible();
    await expect(page.getByRole('button', { name: chinese['portalModels.try'] })).toBeVisible();
    await expect(page.getByText(chinese['portalModels.factsUnavailable.title'])).toBeVisible();
    await expect(page.getByText(chinese['portalModels.factsUnavailable.detail'])).toBeVisible();
    await info.attach('portal-model-facts-unavailable-zh-cn', {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
    control.deploymentsProblem = null;
    await page.getByRole('button', { name: chinese['action.retry'] }).click();
    await expect(row.getByText('Qwen3-0.6B')).toBeVisible();
    await expect(page.getByText(chinese['portalModels.factsUnavailable.title'])).toHaveCount(0);
  });
});
