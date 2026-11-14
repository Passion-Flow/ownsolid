// Imports the real model into a real installation: a browser sends the whole bundle, is interrupted,
// refuses a different file, continues, and the server verifies every file against its published
// hash. Skipped unless the environment names a bundle; docs/development/environment.md has the recipe.
import type { ModelImport } from '@ownsolid/api';
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page, type Request } from '@playwright/test';

const bundle = process.env.OWNSOLID_LIVE_MODEL_BUNDLE ?? '';
// A file of the same size as the bundle, with different bytes.
const impostor = process.env.OWNSOLID_LIVE_MODEL_IMPOSTOR ?? '';
const modelName = 'Qwen3-0.6B';

test.skip(origin === '' || bundle === '' || impostor === '', 'needs the model bundle');
test.use(liveOptions);
// Retire the resumed version before creating the empty import to discard.
test.describe.configure({ mode: 'serial' });

const signIn = async (page: Page, path: string, heading: string | RegExp) => {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: heading, level: 1 })).toBeVisible();
};

async function prepareUpload(page: Page, reason: string) {
  await signIn(page, '/library/import', 'Import a bundle');
  await expectAccessible(page);
  await page.getByTestId('model-bundle').setInputFiles(bundle);
  await expect(page.getByText(/Allow about .* in the model store during import/)).toBeVisible();
  await page.getByLabel('Reason').fill(reason);
  await page.getByRole('checkbox').check();
}

const chunkPath = /\/model-imports\/[^/]+\/chunks$/;

const chunkOffset = (request: Request): number =>
  Number(request.headers()['x-chunk-offset'] ?? Number.NaN);

async function readImport(page: Page, id: string): Promise<ModelImport> {
  return page.evaluate(async (importId) => {
    const path = `/api/v1/sites/default/cells/default/admin/model-imports/${importId}`;
    const response = await fetch(path, { cache: 'no-store' });
    if (!response.ok) throw new Error(`Import read returned HTTP ${response.status}`);
    return ((await response.json()) as { data: ModelImport }).data;
  }, id);
}

