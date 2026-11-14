// Scripted transport faults establish UI dispatch and recovery, not durable audit commits.
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  licenseEvent,
  memberUser,
  mockControlPlane,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page, type Route } from '@playwright/test';

const endpoint = '**/api/v1/admin/audit-events?*';
const requestId = '6f1c7a1e-0000-4000-8000-0000000000fa';
const paths = {
  trail: '/audit',
  activity: `/users/${memberUser.id}?tab=activity`,
} as const;
type Fault =
  'none' | 'lost-response' | 'network' | 'timeout' | 'unknown' | 'unavailable' | 'invalid-response';

async function refuse(route: Route, status: number, code: string) {
  await route.fulfill({
    status,
    contentType: 'application/problem+json',
    headers: { 'X-Request-ID': requestId },
    body: JSON.stringify({ code, status, request_id: requestId }),
  });
}

function history(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    ...licenseEvent,
    id: `6f1c7a1e-0000-4000-8000-${String(index).padStart(12, '0')}`,
    action: 'account.updated',
    target_type: 'user',
    target_id: memberUser.id,
    actor_user_id: memberUser.id,
    actor_email: memberUser.email,
    reason: null,
  }));
}

async function configure(page: Page, locale: 'en' | 'zh-CN'): Promise<ControlPlane> {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.preferences.locale = locale;
  control.users = [memberUser];
  control.auditEvents = history(1);
  await page.setViewportSize(
    locale === 'en' ? { width: 1440, height: 900 } : { width: 390, height: 844 },
  );
  return control;
}

async function focusAndReconnect(page: Page) {
  const other = await page.context().newPage();
  await other.bringToFront();
  await page.bringToFront();
  await other.close();
  await page.context().setOffline(true);
  await page.context().setOffline(false);
  await page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('focus'));
    window.dispatchEvent(new Event('online'));
  });
  // The old global policy retries after one then two seconds. Observe that whole window.
  await page.waitForTimeout(3_500);
}

