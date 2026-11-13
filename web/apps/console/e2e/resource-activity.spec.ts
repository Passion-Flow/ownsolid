import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  activeConnector,
  expectAccessible,
  licenseEvent,
  memberUser,
  mockControlPlane,
  node,
  readyModel,
  runningDeployment,
  ssoOn,
} from '@ownsolid/shell/testing';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { boundedActivity } from './resource-activity-fixture.ts';

const path = `/users/${memberUser.id}`;
const recent = (count = 1) =>
  Array.from({ length: count }, (_, index) => ({
    ...licenseEvent,
    id: `6f1c7a1e-0000-4000-8000-${String(index).padStart(12, '0')}`,
    occurred_at: new Date(Date.now() - (index + 1) * 60_000).toISOString(),
    action: 'account.updated',
    target_type: 'user',
    target_id: memberUser.id,
  }));

async function configure(page: Page, locale: 'en' | 'zh-CN') {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.preferences.locale = locale;
  control.users = [
    { ...memberUser, created_at: new Date(Date.now() - 400 * 86_400_000).toISOString() },
  ];
  await page.setViewportSize(
    locale === 'en' ? { width: 1440, height: 900 } : { width: 390, height: 844 },
  );
  return control;
}

async function screenshot(page: Page, info: TestInfo, state: string) {
  if (state.startsWith('partial-')) await page.getByRole('alert').scrollIntoViewIfNeeded();
  await info.attach(`resource-history-${state}`, {
    body: await page.screenshot({ animations: 'disabled', fullPage: true }),
    contentType: 'image/png',
  });
  if (state.startsWith('filtered-empty-')) {
    const messages = state.endsWith('zh-CN') ? chinese : english;
    await page.getByRole('button', { name: messages['resource.activity.clearDates'], exact: true }).scrollIntoViewIfNeeded();
    await info.attach(`resource-history-${state}-remedy`, {
      body: await page.screenshot({ animations: 'disabled', fullPage: true }),
      contentType: 'image/png',
    });
  }
}

