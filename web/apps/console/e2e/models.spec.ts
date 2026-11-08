// The Model library: finding what a workspace has, bringing a model in through upload and
// verification, and what happens when any of that goes wrong (docs/research/model-library.md).
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  derivedModel,
  failedImport,
  mockControlPlane,
  modelCatalog,
  publishedImport,
  readyModel,
  stalledImport,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
});

const bundle = (text = 'abcdefghijklmnopqrstuvwx') => ({
  name: 'bundle.tar',
  mimeType: 'application/x-tar',
  buffer: Buffer.from(text),
});

async function fillForm(page: Page, file = bundle()) {
  await page.goto('/library/import');
  await page.getByTestId('model-bundle').setInputFiles(file);
  await page.getByLabel('Reason').fill('For the support team');
  await page.getByRole('checkbox').check();
}

const start = (page: Page) => page.getByRole('button', { name: 'Start import' }).click();

test('is reached from the navigation, and says what it is for when empty', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Models', exact: true }).click();
  await page.getByRole('navigation').getByRole('link', { name: 'Model library' }).click();
  await expect(page.getByRole('heading', { name: 'Model library', level: 1 })).toBeFocused();
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
  await page.getByRole('main').getByRole('link', { name: 'Import a bundle' }).first().click();
  await expect(page.getByRole('heading', { name: 'Import a bundle', level: 1 })).toBeVisible();
});

for (const locale of ['en', 'zh-CN'] as const) {
  test(`copies the tool-specific bundle commands from offline help (${locale})`, async ({
    page,
    context,
  }) => {
    const zh = locale === 'zh-CN';
    control.preferences.locale = locale;
    const tools = [
      { label: 'Python 3', prefix: 'python3 - ' },
      {
        label: zh ? chinese['models.import.howTo.gnuMac'] : 'GNU tar on macOS (gtar)',
        prefix: 'gtar --format=ustar --no-recursion -cf bundle.tar -- ',
      },
      {
        label: zh ? chinese['models.import.howTo.gnu'] : 'GNU tar (tar)',
        prefix: 'tar --format=ustar --no-recursion -cf bundle.tar -- ',
      },
    ];
    const files = modelCatalog.catalog.files.map(({ path }) => `'${path}'`).join(' ');
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.goto('/help/model-library');
    const main = page.getByRole('main');
    await expect(main.getByText('tarfile.USTAR_FORMAT', { exact: true })).toBeVisible();
    await main
      .getByRole('link', { name: zh ? chinese['models.import.title'] : 'Import a bundle' })
      .click();
    await main
      .getByText(zh ? chinese['models.import.howTo'] : 'How to make a bundle', { exact: true })
      .click();
    await expect(main.getByText(/tar --version/)).toBeVisible();
    for (const { label, prefix } of tools) {
      await expect(main.getByRole('heading', { name: label, exact: true })).toBeVisible();
      await main
        .getByRole('button', {
          name: (zh ? chinese['action.copyNamed'] : 'Copy: {name}').replace('{name}', label),
        })
        .click();
      const copied = await page.evaluate(() => navigator.clipboard.readText());
      expect(copied.split('\n')[0]).toBe(
        `${prefix}${files}${prefix.startsWith('python3') ? " <<'PY'" : ''}`,
      );
      expect(copied).not.toContain('COPYFILE_DISABLE');
      if (prefix.startsWith('python3')) expect(copied).toContain('format=tarfile.USTAR_FORMAT');
    }
  });
}

test('keeps bundle commands selectable when the clipboard is unavailable', async ({ page }) => {
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', { value: undefined }),
  );
  await page.goto('/library/import');
  await page.getByText('How to make a bundle', { exact: true }).click();
  const copy = page.getByRole('button', {
    name: 'Clipboard unavailable. Select and copy the Python 3 command.',
  });
  await expect(copy).toHaveAttribute('aria-disabled', 'true');
  await expect(page.locator('pre code').filter({ hasText: 'tarfile.USTAR_FORMAT' })).toBeVisible();
});

