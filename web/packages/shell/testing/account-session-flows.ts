import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test } from '@playwright/test';
import { expectAccessible } from './accessibility-flows.ts';
import { browserSession } from './browser-sessions.ts';
import { account, mockControlPlane, type ControlPlane } from './control-plane.ts';

const other = {
  ...browserSession,
  id: '22222222-2222-4222-8222-222222222222',
  current: false,
  authentication_method: 'sso' as const,
};

/** Shared Account session acceptance; screenshots still require a person to open and review them. */
export function accountSessionFlows(audience: ControlPlane['audience']): void {
  test.describe(`account sessions, ${audience}`, () => {
    let control: ControlPlane;
    const failures: string[] = [];
    test.beforeEach(async ({ page, baseURL }) => {
      failures.length = 0;
      page.on('request', (request) => {
        if (!request.url().startsWith(baseURL ?? '') && !request.url().startsWith('data:')) {
          failures.push(`outside request: ${request.url()}`);
        }
      });
      page.on('console', (message) => {
        if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
          failures.push(message.text());
        }
      });
      control = await mockControlPlane(page, audience);
      control.signedIn = true;
      control.browserSessions = [{ ...browserSession }, { ...other }];
    });
    test.afterEach(() => {
      expect(failures).toEqual([]);
    });

    for (const locale of ['en', 'zh-CN'] as const) {
      const text = locale === 'en' ? english : chinese;
      test(`reviews the settings section and confirmation in ${locale}`, async ({ page }, info) => {
        control.preferences.locale = locale;
        await page.goto('/account');
        const section = page.getByRole('region', { name: text['account.sessions.title'] });
        await expect(
          section.getByText(text['account.sessions.current'], { exact: true }),
        ).toBeVisible();
        expect(control.browserSessionReads[0]).toEqual({ cursor: null, background: true });
        const currentEnd = section.getByRole('listitem').first().getByRole('button');
        await expect(currentEnd).toHaveAttribute('aria-disabled', 'true');
        await currentEnd.focus();
        await page.keyboard.press('Enter');
        await expect(page.getByRole('dialog')).toHaveCount(0);
        await page.screenshot({ path: info.outputPath('account-desktop.png'), fullPage: true });
        await section.getByRole('listitem').nth(1).getByRole('button').click();
        const dialog = page.getByRole('dialog', { name: text['account.sessions.confirm.title'] });
        await expect(dialog.getByRole('textbox')).toBeFocused();
        await expect(
          dialog.getByRole('button', { name: text['account.sessions.end'] }),
        ).toBeDisabled();
        await page.screenshot({ path: info.outputPath('end-confirmation.png'), fullPage: true });
        await dialog.getByRole('textbox').fill(other.id);
        await dialog.getByRole('button', { name: text['account.sessions.end'] }).click();
        await expect(dialog).toHaveCount(0);
        await expect(section.getByText(text['account.sessions.onlyCurrent'])).toBeVisible();
        expect(control.browserSessionEnds).toEqual([other.id]);
        expect(control.browserSessionReads.at(-1)?.background).toBe(false);
        await page.screenshot({ path: info.outputPath('current-only.png'), fullPage: true });
        control.browserSessions = [];
        await section
          .getByRole('button', { name: text['account.sessions.refresh'], exact: true })
          .click();
        await expect(section.getByText(text['account.sessions.empty.title'])).toBeVisible();
        await page.screenshot({ path: info.outputPath('empty-live-list.png'), fullPage: true });
        control.browserSessions = [{ ...browserSession }, { ...other }];
        await section
          .getByRole('button', { name: text['account.sessions.refresh'], exact: true })
          .first()
          .click();
        await expect(section.getByRole('listitem')).toHaveCount(2);
        await page.setViewportSize({ width: 390, height: 844 });
        await expectAccessible(page);
        expect(
          await page.locator('main').evaluate((main) => main.scrollWidth - main.clientWidth),
        ).toBe(0);
        await page.screenshot({ path: info.outputPath('account-390.png'), fullPage: true });
        await page.setViewportSize({ width: 360, height: 780 });
        await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
        await expectAccessible(page);
        expect(
          await page.locator('main').evaluate((main) => main.scrollWidth - main.clientWidth),
        ).toBe(0);
        await page.screenshot({ path: info.outputPath('account-narrow-dark.png'), fullPage: true });
      });
    }

    test('replaces the current marker after a real same-account sign-out and sign-in', async ({
      page,
    }, info) => {
      await page.goto('/account');
      const section = page.getByRole('region', { name: 'Sessions', exact: true });
      await expect(
        section.getByText(`Session ${browserSession.id}`, { exact: true }),
      ).toBeVisible();
      await page.getByRole('button', { name: 'Account menu' }).click();
      await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
      await page.getByLabel('Email').fill(account.email);
      await page.getByLabel('Password', { exact: true }).fill(account.password);
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await expect(page).not.toHaveURL(/\/sign-in/);
      await page.goto('/account');
      const current = section.getByRole('listitem').filter({ hasText: 'Current session' });
      await expect(current).toHaveCount(1);
      const newId = control.browserSessions.find((row) => row.current)?.id;
      expect(newId).toBeTruthy();
      expect(newId).not.toBe(browserSession.id);
      await expect(current.getByText(`Session ${newId ?? ''}`, { exact: true })).toBeVisible();
      await expect(section.getByText(`Session ${browserSession.id}`, { exact: true })).toHaveCount(
        0,
      );
      await page.screenshot({ path: info.outputPath('same-account-relogin.png'), fullPage: true });
    });

    test('replaces the current marker through the actual password form', async ({ page }, info) => {
      await page.goto('/account');
      const section = page.getByRole('region', { name: 'Sessions', exact: true });
      await expect(section.getByRole('listitem')).toHaveCount(2);
      await page.getByLabel('Current password').fill(account.password);
      await page.getByLabel('New password', { exact: true }).fill('another sufficient password');
      await page.getByLabel('Confirm the password').fill('another sufficient password');
      await page.getByRole('button', { name: 'Change password', exact: true }).click();
      await expect(section.getByRole('listitem')).toHaveCount(1);
      const newId = control.browserSessions[0]?.id;
      expect(newId).toBeTruthy();
      expect(newId).not.toBe(browserSession.id);
      await expect(section.getByText(`Session ${newId ?? ''}`, { exact: true })).toBeVisible();
      await expect(section.getByText('Current session', { exact: true })).toBeVisible();
      await expect(section.getByText(`Session ${browserSession.id}`, { exact: true })).toHaveCount(
        0,
      );
      expect(control.browserSessionReads.at(-1)?.background).toBe(true);
      await page.screenshot({ path: info.outputPath('password-rotation.png'), fullPage: true });
    });

    test('keeps earlier pages on an invalid cursor and failed restart', async ({ page }, info) => {
      control.browserSessionPageSize = 1;
      await page.goto('/account');
      const section = page.getByRole('region', { name: 'Sessions', exact: true });
      await expect(section.getByRole('listitem')).toHaveCount(1);
      control.browserSessionReadProblem = { status: 400, code: 'INVALID_CURSOR' };
      await section.getByRole('button', { name: 'Load more sessions' }).click();
      await expect(section.getByRole('alert')).toBeVisible();
      await expect(section.getByRole('listitem')).toHaveCount(1);
      await page.screenshot({ path: info.outputPath('pagination-partial.png'), fullPage: true });
      control.browserSessionReadProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await section.getByRole('button', { name: 'Restart from the first page' }).click();
      await expect(section.getByRole('button', { name: 'Read again' })).toBeVisible();
      await expect(section.getByRole('listitem')).toHaveCount(1);
      control.browserSessionReadProblem = null;
      await section.getByRole('button', { name: 'Read again' }).click();
      await expect(section.getByRole('alert')).toHaveCount(0);
    });

    for (const [status, code] of [
      [403, 'ACCESS_DENIED'],
      [503, 'SERVICE_NOT_READY'],
    ] as const) {
      test(`shows ${code} without retained session actions`, async ({ page }, info) => {
        await page.goto('/account');
        const section = page.getByRole('region', { name: 'Sessions', exact: true });
        await expect(section.getByRole('listitem')).toHaveCount(2);
        control.browserSessionReadProblem = { status, code };
        await section.getByRole('button', { name: 'Refresh' }).click();
        await expect(section.getByRole('alert')).toBeVisible();
        await expect(section.getByRole('listitem')).toHaveCount(0);
        await expect(page.getByLabel('Display name')).toBeEnabled();
        if (code === 'SERVICE_NOT_READY')
          await expect(section.getByRole('button', { name: 'End session' })).toHaveAttribute(
            'aria-disabled',
            'true',
          );
        await page.screenshot({ path: info.outputPath(`${code}.png`), fullPage: true });
      });
    }

    for (const [status, code] of [
      [404, 'RESOURCE_NOT_FOUND'],
      [409, 'STORAGE_COMMIT_OUTCOME_UNKNOWN'],
    ] as const) {
      test(`reads before another attempt after ${code}`, async ({ page }) => {
        await page.goto('/account');
        const section = page.getByRole('region', { name: 'Sessions', exact: true });
        await expect(section.getByRole('listitem')).toHaveCount(2);
        control.browserSessionEndProblem = { status, code };
        await section.getByRole('listitem').nth(1).getByRole('button').click();
        const dialog = page.getByRole('dialog');
        await dialog.getByRole('textbox').fill(other.id);
        await dialog.getByRole('button', { name: 'End session' }).click();
        await expect(dialog).toHaveCount(0);
        await expect(section.getByText('The session end could not be confirmed')).toBeVisible();
        expect(control.browserSessionEnds).toEqual([other.id]);
        await section.getByRole('button', { name: 'Read again' }).click();
        await expect(section.getByRole('alert')).toHaveCount(0);
        expect(control.browserSessionEnds).toEqual([other.id]);
      });
    }

    test('keeps the settings page usable during a delayed first read', async ({ page }, info) => {
      let release = () => {
        /* Replaced when the barrier is created. */
      };
      const barrier = new Promise<undefined>((resolve) => {
        release = () => {
          resolve(undefined);
        };
      });
      await page.route('**/api/v1/me/sessions', async (route) => {
        await barrier;
        await route.fallback();
      });
      await page.goto('/account');
      const section = page.getByRole('region', { name: 'Sessions', exact: true });
      await expect(section.locator('.animate-pulse').first()).toBeVisible();
      await expect(page.getByLabel('Display name')).toBeEnabled();
      await page.screenshot({ path: info.outputPath('loading-delayed.png'), fullPage: true });
      release();
      await expect(section.getByRole('listitem')).toHaveCount(2);
    });

    test('does not replay a lost end reply even when the server already ended the target', async ({
      page,
    }) => {
      let deletes = 0;
      await page.route(`**/api/v1/me/sessions/${other.id}`, async (route) => {
        deletes += 1;
        control.browserSessions = control.browserSessions.filter((row) => row.id !== other.id);
        await route.abort('connectionreset');
      });
      await page.goto('/account');
      const section = page.getByRole('region', { name: 'Sessions', exact: true });
      await expect(section.getByRole('listitem')).toHaveCount(2);
      await section.getByRole('listitem').nth(1).getByRole('button').click();
      const dialog = page.getByRole('dialog');
      await dialog.getByRole('textbox').fill(other.id);
      await dialog.getByRole('button', { name: 'End session' }).click();
      await expect(dialog).toHaveCount(0);
      await section.getByRole('button', { name: 'Read again' }).click();
      await expect(section.getByRole('listitem')).toHaveCount(1);
      expect(deletes).toBe(1);
      await expect(section.getByText(english['account.sessions.ended'])).toHaveCount(0);
    });
  });
}
