// Reads the retained model fixture in the owning installation over the normal Admin entrance.
import { readFileSync } from 'node:fs';
import type { ModelDetail } from '@ownsolid/api';
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test } from '@playwright/test';

const fixtureFile = process.env.OWNSOLID_LIVE_MODEL_LINEAGE_FILE ?? '';
test.skip(origin === '' || fixtureFile === '', 'needs the owning installation and model fixture');
test.use(liveOptions);

test('reads retained model lineage and recovers from an interrupted read', async ({
  page,
}, testInfo) => {
  const fixture = JSON.parse(readFileSync(fixtureFile, 'utf8')) as {
    model_id: string;
    dataset_lineage: NonNullable<ModelDetail['dataset_lineage']>;
  };
  const path = `/library/${fixture.model_id}`;
  const detail = `/api/v1/sites/default/cells/default/admin/models/${fixture.model_id}`;
  const outside: string[] = [];
  const allowed = new URL(origin).origin;
  page.on('request', (request) => {
    const address = new URL(request.url());
    if (address.origin !== allowed && address.protocol !== 'blob:') outside.push(address.origin);
  });
  await page.goto(path);
  const unsigned = await page.evaluate(async (target) => {
    const response = await fetch(target);
    const problem: unknown = await response.json();
    return { status: response.status, problem };
  }, detail);
  expect(unsigned).toMatchObject({ status: 401, problem: { code: 'AUTH_REQUIRED' } });
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('tab', { name: 'Versions and lineage' }).click();
  const panel = page.getByRole('tabpanel', { name: 'Versions and lineage' });
  for (const [title, version] of [
    ['Training dataset version', fixture.dataset_lineage.training],
    ['Preparation holdout version', fixture.dataset_lineage.holdout],
  ] as const) {
    const group = panel.getByRole('region', { name: title });
    await expect(group.locator('dd')).toHaveText([
      version.dataset_id,
      version.dataset_version_id,
      version.manifest_sha256,
    ]);
  }
  const base = panel.getByRole('link', { name: 'The model it was made from' });
  await expect(base).toBeVisible();
  await expectAccessible(page);
  await testInfo.attach('retained-model-lineage', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await base.click();
  await page.getByRole('tab', { name: 'Versions and lineage' }).click();
  await expect(panel.getByRole('heading', { name: 'No dataset lineage available' })).toBeVisible();
  await expect(panel.getByRole('region', { name: 'Training dataset version' })).toHaveCount(0);
  await testInfo.attach('model-without-dataset-lineage', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  // Drop the browser's read, then let Retry reach the same authorized HTTPS service.
  const interruptedRead = `**${detail}`;
  await page.route(interruptedRead, (route) => route.abort('connectionfailed'));
  await page.goto(`${path}?tab=lineage`);
  const error = page.getByRole('alert');
  await expect(error).toContainText(english['problem.NETWORK_UNAVAILABLE.title']);
  await page.unroute(interruptedRead);
  const recovered = page.waitForResponse((response) => response.url() === `${allowed}${detail}`);
  await error.getByRole('button', { name: 'Try again' }).click();
  expect((await recovered).ok()).toBe(true);
  await expect(panel).toContainText(fixture.dataset_lineage.training.dataset_version_id);
  await expect(panel).toContainText(fixture.dataset_lineage.holdout.dataset_version_id);
  expect(outside).toEqual([]);
});