for (const [locale, messages] of [
  ['en', english],
  ['zh-CN', chinese],
] as const) {
  test.describe(`bounded resource history in ${locale}`, () => {
    test.use({ timezoneId: 'UTC' });

    for (const subject of [
      { name: 'node', id: node.id, path: `/nodes/${node.id}` },
      { name: 'model', id: readyModel.id, path: `/library/${readyModel.id}` },
      { name: 'deployment', id: runningDeployment.id, path: `/services/${runningDeployment.id}` },
      { name: 'connector', id: activeConnector.id, path: `/sso/${activeConnector.id}` },
    ]) {
      test(`reads recent activity on an old ${subject.name} under a one-day policy`, async ({
        page,
      }, info) => {
        const control = await configure(page, locale);
        const created_at = new Date(Date.now() - 400 * 86_400_000).toISOString();
        control.nodes = [{ ...node, created_at }];
        control.models = [{ ...readyModel, created_at }];
        control.deployments = [{ ...runningDeployment, created_at }];
        control.ssoConfiguration = { ...ssoOn };
        control.ssoConnectorList = [{ ...activeConnector, created_at }];
        const fixture = await boundedActivity(
          page,
          [
            {
              ...licenseEvent,
              occurred_at: new Date(Date.now() - 60_000).toISOString(),
              target_id: subject.id,
            },
          ],
          1,
        );
        await page.goto(`${subject.path}?tab=activity`);
        await expect(page.getByRole('tabpanel').getByRole('row')).toHaveCount(2);
        expect(fixture.queries).toHaveLength(1);
        expect(fixture.queries[0]?.get('target_id')).toBe(subject.id);
        expect(
          Date.parse(fixture.queries[0]?.get('to') ?? '') -
            Date.parse(fixture.queries[0]?.get('from') ?? ''),
        ).toBe(86_400_000);
        await expectAccessible(page);
        await screenshot(page, info, `${subject.name}-recent-${locale}`);
      });
    }

    for (const maximumDays of [1, 31]) {
      test(`reads current activity for a 400-day-old resource with a ${maximumDays}-day policy`, async ({
        page,
      }, info) => {
        await configure(page, locale);
        const fixture = await boundedActivity(page, recent(), maximumDays);
        await page.goto(`${path}?tab=activity`);
        await expect(page.getByRole('row')).toHaveCount(2);
        expect(fixture.queries).toHaveLength(1);
        const query = fixture.queries[0];
        expect(Date.parse(query?.get('to') ?? '') - Date.parse(query?.get('from') ?? '')).toBe(
          86_400_000,
        );
        expect(query?.get('target_id')).toBe(memberUser.id);
        const snapshot = page.getByRole('tabpanel').getByRole('status');
        const displayed = await snapshot.textContent();
        await page
          .getByRole('tab', { name: messages['resource.tab.overview'], exact: true })
          .click();
        await page
          .getByRole('tab', { name: messages['resource.tab.activity'], exact: true })
          .click();
        await expect(snapshot).toHaveText(displayed ?? '');
        expect(fixture.queries).toHaveLength(1);
        await page
          .getByLabel(messages['resource.activity.period'], { exact: true })
          .selectOption('custom');
        await page
          .getByLabel(messages['audit.window.from'], { exact: true })
          .fill('2026-01-01T00:00');
        expect(fixture.queries).toHaveLength(1);
        await page.getByRole('button', { name: messages['action.cancel'], exact: true }).click();
        await expect(
          page.getByLabel(messages['resource.activity.period'], { exact: true }),
        ).toHaveValue('recent');
        expect(fixture.queries).toHaveLength(1);
        if (maximumDays === 1) {
          await expectAccessible(page);
          await screenshot(page, info, `recent-${locale}`);
        }
      });
    }

    test('corrects a refused period, preserves its URL across tabs and reload, and distinguishes empty states', async ({
      page,
    }, info) => {
      await configure(page, locale);
      const fixture = await boundedActivity(page, [
        { ...licenseEvent, target_id: memberUser.id, occurred_at: '2026-01-01T12:00:00Z' },
      ]);
      await page.goto(`${path}?tab=activity`);
      await expect(
        page.getByText(messages['resource.activity.empty.title'], { exact: true }),
      ).toBeVisible();
      await screenshot(page, info, `recent-empty-${locale}`);
      await page
        .getByRole('button', { name: messages['resource.activity.chooseDates'], exact: true })
        .click();
      const from = page.getByLabel(messages['audit.window.from'], { exact: true });
      const to = page.getByLabel(messages['audit.window.to'], { exact: true });
      await expect(from).toBeFocused();
      await from.fill('2026-01-01T00:00');
      await to.fill('2026-03-01T00:00');
      const read = page.getByRole('button', {
        name: messages['resource.activity.read'],
        exact: true,
      });
      await read.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('alert')).toContainText(messages['resource.activity.refused']);
      await expect(from).toHaveValue('2026-01-01T00:00');
      await expect(to).toHaveValue('2026-03-01T00:00');
      expect(fixture.queries).toHaveLength(2);
      await screenshot(page, info, `range-refused-${locale}`);
      await to.fill('2026-01-02T00:00');
      await read.click();
      await expect(page.getByRole('row')).toHaveCount(2);
      expect(fixture.queries).toHaveLength(3);
      await screenshot(page, info, `historical-${locale}`);
      const params = new URL(page.url()).searchParams;
      expect(params.get('activity_from')).toBe('2026-01-01T00:00:00.000Z');
      await page.getByRole('tab', { name: messages['resource.tab.overview'], exact: true }).click();
      expect(new URL(page.url()).searchParams.get('activity_from')).toBe(
        params.get('activity_from'),
      );
      await page.getByRole('tab', { name: messages['resource.tab.activity'], exact: true }).click();
      await expect(page.getByRole('row')).toHaveCount(2);
      expect(fixture.queries).toHaveLength(3);
      await page.reload();
      await expect(page.getByRole('row')).toHaveCount(2);
      expect(fixture.queries).toHaveLength(4);
      await from.fill('2026-01-03T00:00');
      await to.fill('2026-01-04T00:00');
      await read.click();
      await expect(
        page.getByText(messages['resource.activity.filtered.title'], { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText(messages['resource.activity.empty.title'], { exact: true }),
      ).toBeHidden();
      await expectAccessible(page);
      await screenshot(page, info, `filtered-empty-${locale}`);
      await page
        .getByRole('button', { name: messages['resource.activity.clearDates'], exact: true })
        .click();
      await expect(
        page.getByText(messages['resource.activity.empty.title'], { exact: true }),
      ).toBeVisible();
      expect(new URL(page.url()).searchParams.has('activity_from')).toBe(false);
      expect(fixture.queries).toHaveLength(6);
    });

    test('keeps exact cursor bounds and replaces a failed recent snapshot only after explicit recovery succeeds', async ({
      page,
    }, info) => {
      await configure(page, locale);
      await page.clock.install({ time: new Date() });
      const fixture = await boundedActivity(page, recent(45));
      await page.goto(`${path}?tab=activity`);
      await expect(page.getByRole('row')).toHaveCount(21);
      const snapshot = page.getByRole('tabpanel').getByRole('status');
      const previous = await snapshot.textContent();
      const more = page.getByRole('button', { name: messages['audit.loadMore'], exact: true });
      await more.click();
      await expect(page.getByRole('row')).toHaveCount(41);
      for (const bound of ['from', 'to'])
        expect(fixture.queries[1]?.get(bound)).toBe(fixture.queries[0]?.get(bound));
      fixture.failure = { status: 400, code: 'INVALID_CURSOR' };
      await more.click();
      await expect(
        page.getByRole('button', { name: messages['audit.read.restart'], exact: true }),
      ).toBeVisible();
      fixture.failure = { status: 409, code: 'STORAGE_COMMIT_OUTCOME_UNKNOWN' };
      await page.getByRole('button', { name: messages['audit.read.restart'], exact: true }).click();
      await expect(page.getByRole('alert')).toContainText(messages['audit.read.unknown.title']);
      await expect(page.getByRole('row')).toHaveCount(41);
      await expect(snapshot).toHaveText(previous ?? '');
      expect(fixture.queries).toHaveLength(4);
      await screenshot(page, info, `partial-${locale}`);
      fixture.failure = null;
      await page.clock.fastForward(3_600_000);
      expect(fixture.queries).toHaveLength(4);
      await page.getByRole('button', { name: messages['audit.read.again'], exact: true }).click();
      await expect(page.getByRole('row')).toHaveCount(21);
      expect(fixture.queries).toHaveLength(5);
      expect(fixture.queries[4]?.has('cursor')).toBe(false);
      expect(fixture.queries[4]?.get('to')).not.toBe(fixture.queries[0]?.get('to'));
      await more.click();
      await expect(page.getByRole('row')).toHaveCount(41);
      for (const bound of ['from', 'to'])
        expect(fixture.queries[5]?.get(bound)).toBe(fixture.queries[4]?.get(bound));
      await expectAccessible(page);
    });

    test('cancels a pending page when leaving the tab and restores its earlier snapshot without replay', async ({
      page,
    }) => {
      await configure(page, locale);
      const fixture = await boundedActivity(page, recent(25));
      await page.goto(`${path}?tab=activity`);
      await expect(page.getByRole('row')).toHaveCount(21);
      let release: () => void = () => undefined;
      const held = new Promise<void>((resolve) => {
        release = resolve;
      });
      fixture.hold = () => held;
      try {
        await page.getByRole('button', { name: messages['audit.loadMore'], exact: true }).click();
        await expect.poll(() => fixture.queries.length).toBe(2);
        const aborted = page.waitForEvent('requestfailed', {
          predicate: (request) => new URL(request.url()).pathname === '/api/v1/admin/audit-events',
        });
        await page
          .getByRole('tab', { name: messages['resource.tab.overview'], exact: true })
          .click();
        await aborted;
      } finally {
        fixture.hold = null;
        release();
      }
      await page.getByRole('tab', { name: messages['resource.tab.activity'], exact: true }).click();
      await expect(page.getByRole('row')).toHaveCount(21);
      expect(fixture.queries).toHaveLength(2);
      await page.getByRole('button', { name: messages['audit.loadMore'], exact: true }).click();
      await expect(page.getByRole('row')).toHaveCount(26);
      expect(fixture.queries).toHaveLength(3);
      for (const bound of ['from', 'to'])
        expect(fixture.queries[2]?.get(bound)).toBe(fixture.queries[0]?.get(bound));
    });

    test('shows loading and a retained error, then hides all cached rows when access is refused', async ({
      page,
    }, info) => {
      await configure(page, locale);
      const fixture = await boundedActivity(page, recent());
      let release: () => void = () => undefined;
      const held = new Promise<void>((resolve) => {
        release = resolve;
      });
      fixture.hold = () => held;
      try {
        await page.goto(`${path}?tab=activity`);
        await expect(
          page.getByRole('button', { name: messages['resource.activity.reading'], exact: true }),
        ).toBeDisabled();
        await expect(page.getByRole('tabpanel').locator('[aria-busy="true"]')).toBeVisible();
        await screenshot(page, info, `loading-${locale}`);
        fixture.failure = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      } finally {
        release();
        fixture.hold = null;
      }
      await expect(page.getByRole('alert')).toContainText(
        messages['problem.STORAGE_UNAVAILABLE.title'],
      );
      await expect(
        page.getByText(messages['resource.activity.empty.title'], { exact: true }),
      ).toBeHidden();
      await screenshot(page, info, `error-${locale}`);
      fixture.failure = null;
      await page.getByRole('button', { name: messages['audit.read.again'], exact: true }).click();
      await expect(page.getByRole('row')).toHaveCount(2);
      fixture.failure = { status: 403, code: 'ACCESS_DENIED' };
      await page
        .getByRole('button', { name: messages['resource.activity.read'], exact: true })
        .click();
      await expect(page.getByRole('table')).toBeHidden();
      await expect(
        page.getByText(messages['resource.activity.denied'], { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByLabel(messages['resource.activity.period'], { exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByRole('button', { name: messages['resource.activity.read'], exact: true }),
      ).toHaveAttribute('aria-disabled', 'true');
      expect(fixture.queries).toHaveLength(3);
      await expectAccessible(page);
      await screenshot(page, info, `denied-${locale}`);
    });
  });
}

test.describe('native activity dates in a daylight-saving zone', () => {
  test.use({ timezoneId: 'America/New_York' });
  test('rejects reversed and nonexistent dates without an audit read', async ({ page }, info) => {
    await configure(page, 'en');
    const fixture = await boundedActivity(page, []);
    await page.goto(`${path}?tab=activity`);
    await expect(
      page.getByText(english['resource.activity.empty.title'], { exact: true }),
    ).toBeVisible();
    await page
      .getByRole('button', { name: english['resource.activity.chooseDates'], exact: true })
      .click();
    const from = page.getByLabel(english['audit.window.from'], { exact: true });
    const to = page.getByLabel(english['audit.window.to'], { exact: true });
    await from.fill('2026-03-08T02:30');
    await to.fill('2026-03-08T04:00');
    await page
      .getByRole('button', { name: english['resource.activity.read'], exact: true })
      .click();
    await expect(from).toHaveAttribute('aria-invalid', 'true');
    expect(fixture.queries).toHaveLength(1);
    await from.fill('2026-03-09T04:00');
    await page
      .getByRole('button', { name: english['resource.activity.read'], exact: true })
      .click();
    await expect(page.getByText(english['audit.window.reversed'], { exact: true })).toBeVisible();
    expect(fixture.queries).toHaveLength(1);
    await screenshot(page, info, 'invalid-local-dates-en');
  });
});