for (const [locale, messages] of [
  ['en', english],
  ['zh-CN', chinese],
] as const) {
  test(`finds explicit audit recovery in Help in ${locale}`, async ({ page }, info) => {
    await configure(page, locale);
    await page.goto('/help');
    const search = page.getByLabel(messages['help.search'], { exact: true });
    await search.fill('no-audit-answer-matches-this');
    await expect(
      page.getByRole('heading', { name: messages['help.empty.title'], exact: true }),
    ).toBeVisible();
    const term = locale === 'en' ? 'confirmed' : '\u65e0\u6cd5\u786e\u8ba4';
    await search.fill(term);
    await expect(page).toHaveURL(new RegExp(`[?&]q=${encodeURIComponent(term)}(?:&|$)`));
    await page
      .getByRole('main')
      .getByRole('link', { name: new RegExp(`^${messages['audit.title']}`) })
      .click();
    const heading = page.getByRole('heading', {
      name: messages['audit.read.unknown.title'],
      exact: true,
    });
    await expect(heading).toBeVisible();
    await expect(page.getByRole('main')).toContainText(messages['audit.read.again']);
    await heading.scrollIntoViewIfNeeded();
    await info.attach(`audit-read-help-${locale}`, {
      body: await page.screenshot({ animations: 'disabled', fullPage: true }),
      contentType: 'image/png',
    });
    await page
      .getByRole('link', { name: messages['audit.read.unknown.title'], exact: true })
      .click();
    await expect(page.getByRole('main').locator('p').last()).toBeInViewport({ ratio: 1 });
    await info.attach(`audit-read-help-section-${locale}`, {
      body: await page.screenshot({ animations: 'disabled' }),
      contentType: 'image/png',
    });
  });
  for (const surface of ['trail', 'activity'] as const) {
    for (const initial of [
      'lost-response',
      'network',
      'timeout',
      'unknown',
      'unavailable',
      'invalid-response',
    ] as const) {
      test(`${surface} waits for deliberate recovery after ${initial} in ${locale}`, async ({
        page,
      }, info) => {
        const control = await configure(page, locale);
        if (initial === 'timeout') control.requestTimeoutMilliseconds = 1_000;
        let fault: Fault = initial;
        const attempts: URLSearchParams[] = [];
        const held: (() => void)[] = [];
        let fixtureAcceptedReads = 0;
        await page.route(endpoint, async (route) => {
          attempts.push(new URL(route.request().url()).searchParams);
          switch (fault) {
            case 'lost-response':
              fixtureAcceptedReads += 1;
              await route.abort('failed');
              return;
            case 'network':
              await route.abort('connectionrefused');
              return;
            case 'timeout':
              await new Promise<void>((resolve) => {
                held.push(resolve);
              });
              await route.abort('failed').catch(() => undefined);
              return;
            case 'unknown':
              await refuse(route, 409, 'STORAGE_COMMIT_OUTCOME_UNKNOWN');
              return;
            case 'unavailable':
              await refuse(route, 503, 'STORAGE_UNAVAILABLE');
              return;
            case 'invalid-response':
              await route.fulfill({ status: 200, body: 'not-json' });
              return;
            case 'none':
              await route.fallback();
          }
        });
        try {
          await page.goto(paths[surface]);
          const alert = page.getByRole('alert');
          await expect(alert).toBeVisible();
          await focusAndReconnect(page);
          expect(attempts).toHaveLength(1);
          if (initial !== 'unavailable') {
            await expect(alert).toContainText(messages['audit.read.unknown.title']);
            await expect(alert).toContainText(messages['audit.read.unknown.detail']);
          }
          if (initial === 'unknown') await expect(alert).toContainText(requestId);
          if (initial === 'lost-response') expect(fixtureAcceptedReads).toBe(1);
          await expect(page.getByRole('table')).toBeHidden();
          await expect(page.getByText(messages['audit.empty.title'], { exact: true })).toBeHidden();
          await expect(
            page.getByText(messages['resource.activity.empty.title'], { exact: true }),
          ).toBeHidden();
          if (initial === 'unknown') {
            await info.attach(`audit-${surface}-uncertain-${locale}`, {
              body: await page.screenshot({ animations: 'disabled' }),
              contentType: 'image/png',
            });
          }
          fault = 'none';
          held.splice(0).forEach((release) => {
            release();
          });
          const retry = page.getByRole('button', {
            name: messages['audit.read.again'],
            exact: true,
          });
          await retry.focus();
          await page.keyboard.press('Enter');
          await expect(
            page.getByRole('row').filter({ hasText: messages['audit.action.account.updated'] }),
          ).toHaveCount(1);
          expect(attempts).toHaveLength(2);
          expect(attempts.every((query) => !query.has('action'))).toBe(true);
          if (surface === 'activity') expect(attempts[1]?.get('target_id')).toBe(memberUser.id);
          await expect(alert).toBeHidden();
          const beforeReload = attempts.length;
          await page.reload();
          await expect(page.getByRole('table')).toBeVisible();
          expect(attempts).toHaveLength(beforeReload + 1);
          expect(control.auditQueries).toHaveLength(2);
        } finally {
          held.splice(0).forEach((release) => {
            release();
          });
        }
      });
    }

    test(`${surface} preserves two pages and retries only the failed cursor in ${locale}`, async ({
      page,
    }, info) => {
      const control = await configure(page, locale);
      control.auditEvents = history(45);
      await page.goto(paths[surface]);
      const rows = page.getByRole('row');
      const more = page.getByRole('button', { name: messages['audit.loadMore'], exact: true });
      await expect(rows).toHaveCount(21);
      await more.click();
      await expect(rows).toHaveCount(41);
      control.auditProblem = { status: 409, code: 'STORAGE_COMMIT_OUTCOME_UNKNOWN' };
      await more.click();
      await expect(page.getByRole('alert')).toContainText(messages['audit.read.unknown.title']);
      await expect(rows).toHaveCount(41);
      await expect(page.getByText(messages['audit.read.partial'], { exact: true })).toBeVisible();
      await expect(more).toBeHidden();
      await page.getByRole('alert').scrollIntoViewIfNeeded();
      await info.attach(`audit-${surface}-partial-${locale}`, {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });
      const before = control.auditQueries.length;
      control.auditProblem = null;
      let release: () => void = () => undefined;
      const retryReply = new Promise<void>((resolve) => {
        release = resolve;
      });
      await page.route(endpoint, async (route) => {
        await retryReply;
        await route.fallback();
      });
      try {
        await page.getByRole('button', { name: messages['audit.read.again'], exact: true }).click();
        await expect(
          page.getByRole('button', { name: messages['audit.read.pending'], exact: true }),
        ).toBeDisabled();
        await expect(rows).toHaveCount(41);
      } finally {
        release();
      }
      await expect(rows).toHaveCount(46);
      expect(control.auditQueries).toHaveLength(before + 1);
      expect(control.auditQueries.at(-1)?.get('cursor')).toBe('40');
    });

    test(`${surface} restarts an invalid cursor once and retains history until success in ${locale}`, async ({
      page,
    }) => {
      const control = await configure(page, locale);
      control.auditEvents = history(45);
      await page.goto(paths[surface]);
      const rows = page.getByRole('row');
      const more = page.getByRole('button', { name: messages['audit.loadMore'], exact: true });
      await expect(rows).toHaveCount(21);
      await more.click();
      await expect(rows).toHaveCount(41);
      control.auditProblem = { status: 400, code: 'INVALID_CURSOR' };
      await more.click();
      await expect(
        page.getByRole('button', { name: messages['audit.read.restart'], exact: true }),
      ).toBeVisible();
      const before = control.auditQueries.length;
      control.auditProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await page.getByRole('button', { name: messages['audit.read.restart'], exact: true }).click();
      await expect(page.getByRole('alert')).toContainText(
        messages['problem.STORAGE_UNAVAILABLE.title'],
      );
      expect(control.auditQueries).toHaveLength(before + 1);
      expect(control.auditQueries.at(-1)?.has('cursor')).toBe(false);
      await expect(rows).toHaveCount(41);
      control.auditProblem = null;
      await page.getByRole('button', { name: messages['audit.read.again'], exact: true }).click();
      await expect(rows).toHaveCount(21);
      expect(control.auditQueries).toHaveLength(before + 2);
      expect(control.auditQueries.at(-1)?.has('cursor')).toBe(false);
    });

    test(`${surface} removes cached events after a current permission refusal in ${locale}`, async ({
      page,
    }) => {
      const control = await configure(page, locale);
      control.auditEvents = history(25);
      let revoking = false;
      let reads = 0;
      let release: () => void = () => undefined;
      const permissionReply = new Promise<void>((resolve) => {
        release = resolve;
      });
      await page.route(endpoint, async (route) => {
        reads += 1;
        if (!revoking) {
          await route.fallback();
          return;
        }
        await permissionReply;
        await refuse(route, 403, 'ACCESS_DENIED');
      });
      await page.goto(paths[surface]);
      await expect(page.getByRole('row')).toHaveCount(21);
      const revisit = async () => {
        if (surface === 'trail') {
          await page
            .getByLabel(messages['audit.filter.category'], { exact: true })
            .selectOption('account');
          await expect(page).toHaveURL(/category=account/);
        } else {
          await page
            .getByRole('tab', { name: messages['resource.tab.actions'], exact: true })
            .click();
          await expect(page).toHaveURL(/tab=actions/);
        }
      };
      await revisit();
      await expect(page.getByRole('row')).toHaveCount(21);
      if (surface === 'trail') {
        await page.getByLabel(messages['audit.filter.category'], { exact: true }).selectOption('');
        await expect(page).toHaveURL(/\/audit$/);
      } else {
        await page
          .getByRole('tab', { name: messages['resource.tab.activity'], exact: true })
          .click();
        await expect(page).toHaveURL(/tab=activity/);
      }
      await expect(page.getByRole('row')).toHaveCount(21);
      revoking = true;
      await page.getByRole('button', { name: messages['audit.loadMore'], exact: true }).click();
      if (surface === 'trail') {
        await page.getByRole('row').nth(1).getByRole('button').click();
        await expect(page.getByRole('dialog')).toBeVisible();
      }
      release();
      await expect(page.getByRole('alert')).toContainText(messages['problem.ACCESS_DENIED.title']);
      await expect(page.getByRole('table')).toBeHidden();
      await expect(page.getByRole('dialog')).toBeHidden();
      await expect(
        page.getByRole('button', { name: messages['audit.read.again'], exact: true }),
      ).toBeHidden();
      const before = reads;
      await focusAndReconnect(page);
      expect(reads).toBe(before);
      await revisit();
      await expect.poll(() => reads).toBe(before + 1);
      await expect(page.getByRole('alert')).toContainText(messages['problem.ACCESS_DENIED.title']);
      await expect(page.getByRole('table')).toBeHidden();
    });
  }
}