test('filters from the address, and says which filter hid everything', async ({ page }) => {
  control.models = [readyModel, derivedModel];
  await page.goto('/library?name=support');
  await expect(page.getByRole('row', { name: /Support assistant/ })).toBeVisible();
  await expect(page.getByRole('button', { name: `Show ${readyModel.name}` })).toHaveCount(0);
  await page.getByRole('searchbox', { name: 'Search models' }).fill('nothing like this');
  await expect(page.getByRole('heading', { name: 'No model matches' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('row', { name: /Support assistant/ })).toBeVisible();
  await page.getByLabel('Kind').selectOption('approved_base');
  await expect(page).toHaveURL(/kind=approved_base/);
  await expect(page.getByRole('row', { name: /Support assistant/ })).toHaveCount(0);
});

test('previews a model in a drawer, and opens its own page', async ({ page }) => {
  control.models = [readyModel];
  await page.goto('/library');
  await page.getByRole('button', { name: `Show ${readyModel.name}` }).click();
  const drawer = page.getByRole('dialog', { name: readyModel.name });
  await expect(drawer).toContainText('Apache-2.0');
  await expect(drawer).toContainText('2,048 tokens');
  await drawer.getByRole('link', { name: 'Open the full page' }).click();
  await expect(page.getByRole('heading', { name: readyModel.name, level: 1 })).toBeVisible();
  await expect(page.getByText('Qwen3ForCausalLM')).toBeVisible();
});

test('finds a model from the command menu and opens its drawer', async ({ page }) => {
  control.models = [readyModel];
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox').fill('qwen');
  await page.getByRole('option', { name: new RegExp(readyModel.name) }).click();
  await expect(page.getByRole('dialog', { name: readyModel.name })).toContainText('Apache-2.0');
});

test('shows a failed read and retries it', async ({ page }) => {
  control.modelsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/library');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  control.modelsProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
});

test('says what the catalogue can do in this release', async ({ page }) => {
  await page.goto('/library?tab=catalogue');
  await expect(page.getByText('Searching both registries is not ready yet')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Qwen/Qwen3-0.6B' })).toBeVisible();
});

test('imports a bundle: sends every chunk, verifies, and opens the model', async ({ page }) => {
  control.modelCatalog = {
    ...modelCatalog,
    catalog: {
      ...modelCatalog.catalog,
      files: modelCatalog.catalog.files.map((file) => ({ ...file, size_bytes: 8 })),
    },
  };
  await fillForm(page);
  await expect(page.getByRole('heading', { name: 'Storage needed' })).toBeVisible();
  await expect(
    page.getByText('Allow about 40 byte in the model store', { exact: false }),
  ).toContainText('The model files use 16 byte.');
  await expect(
    page.getByText(
      'After publication, the server removes temporary import files in the background.',
    ),
  ).toBeVisible();
  await start(page);
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
  const chunks = Object.keys(control.modelChunks[control.modelImports[0]?.id ?? ''] ?? {});
  expect(chunks).toEqual(['0', '8', '16']);

  // Ready is a publication result, even while temporary upload records remain.
  await page.reload();
  await expect(page.getByRole('status').filter({ hasText: 'Model published' })).toContainText(
    'These files may still take up space.',
  );
  const timeline = page.getByRole('list', { name: 'Steps of this import' });
  await expect(timeline.getByRole('listitem')).toHaveCount(3);
  await expect(timeline.getByRole('listitem').last()).toContainText('Publish the model');
  await expect(page.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  await page.getByRole('link', { name: 'Open the model' }).click();
  await expect(page.getByRole('heading', { name: 'Qwen3-0.6B', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: 'Model library' }).first().click();
  await expect(page.getByRole('button', { name: 'Show Qwen3-0.6B' })).toBeVisible();
});

test('shows publication without reading the import catalogue', async ({ page }) => {
  control.modelImports = [publishedImport];
  control.catalogProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  const requests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.endsWith('/models/catalog')) requests.push(request.url());
  });
  await page.goto(`/library/imports/${publishedImport.id}`);
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
  await expect(page.getByText('Model published', { exact: true })).toBeVisible();
  expect(requests).toEqual([]);
});

test('keeps a paused upload visible while its resume settings are unavailable', async ({
  page,
}) => {
  control.modelImports = [stalledImport];
  control.catalogProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto(`/library/imports/${stalledImport.id}`);
  await expect(page.getByText('Upload incomplete', { exact: true })).toBeVisible();
  const timeline = page.getByRole('list', { name: 'Steps of this import' });
  await expect(timeline.getByRole('listitem').first()).not.toContainText('Running');
  const choose = page.getByRole('button', { name: 'Choose the file' });
  await expect(choose).toHaveAttribute('aria-disabled', 'true');
  await expect(page.getByText(/The import status is available/)).toBeVisible();
  control.catalogProblem = null;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(choose).toBeEnabled();
});

test('rides out a lost answer and a dropped connection without help', async ({ page }) => {
  control.chunkFailures = ['lost', 'dropped'];
  await fillForm(page);
  await start(page);
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible({ timeout: 15_000 });
});

test('stops on a long outage, says so, and continues on request', async ({ page }) => {
  const down = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  control.chunkFailures = [down, down, down, down];
  await fillForm(page);
  await start(page);
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible({ timeout: 15_000 });
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
});

test('discards an import before its first chunk is accepted', async ({ page }) => {
  const chunkPath = /\/admin\/model-imports\/[0-9a-f-]{36}\/chunks$/;
  await page.route(chunkPath, () => {
    // Hold the first request without forwarding it to the scripted server.
  });
  await fillForm(page);
  const firstChunk = page.waitForRequest(chunkPath);
  await start(page);
  const request = await firstChunk;

  await page.getByRole('button', { name: 'Discard' }).click();
  const dialog = page.getByRole('dialog', { name: 'Discard the import of Qwen3-0.6B' });
  await dialog.getByLabel('Reason').fill('The source bundle was replaced');
  const discard = dialog.getByRole('button', { name: 'Discard' });
  await expect(discard).toBeDisabled();
  await dialog.getByLabel(/^Type /).fill('Qwen3-0.6B');

  const aborted = page.waitForEvent('requestfailed', (failed) => failed === request);
  await discard.click();
  await aborted;
  await expect(page.getByText('Discarded', { exact: true }).first()).toBeVisible();
  expect(control.modelImports[0]).toMatchObject({
    state: 'cancelled',
    uploaded_bytes: 0,
    attempts: 0,
  });
  expect(control.modelChunks).toEqual({});
  expect(control.models).toEqual([]);
});

test('keeps uploading while the person moves around the console', async ({ page }) => {
  control.chunkDelayMilliseconds = 400;
  await fillForm(page);
  await start(page);
  await expect(page.getByText('You can use the rest of the console')).toBeVisible();
  await expect(page.getByRole('meter', { name: 'Uploaded so far' })).toBeVisible();
  await page.getByRole('link', { name: 'Imports' }).click();
  await page.getByRole('link', { name: 'Qwen3-0.6B' }).click();
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
});

test('continues after the tab closed, from what the server holds', async ({ page }) => {
  control.modelImports = [{ ...stalledImport }];
  control.modelChunks = {
    [stalledImport.id]: {
      0: 'bc4f0ccbb7ae62ac9aa2bcc5c53ac39fa38b9c03d3e3edd30a0ceb0e2e6d7a8b',
    },
  };
  control.chunkFailures = [];
  await page.goto('/library?tab=imports');
  await page.getByRole('link', { name: stalledImport.name }).click();
  await expect(page.getByText('This tab is not uploading')).toBeVisible();
  await page.getByTestId('model-bundle-resume').setInputFiles(bundle('too short'));
  await expect(page.getByText('That file is a different size')).toBeVisible();
  await page.getByTestId('model-bundle-resume').setInputFiles(bundle());
  await expect(page.getByText('This is not the file this import began with')).toBeVisible();
  expect(control.modelImports[0]?.uploaded_bytes).toBe(8);
  await expect(page.getByTestId('model-bundle-resume')).toBeAttached();
});

test('continues with the same file', async ({ page }) => {
  const file = bundle();
  control.modelImports = [{ ...stalledImport, uploaded_bytes: 0 }];
  await page.goto(`/library/imports/${stalledImport.id}`);
  await page.getByTestId('model-bundle-resume').setInputFiles(file);
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
});

test('explains a failed verification, verifies again, and can discard', async ({ page }) => {
  control.modelImports = [{ ...failedImport }];
  control.verificationOutcome = 'MODEL_DIGEST_MISMATCH';
  await page.goto(`/library/imports/${failedImport.id}`);
  await expect(page.getByText('A file does not match its published hash')).toBeVisible();
  await expect(page.getByText(/Discard this import, then fetch the model again/)).toBeVisible();
  await expect(page.getByText('Model published', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open the model' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Verify again (try 2 of 3)' }).click();
  await expect(page.getByRole('button', { name: 'Verify again (try 3 of 3)' })).toBeVisible();
  await page.getByRole('button', { name: 'Discard' }).click();
  const dialog = page.getByRole('dialog', { name: `Discard the import of ${failedImport.name}` });
  await dialog.getByLabel(/^Type /).fill(failedImport.name);
  await dialog.getByLabel('Reason').fill('Fetched again');
  await dialog.getByRole('button', { name: 'Discard' }).click();
  await expect(page.getByText('Discarded', { exact: true }).first()).toBeVisible();
  await expect(page.getByRole('button', { name: 'Discard' })).toHaveCount(0);
});

test('says when an import has used all its tries', async ({ page }) => {
  control.modelImports = [{ ...failedImport, attempts: 3 }];
  await page.goto(`/library/imports/${failedImport.id}`);
  await expect(page.getByText('This import used all its tries')).toBeVisible();
  await expect(page.getByRole('button', { name: /Verify again/ })).toHaveCount(0);
});

test('cannot start without a store, and says what to do', async ({ page }) => {
  control.modelCatalog = { ...modelCatalog, storage_available: false };
  await fillForm(page);
  await expect(page.getByText('This installation has no model store')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start import' })).toBeDisabled();
});

test('points to the import in the way of a new one', async ({ page }) => {
  control.modelImports = [{ ...failedImport }];
  await fillForm(page);
  await expect(page.getByText(`${failedImport.name} is still being imported`)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start import' })).toBeDisabled();
  await page.getByRole('link', { name: 'Open it' }).click();
  await expect(page.getByText('A file does not match its published hash')).toBeVisible();
});

test('refuses a file that cannot be the bundle before anything is sent', async ({ page }) => {
  await fillForm(page, bundle('tiny'));
  await expect(page.getByText('This file is too small to be the bundle')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start import' })).toBeDisabled();
  await page.getByTestId('model-bundle').setInputFiles(bundle('x'.repeat(65)));
  await expect(page.getByText('This file is larger than this installation accepts')).toBeVisible();
});

test('explains a refused start and keeps the form', async ({ page }) => {
  control.nextModelImport = { status: 409, code: 'MODEL_ALREADY_EXISTS' };
  await fillForm(page);
  await start(page);
  await expect(page.getByText('This workspace already has this model')).toBeVisible();
  await expect(page.getByLabel('Reason')).toHaveValue('For the support team');
});

test('retires a model after its name is typed, and keeps one still in use', async ({ page }) => {
  control.models = [readyModel];
  control.nextRetire = { status: 409, code: 'MODEL_VERSION_REFERENCED' };
  await page.goto(`/library/${readyModel.id}`);
  await page.getByRole('button', { name: 'Retire' }).click();
  const dialog = page.getByRole('dialog', { name: `Retire ${readyModel.name}` });
  await expect(dialog.getByRole('button', { name: 'Retire' })).toBeDisabled();
  await dialog.getByLabel(/^Type /).fill(readyModel.name);
  await dialog.getByLabel('Reason').fill('Replaced');
  await dialog.getByRole('button', { name: 'Retire' }).click();
  await expect(dialog.getByText('This model is in use')).toBeVisible();
  await dialog.getByRole('button', { name: 'Retire' }).click();
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'retired' })).toBeVisible();
});

test('leads from a fine-tuned model to the one it was made from', async ({ page }) => {
  control.models = [readyModel, derivedModel];
  await page.goto(`/library/${derivedModel.id}`);
  await page.getByRole('link', { name: 'The model it was made from' }).click();
  await expect(page.getByRole('heading', { name: readyModel.name, level: 1 })).toBeVisible();
});

test('says when a model or an import does not exist', async ({ page }) => {
  await page.goto(`/library/${readyModel.id}`);
  await expect(page.getByRole('heading', { name: 'No such model', level: 1 })).toBeVisible();
  await page.goto(`/library/imports/${failedImport.id}`);
  await expect(page.getByRole('heading', { name: 'No such import', level: 1 })).toBeVisible();
});

test('explains import storage and background cleanup in Simplified Chinese', async ({ page }) => {
  control.preferences.locale = 'zh-CN';
  control.modelImports = [publishedImport];
  await page.goto('/library/import');
  await expect(
    page.getByRole('heading', { name: chinese['models.import.storage.title'] }),
  ).toBeVisible();
  await expect(page.getByText(chinese['models.import.storage.cleanup'])).toBeVisible();
  await expect(page.getByRole('button', { name: chinese['models.import.submit'] })).toBeDisabled();

  await page.goto(`/library/imports/${publishedImport.id}`);
  await expect(
    page.getByRole('status').filter({ hasText: chinese['models.import.published.title'] }),
  ).toContainText(chinese['models.import.published.detail']);
  await expect(page.getByRole('link', { name: chinese['models.import.openModel'] })).toBeVisible();
});
