import { createHash } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { expect, test, type Page, type BrowserContext, type Response } from '@playwright/test';
import { english, chinese, expectFocusVisible } from './runtime-source.ts';
import { revision } from './bound-source.ts';
import { password, readFixture } from './fixture.ts';

const file = process.env.OWNSOLID_GW120_FIXTURE_FILE ?? '';
test.skip(file === '', 'Needs the newly frozen installation and an owned GW-120 fixture');
test.use({ trace: 'off', screenshot: 'off', video: 'off' });

test('installed Console edits persist into the authorized Portal catalogue in both languages', async ({ browser }, info) => {
  test.setTimeout(180_000);
  const fixture = readFixture(file);
  const outside: string[] = [];
  const records: Record<string, unknown>[] = [];
  const contexts: BrowserContext[] = [];
  const errors: unknown[] = [];
  const base = `/api/v1/sites/${fixture.site}/cells/${fixture.cell}`;
  const adminAlias = `${base}/admin/distribution/workspaces/${fixture.workspace}/aliases/${fixture.alias}`;
  const portalAlias = `${base}/workspaces/${fixture.workspace}/distribution/aliases/${fixture.alias}`;
  const editPath = `${adminAlias}/description`;

  async function pageFor(origin: string, actor: typeof fixture.admin) {
    const context = await browser.newContext({ baseURL: origin, locale: 'en' });
    contexts.push(context);
    const page = await context.newPage();
    await page.route(url => /^https?:$/.test(url.protocol) && url.origin !== origin, async route => {
      outside.push(new URL(route.request().url()).origin);
      await route.abort();
    });
    await page.goto('/account');
    await page.locator('input[type="email"]').fill(actor.email);
    await page.locator('input[type="password"]').fill(password(actor.passwordFile));
    await page.getByRole('button', { name: /^(Sign in|\u767b\u5f55)$/ }).click();
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    await page.emulateMedia({ reducedMotion: 'reduce' });
    return page;
  }

  async function document(page: Page, path: string) {
    return page.evaluate(async url => {
      const response = await fetch(url, { cache: 'no-store' });
      return { status: response.status, requestId: response.headers.get('x-request-id'), cache: response.headers.get('cache-control'), body: await response.json() as Record<string, unknown> };
    }, path);
  }

  async function alias(page: Page, path: string) {
    const result = await document(page, path);
    expect(result.status).toBe(200);
    const value = result.body.data as { id: string; workspace_id: string; name: string; deployment_id: string; deployment_revision: number; gateway_id: string; revision: number; disabled_at: string | null; description: string; description_revision: number };
    expect(value).toMatchObject({ id: fixture.alias, workspace_id: fixture.workspace, name: fixture.aliasName, deployment_id: fixture.deployment, disabled_at: null });
    expect(typeof value.description).toBe('string');
    expect(value.description_revision).toBeGreaterThan(0);
    return value;
  }

  async function language(page: Page, locale: 'en' | 'zh-CN') {
    const from = (await page.locator('html').getAttribute('lang')) === 'zh-CN' ? chinese : english;
    if (await page.locator('html').getAttribute('lang') === locale) return;
    await page.getByRole('button', { name: from['user.menu'], exact: true }).click();
    const saved = page.waitForResponse(reply => new URL(reply.url()).pathname === '/api/v1/preferences' && reply.request().method() === 'PATCH');
    await page.getByRole('menuitem', { name: english[`locale.${locale}`], exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect(page.locator('html')).toHaveAttribute('lang', locale);
  }

  async function editor(page: Page, messages: typeof english) {
    await page.goto(`/services/${fixture.deployment}`);
    await page.getByRole('group', { name: fixture.aliasName, exact: true }).getByRole('button', { name: messages['services.gateway.description.edit'], exact: true }).click();
    return page.getByRole('dialog', { name: messages['services.gateway.description.edit'], exact: true });
  }

  async function record(reply: Response) {
    const body = await reply.json() as Record<string, unknown>;
    const requestId = reply.headers()['x-request-id'];
    expect(requestId).toMatch(/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/);
    records.push({ path: new URL(reply.url()).pathname, status: reply.status(), requestId, intent: reply.request().headers()['idempotency-key'], body });
    if (reply.status() >= 400) {
      expect(body.request_id).toBe(requestId);
      expect(reply.headers()['cache-control']).toContain('no-store');
    }
    return body;
  }

  async function write(page: Page, messages: typeof english, text: string, reason: string, expected = 200) {
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel(messages['services.gateway.description.label'], { exact: true }).fill(text);
    await dialog.getByLabel(messages['gateway.reason'], { exact: true }).fill(reason);
    const response = page.waitForResponse(reply => new URL(reply.url()).pathname.endsWith('/description') && reply.request().method() === 'POST');
    await dialog.getByRole('button', { name: messages['services.gateway.description.save'], exact: true }).click();
    const reply = await response;
    expect(reply.status()).toBe(expected);
    const body = await record(reply);
    if (expected === 409) expect(body.code).toBe('VERSION_CONFLICT');
    return body;
  }

  async function catalogue(page: Page, messages: typeof english, expected: string) {
    await page.goto(`/models?open=${fixture.alias}`);
    const drawer = page.getByRole('dialog', { name: fixture.aliasName, exact: true });
    if (expected) await expect(drawer.getByText(expected, { exact: true })).toBeVisible();
    else await expect(drawer.getByText(messages['portalModels.detail.goodFor.empty'], { exact: true })).toBeVisible();
    await expect(drawer.locator('textarea')).toHaveCount(0);
    await expect(drawer.getByRole('button', { name: messages['services.gateway.description.edit'], exact: true })).toHaveCount(0);
    await info.attach(`installed-gw120-portal-${await page.locator('html').getAttribute('lang')}`, { body: await page.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
    await page.keyboard.press('Escape');
  }

  let admin: Page | undefined;
  let member: Page | undefined;
  try {
    admin = await pageFor(fixture.adminOrigin, fixture.admin);
    const second = await pageFor(fixture.adminOrigin, fixture.admin);
    member = await pageFor(fixture.portalOrigin, fixture.member);
    const adminIdentity = await document(admin, '/api/v1/me');
    expect(adminIdentity.status).toBe(200);
    const adminMe = adminIdentity.body.data as { audience: string; user: { id: string; is_platform_admin: boolean } };
    expect(adminMe.audience).toBe('admin');
    expect(adminMe.user.is_platform_admin).toBe(true);
    const identity = await document(member, '/api/v1/me');
    expect(identity.status).toBe(200);
    const me = identity.body.data as { audience: string; workspace: { id: string }; user: { id: string; is_platform_admin: boolean } };
    expect(me.audience).toBe('portal');
    expect(me.user.is_platform_admin).toBe(false);
    expect(me.workspace.id).toBe(fixture.workspace);
    const initial = await alias(admin, adminAlias);
    await alias(member, portalAlias);
    for (const [locale, messages] of [['en', english], ['zh-CN', chinese]] as const) {
      await language(admin, locale);
      await language(member, locale);
      await admin.setViewportSize({ width: locale === 'en' ? 1440 : 390, height: locale === 'en' ? 900 : 844 });
      await member.setViewportSize({ width: locale === 'en' ? 1440 : 390, height: locale === 'en' ? 900 : 844 });
      const draft = `Support requests for ${fixture.fixturePrefix}; verify important answers. ${locale}`;
      const other = `Another administrator's current text. ${locale}`;
      const dialog = await editor(admin, messages);
      await dialog.getByLabel(messages['services.gateway.description.label'], { exact: true }).fill(draft);
      await dialog.getByLabel(messages['gateway.reason'], { exact: true }).fill('Qualify the owned description fixture');
      await admin.mouse.click(4, 4);
      await expect(dialog).toBeVisible();
      await expectFocusVisible(admin);
      await info.attach(`installed-gw120-editor-${locale}`, { body: await admin.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      // One real request is interrupted before dispatch; this is not a lost-commit claim.
      const attempts: { intent: string; bodySha256: string }[] = [];
      let interrupt = true;
      const handler = async (route: import('@playwright/test').Route) => {
        attempts.push({ intent: route.request().headers()['idempotency-key'] ?? '', bodySha256: createHash('sha256').update(route.request().postData() ?? '').digest('hex') });
        if (interrupt) { interrupt = false; await route.abort('connectionreset'); }
        else await route.continue();
      };
      await admin.route(`**${editPath}`, handler);
      await dialog.getByRole('button', { name: messages['services.gateway.description.save'], exact: true }).click();
      await expect(dialog.getByRole('button', { name: messages['action.retry'], exact: true })).toBeVisible();
      await expect(dialog.getByLabel(messages['services.gateway.description.label'], { exact: true })).toHaveValue(draft);
      const retried = admin.waitForResponse(reply => new URL(reply.url()).pathname === editPath && reply.status() === 200);
      await dialog.getByRole('button', { name: messages['action.retry'], exact: true }).click();
      await record(await retried);
      expect(attempts).toHaveLength(2);
      const firstAttempt = attempts[0];
      if (!firstAttempt) throw new Error('The interrupted request was not observed');
      expect(firstAttempt.intent).toMatch(/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/);
      expect(attempts[0]).toEqual(attempts[1]);
      records.push({ networkRetry: attempts, interruptedBeforeDispatch: true });
      await admin.unroute(`**${editPath}`, handler);
      await expect(dialog).toHaveCount(0);
      await catalogue(member, messages, draft);
      // Two independent real sessions see the same description revision.
      await editor(admin, messages);
      await editor(second, messages);
      await write(second, messages, other, 'Qualify the competing administrator');
      await expect(second.getByRole('dialog')).toHaveCount(0);
      await write(admin, messages, draft + ' revised', 'Keep the draft after a conflict', 409);
      await expect(admin.getByLabel(messages['services.gateway.description.label'], { exact: true })).toHaveValue(draft + ' revised');
      await expect(admin.getByRole('button', { name: messages['services.gateway.description.review'], exact: true })).toBeVisible();
      await admin.getByRole('button', { name: messages['services.gateway.description.review'], exact: true }).click();
      await expect(admin.getByRole('dialog').getByText(other, { exact: true })).toBeVisible();
      await info.attach(`installed-gw120-review-${locale}`, { body: await admin.screenshot({ animations: 'disabled' }), contentType: 'image/png' });
      await write(admin, messages, draft + ' revised', 'Keep the draft after a conflict');
      await expect(admin.getByRole('dialog')).toHaveCount(0);
      await catalogue(member, messages, draft + ' revised');
      await editor(admin, messages);
      await write(admin, messages, '', 'Clear the owned description fixture');
      await expect(admin.getByRole('dialog')).toHaveCount(0);
      const undone = admin.waitForResponse(reply => new URL(reply.url()).pathname === editPath && reply.request().method() === 'POST' && reply.status() === 200);
      await admin.getByRole('button', { name: messages['action.undo'], exact: true }).click();
      await record(await undone);
      await expect(admin.getByText(messages['action.undone'], { exact: true })).toBeVisible();
      await catalogue(member, messages, draft + ' revised');
      await editor(admin, messages);
      await write(admin, messages, '', 'Check the explicit empty description');
      await expect(admin.getByRole('dialog')).toHaveCount(0);
      await catalogue(member, messages, '');
    }
    const foreign = await document(member, `${base}/workspaces/${fixture.otherWorkspace}/distribution/aliases/${fixture.alias}`);
    expect(foreign.status).toBe(404);
    expect(foreign.body.request_id).toBe(foreign.requestId);
    expect(foreign.cache).toContain('no-store');
    const final = await alias(admin, adminAlias);
    expect(final).toMatchObject({ revision: initial.revision, gateway_id: initial.gateway_id, deployment_id: initial.deployment_id, deployment_revision: initial.deployment_revision, description: '' });
    expect(final.description_revision).toBeGreaterThan(initial.description_revision);
    expect(outside).toEqual([]);
    const proof = { source: revision, imageDigest: fixture.imageDigest, declaredSchema: fixture.schemaVersion, declaredStableTables: fixture.stableTables, readySha256: fixture.readySha256, alias: fixture.alias, workspace: fixture.workspace, admin: adminMe.user.id, member: me.user.id, initial, final, foreign, records, outside, networkFault: 'aborted before dispatch; retry reused the intent', databaseAndImageBinding: 'DEL preflight and independent SQL are required' };
    await writeFile(info.outputPath('gw120-ui-proof.json'), JSON.stringify(proof, null, 2) + '\n', { mode: 0o600, flag: 'wx' });
  } catch (error: unknown) {
    errors.push(error);
  } finally {
    // Restore the shared actors to English before Help and Scope use them.
    for (const page of [admin, member]) {
      if (page && !page.isClosed()) {
        try {
          await page.goto('/account');
          await language(page, 'en');
        } catch (error: unknown) { errors.push(error); }
      }
    }
    for (const context of contexts) {
      try { await context.close(); } catch (error: unknown) { errors.push(error); }
    }
  }
  if (errors.length) throw new AggregateError(errors, 'Installed description flow or cleanup failed');
});
