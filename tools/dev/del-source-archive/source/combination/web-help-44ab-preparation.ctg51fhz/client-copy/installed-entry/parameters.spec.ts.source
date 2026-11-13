import { expect, test } from '@playwright/test';
import { english, chinese, adminPassword, email, origin, expectFocusVisible, expectContractAudience } from './runtime-source.ts';

const audience = process.env.OWNSOLID_LIVE_AUDIENCE;
test.skip(origin === '' || email === '', 'needs the shared installation and its protected credential file');

test('the installed reference preserves parameter meaning and bilingual recovery', async ({ page }, info) => {
  test.setTimeout(120_000);
  expect(['admin', 'portal']).toContain(audience);
  const outside: string[] = [];
  const refused: string[] = [];
  const plannedRefusals = new Set<string>();
  await page.route(
    url => (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== origin,
    async route => {
      outside.push(new URL(route.request().url()).origin);
      await route.abort();
    },
  );
  page.on('request', request => {
    if (new URL(request.url()).origin !== origin) outside.push(new URL(request.url()).origin);
  });
  page.on('response', response => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith('/api/') && path !== '/api/v1/me' && !plannedRefusals.has(path) && response.status() >= 400) {
      refused.push(`${response.status()} ${path}`);
    }
  });
  await page.goto('/account');
  await page.getByLabel('Email', { exact: true }).fill(email);
  await page.getByLabel('Password', { exact: true }).fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  await page.emulateMedia({ reducedMotion: 'reduce' });

  const identity = await page.evaluate(async () => {
    const response = await fetch('/api/v1/me', { cache: 'no-store' });
    const body = await response.json() as { data: { audience: string; workspace: { id: string } | null } };
    return { status: response.status, audience: body.data.audience, workspace: body.data.workspace?.id };
  });
  expect(identity.status).toBe(200);
  expect(identity.audience).toBe(audience);
  expect(identity.workspace, 'The prepared installed account needs its own default workspace').toMatch(/^[0-9a-f-]{36}$/i);
  const prefix = audience === 'admin' ? '/api/v1/admin/workspaces/' : '/api/v1/workspaces/';
  const other = audience === 'admin' ? '/api/v1/workspaces/' : '/api/v1/admin/workspaces/';
  const ownPath = prefix + identity.workspace;
  const refusedPath = other + identity.workspace;
  const ownRead = await page.evaluate(async path => {
    const response = await fetch(path, { cache: 'no-store' });
    const body = await response.json() as { data: { id: string } };
    return { status: response.status, id: body.data.id };
  }, ownPath);
  expect(ownRead.status).toBe(200);
  expect(ownRead.id).toBe(identity.workspace);
  plannedRefusals.add(refusedPath);
  const entryRefusal = await page.evaluate(async path => {
    const response = await fetch(path, { cache: 'no-store' });
    return {
      status: response.status,
      type: response.headers.get('content-type'),
      cache: response.headers.get('cache-control'),
      requestId: response.headers.get('x-request-id'),
      problem: await response.json() as { code: string; request_id: string },
    };
  }, refusedPath);
  expect(entryRefusal.status).toBe(404);
  expect(entryRefusal.type).toBe('application/problem+json');
  expect(entryRefusal.cache).toBe('no-store');
  expect(entryRefusal.problem.code).toBe('RESOURCE_NOT_FOUND');
  expect(entryRefusal.problem.request_id).toBe(entryRefusal.requestId);
  expect(entryRefusal.problem.request_id).toMatch(/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i);
  await info.attach(`${audience}-installed-entry-proof`, {
    body: JSON.stringify({ audience, ownPath, refusedPath, ...entryRefusal }),
    contentType: 'application/json',
  });

  let changed = false;
  async function language(from: typeof english, locale: 'en' | 'zh-CN') {
    await page.getByRole('button', { name: from['user.menu'], exact: true }).click();
    const saved = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/v1/preferences' && response.request().method() === 'PATCH',
    );
    await page.getByRole('menuitem', { name: english[`locale.${locale}`], exact: true }).click();
    expect((await saved).status()).toBe(200);
    changed = locale === 'zh-CN';
    await expect(page.locator('html')).toHaveAttribute('lang', locale);
  }

  try {
    for (const [locale, messages] of [['en', english], ['zh-CN', chinese]] as const) {
      if (locale === 'zh-CN') {
        await language(english, locale);
        await page.setViewportSize({ width: 390, height: 844 });
      }
      const excluded = audience === 'admin' ? 'getPortalWorkspace' : 'getAdminWorkspace';
      await page.goto(`/help/api?q=${excluded}`);
      await expect(page.getByRole('heading', { name: messages['help.api.empty.title'], exact: true })).toBeVisible();
      const clear = page.getByRole('button', { name: messages['help.empty.clear'], exact: true });
      await expect(clear).toBeVisible();
      await info.attach(`${audience}-installed-filtered-empty-${locale}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      await clear.click();
      await expect(page).toHaveURL(/\/help\/api$/);
      await page.goto('/help/api?q=changePassword#identity.changePassword');
      await expect(page.locator('html')).toHaveAttribute('lang', locale);
      const password = page.locator('[id="identity.changePassword-detail"]');
      const none = password.getByText(messages['help.api.parameters.none'], { exact: true });
      await expect(none).toBeVisible();
      await expect(password.getByRole('table', { name: messages['help.api.parameters'], exact: true })).toHaveCount(0);
      await expect(password).not.toContainText('help.api.in.undefined');
      const save = page.getByRole('link', { name: messages['help.api.save.label'].replace('{domain}', 'identity'), exact: true });
      await expect(save).toHaveAttribute('download', 'identity.openapi.json');
      const href = await save.getAttribute('href');
      expect(new URL(href ?? '', page.url()).origin).toBe(origin);
      await page.context().setOffline(true);
      try {
        await page.getByLabel(messages['help.api.search']).fill('changePassword');
        await expect(save).toBeVisible();
        await info.attach(`${audience}-installed-download-discovery-offline-${locale}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      } finally { await page.context().setOffline(false); }
      const download = page.waitForEvent('download');
      await save.click();
      await expectContractAudience(await download, audience as 'admin' | 'portal');

      const error = password.getByRole('listitem').filter({ has: page.getByText('PASSWORD_CURRENT_INVALID', { exact: true }) });
      await expect(error.getByText('403', { exact: true })).toBeVisible();
      await expect(error).toContainText(messages['problem.PASSWORD_CURRENT_INVALID.title']);
      await expect(error).toContainText(messages['problem.PASSWORD_CURRENT_INVALID.detail']);
      await none.scrollIntoViewIfNeeded();
      await info.attach(`${audience}-installed-password-parameters-${locale}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });

      const operation = audience === 'admin' ? 'getAdminWorkspace' : 'getPortalWorkspace';
      await page.goto(`/help/api?q=${operation}#workspaces.${operation}`);
      await expect(page.getByRole('button', { name: new RegExp(` ${operation}$`) })).toHaveAttribute('aria-expanded', 'true');
      const detail = page.locator(`[id="workspaces.${operation}-detail"]`);
      const parameter = detail.getByRole('table', { name: messages['help.api.parameters'], exact: true }).getByRole('row').filter({ has: page.getByText('workspace_id', { exact: false }) });
      await expect(parameter).toHaveCount(1);
      await expect(parameter.getByRole('cell', { name: messages['help.api.in.path'], exact: true })).toBeVisible();
      await expect(parameter).toContainText(messages['help.api.required']);
      await expect(parameter).toContainText('uuid');
      await parameter.scrollIntoViewIfNeeded();
      await info.attach(`${audience}-installed-workspace-parameter-${locale}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      await expectFocusVisible(page);

      if (audience === 'admin') {
        await page.goto('/help/api?q=adminCutoverAlias#distribution.adminCutoverAlias');
        const cutover = page.locator('[id="distribution.adminCutoverAlias-detail"]');
        for (const code of ['ALIAS_PAUSE_REQUIRED', 'ALIAS_ALREADY_PAUSED', 'ALIAS_NOT_PAUSED', 'ALIAS_REQUESTS_UNDRAINED', 'ALIAS_ROLLBACK_TARGET_INVALID'] as const) {
          const row = cutover.getByRole('listitem').filter({ has: page.getByText(code, { exact: true }) });
          await expect(row.getByText('409', { exact: true })).toBeVisible();
          await expect(row).toContainText(messages[`problem.${code}.title`]);
          await expect(row).toContainText(messages[`problem.${code}.detail`]);
        }
        await cutover.getByText('ALIAS_PAUSE_REQUIRED', { exact: true }).scrollIntoViewIfNeeded();
        await info.attach(`admin-installed-apply-cutover-errors-${locale}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      }
    }
  } finally {
    if (changed) {
      const current = await page.locator('html').getAttribute('lang');
      await language(current === 'zh-CN' ? chinese : english, 'en');
    }
  }
  expect(outside).toEqual([]);
  expect(refused).toEqual([]);
});