for (const path of Object.values(paths)) {
  test(`does not expose an audit consumer to a Portal session at ${path}`, async ({ page }) => {
    const control = await configure(page, 'en');
    control.auditEvents = history(25);
    await page.goto(path);
    await expect(page.getByRole('row')).toHaveCount(21);
    control.audience = 'portal';
    await page.getByRole('button', { name: english['audit.loadMore'], exact: true }).click();
    await expect(page.getByRole('alert')).toContainText(
      english['problem.RESOURCE_NOT_FOUND.title'],
    );
    await expect(
      page.getByText(english['audit.action.account.updated'], { exact: true }),
    ).toHaveCount(0);
    expect(control.auditQueries).toHaveLength(2);
    await expect(
      page.getByRole('button', { name: english['audit.read.again'], exact: true }),
    ).toBeHidden();
  });
}

test('does not queue an offline audit retry for the next reconnect', async ({ page }) => {
  await configure(page, 'en');
  let attempts = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/v1/admin/audit-events') attempts += 1;
  });
  await page.route(endpoint, async (route) => {
    await route.abort('connectionrefused');
  });
  await page.goto('/audit');
  const retry = page.getByRole('button', { name: english['audit.read.again'], exact: true });
  await expect(retry).toBeVisible();
  await page.context().setOffline(true);
  await retry.click();
  await expect.poll(() => attempts).toBe(2);
  await expect(retry).toBeEnabled();
  await page.context().setOffline(false);
  await focusAndReconnect(page);
  expect(attempts).toBe(2);
});