test('resumes at the durable offset after a real chunk interruption', async ({
  page: first,
  context,
}, testInfo) => {
  test.setTimeout(30 * 60_000);
  const allowed = new URL(origin).origin;
  const outside = new Set<string>();
  context.on('request', (request) => {
    const address = new URL(request.url());
    if (address.origin !== allowed && address.protocol !== 'blob:') outside.add(address.origin);
  });
  const failures: { offset: number; error: string }[] = [];
  first.on('requestfailed', (request) => {
    if (chunkPath.test(new URL(request.url()).pathname)) {
      failures.push({
        offset: chunkOffset(request),
        error: request.failure()?.errorText ?? '',
      });
    }
  });
  // Only the first two writes leave the original tab. Status reads and all writes
  // on the reopened tab use the real server without route interception.
  let forwarded = 0;
  await first.route(chunkPath, async (route) => {
    if (forwarded >= 2) {
      await route.abort('connectionreset');
      return;
    }
    forwarded++;
    await route.continue();
  });

  await prepareUpload(first, 'Live run of the Model library');
  const checkpointReply = first.waitForResponse(
    (response) =>
      chunkPath.test(new URL(response.url()).pathname) &&
      response.ok() &&
      chunkOffset(response.request()) > 0,
    { timeout: 120_000 },
  );
  await first.getByRole('button', { name: 'Start import' }).click();
  const checkpointResponse = await checkpointReply;
  const lastStoredOffset = chunkOffset(checkpointResponse.request());
  const { data: stored } = (await checkpointResponse.json()) as { data: ModelImport };
  const checkpoint = stored.uploaded_bytes;
  expect(checkpoint).toBe(lastStoredOffset * 2);
  await expect(
    first.getByText(english['problem.NETWORK_UNAVAILABLE.title'], { exact: true }),
  ).toBeVisible({ timeout: 30_000 });
  await testInfo.attach('upload-interruption-events', {
    body: JSON.stringify({ importId: stored.id, checkpoint, lastStoredOffset, failures }, null, 2),
    contentType: 'application/json',
  });
  expect(failures.length).toBeGreaterThan(0);
  expect(failures).toEqual(
    failures.map(() => ({ offset: checkpoint, error: 'net::ERR_CONNECTION_RESET' })),
  );
  await testInfo.attach('model-upload-interrupted', {
    body: await first.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  const importPath = new URL(first.url()).pathname;
  await first.close({ runBeforeUnload: false });
  const page = await context.newPage();
  await page.goto('/library?tab=imports');
  await page
    .getByRole('row', { name: /Uploading/ })
    .getByRole('link', { name: modelName })
    .click();
  await expect(page).toHaveURL(`${allowed}${importPath}`);
  await expect(page.getByText('This tab is not uploading')).toBeVisible();
  expect((await readImport(page, stored.id)).uploaded_bytes).toBe(checkpoint);
  await expectAccessible(page);

  const refusal = page.waitForResponse(
    (response) => chunkPath.test(new URL(response.url()).pathname) && response.status() === 409,
  );
  await page.getByTestId('model-bundle-resume').setInputFiles(impostor);
  const refused = await refusal;
  const refusalBody = await refused.text();
  await testInfo.attach('model-upload-original-refusal', {
    body: JSON.stringify(
      {
        status: refused.status(),
        offset: chunkOffset(refused.request()),
        response: refusalBody,
      },
      null,
      2,
    ),
    contentType: 'application/json',
  });
  expect(chunkOffset(refused.request())).toBe(lastStoredOffset);
  expect(JSON.parse(refusalBody)).toMatchObject({ code: 'MODEL_CHUNK_CONFLICT' });
  await expect(page.getByText('This is not the file this import began with')).toBeVisible();
  expect((await readImport(page, stored.id)).uploaded_bytes).toBe(checkpoint);
  await testInfo.attach('model-upload-wrong-file', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  const resumedReplies = [lastStoredOffset, checkpoint].map((offset) =>
    page.waitForResponse(
      (response) =>
        chunkPath.test(new URL(response.url()).pathname) &&
        response.ok() &&
        chunkOffset(response.request()) === offset,
      { timeout: 120_000 },
    ),
  );
  const resumedOffsets: number[] = [];
  page.on('request', (request) => {
    if (chunkPath.test(new URL(request.url()).pathname)) resumedOffsets.push(chunkOffset(request));
  });
  await page.getByTestId('model-bundle-resume').setInputFiles(bundle);
  const writes = await Promise.all(resumedReplies);
  const [replayed, continued] = await Promise.all(
    writes.map(
      async (reply) => ((await reply.json()) as { data: ModelImport }).data.uploaded_bytes,
    ),
  );
  expect(resumedOffsets.slice(0, 2)).toEqual([lastStoredOffset, checkpoint]);
  expect(replayed).toBe(checkpoint);
  expect(continued).toBe(checkpoint + lastStoredOffset);
  await testInfo.attach('model-upload-resume-receipts', {
    body: JSON.stringify({ importId: stored.id, checkpoint, replayed, continued }, null, 2),
    contentType: 'application/json',
  });

  const openModel = page.getByRole('link', { name: 'Open the model' });
  await expect(openModel).toHaveAttribute('href', `/library/${stored.id}`, {
    timeout: 20 * 60_000,
  });
  expect(await readImport(page, stored.id)).toMatchObject({
    state: 'ready',
    model_id: stored.id,
    uploaded_bytes: stored.expected_bytes,
    attempts: 1,
  });
  await expectAccessible(page);
  await testInfo.attach('model-upload-resumed-and-published', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await openModel.click();
  await expect(page.getByRole('heading', { name: modelName, level: 1 })).toBeVisible();
  await expect(page.getByText('Apache-2.0')).toBeVisible();
  await expect(page.getByRole('link', { name: /^Import of / })).toHaveAttribute('href', importPath);
  await page.getByRole('tab', { name: 'Files' }).click();
  await expect(page.getByRole('cell', { name: 'model.safetensors' })).toBeVisible();
  await expectAccessible(page);
  expect([...outside]).toEqual([]);
});

test('retires the model', async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page, '/library', 'Model library');
  await page.getByRole('button', { name: `Show ${modelName}` }).click();
  await page.getByRole('link', { name: 'Open the full page' }).click();
  await page.getByRole('button', { name: 'Retire' }).click();
  const dialog = page.getByRole('dialog', { name: `Retire ${modelName}` });
  await dialog.getByLabel(/^Type /).fill(modelName);
  await dialog.getByLabel('Reason').fill('End of the live run');
  await dialog.getByRole('button', { name: 'Retire' }).click();
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
});

test('discards a real import before accepting its first chunk', async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  await page.route(chunkPath, () => {
    // Keep the first chunk in Chromium until the operator discards the import.
  });
  await prepareUpload(page, 'Verify discard before the first chunk');
  const firstChunk = page.waitForRequest(chunkPath);
  await page.getByRole('button', { name: 'Start import' }).click();
  const request = await firstChunk;
  expect(chunkOffset(request)).toBe(0);
  const id = new URL(request.url()).pathname.split('/').at(-2);
  if (!id) throw new Error('The upload request does not contain an import ID');
  expect(await readImport(page, id)).toMatchObject({
    state: 'uploading',
    uploaded_bytes: 0,
    attempts: 0,
  });

  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: `Discard the import of ${modelName}` });
  await dialog.getByLabel('Reason').fill('Discard the held first chunk in the isolated run');
  const discard = dialog.getByRole('button', { name: 'Discard', exact: true });
  await expect(discard).toBeDisabled();
  await dialog.getByLabel(/^Type /).fill(modelName);
  const aborted = page.waitForEvent('requestfailed', (failed) => failed === request);
  await discard.click();
  const failed = await aborted;
  await expect(page.getByText('Discarded', { exact: true })).toBeVisible();
  const uploadStep = page.getByRole('listitem').filter({ hasText: 'Upload the bundle' });
  await expect(uploadStep.getByText('Cancelled', { exact: true })).toBeVisible();
  const cancelled = await readImport(page, id);
  expect(cancelled).toMatchObject({
    state: 'cancelled',
    uploaded_bytes: 0,
    attempts: 0,
    model_id: null,
  });
  await testInfo.attach('model-upload-discard-first-chunk', {
    body: JSON.stringify(
      {
        importId: id,
        offset: chunkOffset(failed),
        error: failed.failure()?.errorText,
        state: cancelled.state,
        uploadedBytes: cancelled.uploaded_bytes,
        attempts: cancelled.attempts,
      },
      null,
      2,
    ),
    contentType: 'application/json',
  });
  expect(failed.failure()?.errorText).toBe('net::ERR_ABORTED');
  await expectAccessible(page);
  await testInfo.attach('model-upload-discarded', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });
});
