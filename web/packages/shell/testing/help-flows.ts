// Browser flows of the in-product help (plan S-107, S-108), shared by both entrances: the help of
// the page on screen, search on the page and in the command menu, links between articles and
// pages, the API reference of this entrance only, and both languages, with nothing fetched from
// outside the installation.
import { article } from '@ownsolid/help/catalog';
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test } from '@playwright/test';
import { mockControlPlane, type ControlPlane } from './control-plane.ts';

/** Registers the help flows for one entrance. */
export function helpFlows(audience: ControlPlane['audience']): void {
  test.describe('help', () => {
    let control: ControlPlane;
    const elsewhere: string[] = [];

    test.beforeEach(async ({ page, baseURL }) => {
      control = await mockControlPlane(page, audience);
      control.signedIn = true;
      elsewhere.length = 0;
      page.on('request', (request) => {
        if (new URL(request.url()).origin !== new URL(baseURL ?? '').origin) {
          elsewhere.push(request.url());
        }
      });
    });

    test.afterEach(() => {
      expect(elsewhere).toEqual([]);
    });

    test('the top bar opens the article about the page on screen', async ({ page }) => {
      await page.goto('/account');
      await page.getByRole('heading', { name: 'Account', level: 1 }).waitFor();
      await page.getByRole('link', { name: 'Help for this page' }).click();
      await expect(page).toHaveURL(/\/help\/account$/);
      await expect(page.getByRole('heading', { name: 'Your account', level: 1 })).toBeVisible();
      // Each section is an anchor a link can point at.
      await expect(page.locator('#what-you-can-change')).toBeVisible();
      const entrance = audience === 'portal' ? 'portal' : 'console';
      const summary = article(entrance, 'en', 'account')?.summary;
      expect(summary).toBeTruthy();
      await expect(page.getByText(summary ?? '', { exact: true })).toHaveCount(1);
    });

    test('search narrows the articles, says when nothing matches and clears', async ({ page }) => {
      await page.goto('/help');
      await expect(page.getByRole('link', { name: /^Your account/ })).toBeVisible();
      await page.getByLabel('Search help').fill('theme');
      await expect(page.getByRole('link', { name: /^Your account/ })).toBeHidden();
      await page.getByLabel('Search help').fill('nothing-has-this-name');
      await expect(page.getByRole('heading', { name: 'No article matches' })).toBeVisible();
      await expect(page).toHaveURL(/q=nothing-has-this-name/);
      await page.getByRole('button', { name: 'Clear search' }).click();
      await expect(page.getByRole('link', { name: /^Your account/ })).toBeVisible();
    });

    test('an article links to the page it explains', async ({ page }) => {
      await page.goto('/help/what-is-supported');
      await page.getByRole('main').getByRole('link', { name: 'What is supported' }).click();
      await expect(
        page.getByRole('heading', { name: 'What is supported', level: 1 }),
      ).toBeVisible();
    });

    test('an unknown article says so and leads back', async ({ page }) => {
      await page.goto('/help/nothing-has-this-name');
      await expect(
        page.getByRole('heading', { name: 'This article could not be found' }),
      ).toBeVisible();
      await page.getByRole('link', { name: 'Go to all help' }).click();
      await expect(page.getByRole('heading', { name: 'Help', level: 1 })).toBeVisible();
    });

    test('the command menu finds an article without the network', async ({ page }) => {
      await page.goto('/help');
      await page.getByRole('heading', { name: 'Help', level: 1 }).waitFor();
      await page.keyboard.press('ControlOrMeta+k');
      await page.getByPlaceholder(/^Type a command/).fill('account');
      await page.getByRole('option', { name: /Your account/ }).click();
      await expect(page.getByRole('heading', { name: 'Your account', level: 1 })).toBeVisible();
    });

    for (const [locale, messages] of [
      ['en', english],
      ['zh-CN', chinese],
    ] as const) {
      test(`the published operation explains its error in ${locale}`, async ({ page }, info) => {
        control.preferences.locale = locale;
        await page.goto('/help/api');
        await page.getByLabel(messages['help.api.search']).fill('password');
        const operation = page.getByRole('button', { name: /changePassword/ });
        await operation.click();
        await expect(page).toHaveURL(/#identity\.changePassword$/);
        await expect(page.getByRole('heading', { name: messages['help.api.body'] })).toBeVisible();
        await page.reload();
        await expect(operation).toHaveAttribute('aria-expanded', 'true');
        const detail = page.locator('[id="identity.changePassword-detail"]');
        await expect(
          detail.getByText(messages['help.api.parameters.none'], { exact: true }),
        ).toBeVisible();
        await expect(
          detail.getByRole('table', { name: messages['help.api.parameters'], exact: true }),
        ).toHaveCount(0);
        const error = detail.getByRole('listitem').filter({
          has: page.getByText('PASSWORD_CURRENT_INVALID', { exact: true }),
        });
        await expect(error.getByText('403', { exact: true })).toBeVisible();
        await expect(error).toContainText(messages['problem.PASSWORD_CURRENT_INVALID.title']);
        await expect(error).toContainText(messages['problem.PASSWORD_CURRENT_INVALID.detail']);
        const save = page.getByRole('link', {
          name: messages['help.api.save.label'].replace('{domain}', 'identity'),
        });
        await expect(save).toHaveAttribute('download', 'identity.openapi.json');
        await detail
          .getByText(messages['help.api.parameters.none'], { exact: true })
          .scrollIntoViewIfNeeded();
        await info.attach(`password-parameters-${locale}`, {
          body: await page.screenshot({ animations: 'disabled' }),
          contentType: 'image/png',
        });
      });

      test(`the published workspace parameter keeps its meaning in ${locale}`, async ({
        page,
      }, info) => {
        control.preferences.locale = locale;
        if (locale === 'zh-CN') await page.setViewportSize({ width: 390, height: 844 });
        const operation = audience === 'admin' ? 'getAdminWorkspace' : 'getPortalWorkspace';
        await page.goto(`/help/api?q=${operation}#workspaces.${operation}`);
        await expect(page.getByRole('button', { name: new RegExp(operation) })).toHaveAttribute(
          'aria-expanded',
          'true',
        );
        const detail = page.locator(`[id="workspaces.${operation}-detail"]`);
        const parameters = detail.getByRole('table', {
          name: messages['help.api.parameters'],
          exact: true,
        });
        const workspace = parameters.getByRole('row').filter({
          has: page.getByText('workspace_id', { exact: false }),
        });
        await expect(workspace).toHaveCount(1);
        await expect(
          workspace.getByRole('cell', { name: messages['help.api.in.path'], exact: true }),
        ).toBeVisible();
        await expect(workspace).toContainText(messages['help.api.required']);
        await expect(workspace).toContainText('uuid');
        await info.attach(`workspace-parameter-${locale}`, {
          body: await page.screenshot({ animations: 'disabled' }),
          contentType: 'image/png',
        });
      });

      if (audience === 'admin') {
        test(`the published apply-cutover errors explain recovery in ${locale}`, async ({
          page,
        }, info) => {
          control.preferences.locale = locale;
          if (locale === 'zh-CN') await page.setViewportSize({ width: 390, height: 844 });
          await page.goto('/help/api?q=adminCutoverAlias#distribution.adminCutoverAlias');
          await expect(page.getByRole('button', { name: /adminCutoverAlias/ })).toHaveAttribute(
            'aria-expanded',
            'true',
          );
          const detail = page.locator('[id="distribution.adminCutoverAlias-detail"]');
          for (const code of [
            'ALIAS_PAUSE_REQUIRED',
            'ALIAS_ALREADY_PAUSED',
            'ALIAS_NOT_PAUSED',
            'ALIAS_REQUESTS_UNDRAINED',
            'ALIAS_ROLLBACK_TARGET_INVALID',
          ] as const) {
            const error = detail.getByRole('listitem').filter({
              has: page.getByText(code, { exact: true }),
            });
            await expect(error.getByText('409', { exact: true })).toBeVisible();
            await expect(error).toContainText(messages[`problem.${code}.title`]);
            await expect(error).toContainText(messages[`problem.${code}.detail`]);
          }
          await detail.getByText('ALIAS_PAUSE_REQUIRED', { exact: true }).scrollIntoViewIfNeeded();
          await info.attach(`apply-cutover-errors-${locale}`, {
            body: await page.screenshot({ animations: 'disabled' }),
            contentType: 'image/png',
          });
        });
      }
    }

    test('the API reference lists only what this entrance serves', async ({ page }) => {
      await page.goto('/help/api?q=admin%2Fnodes');
      if (audience === 'portal') {
        await expect(page.getByRole('heading', { name: 'No operation matches' })).toBeVisible();
      } else {
        await expect(page.getByRole('button', { name: /\/admin\/nodes/ }).first()).toBeVisible();
      }
    });

    test('reads in Chinese', async ({ page }) => {
      control.preferences.locale = 'zh-CN';
      await page.goto('/help/account');
      const title =
        article(audience === 'admin' ? 'console' : 'portal', 'zh-CN', 'account')?.title ?? '';
      await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
      await page.getByRole('link', { name: chinese['help.back'] }).click();
      await expect(
        page.getByRole('heading', { name: chinese['help.title'], level: 1 }),
      ).toBeVisible();
    });
  });
}
