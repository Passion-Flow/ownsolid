// The Model library: finding what a workspace has, bringing a model in through upload and
// verification, and what happens when any of that goes wrong (docs/research/model-library.md).
import {
  derivedModel,
  failedImport,
  mockControlPlane,
  modelCatalog,
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
  await fillForm(page);
  await expect(
    page.getByText(/takes .* of the model store: the bundle, and the model/),
  ).toBeVisible();
  await start(page);
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible();
  const chunks = Object.keys(control.modelChunks[control.modelImports[0]?.id ?? ''] ?? {});
  expect(chunks).toEqual(['0', '8', '16']);
  await page.getByRole('link', { name: 'Open the model' }).click();
  await expect(page.getByRole('heading', { name: 'Qwen3-0.6B', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: 'Model library' }).first().click();
  await expect(page.getByRole('button', { name: 'Show Qwen3-0.6B' })).toBeVisible();
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
  await expect(page.getByText('The upload is not running')).toBeVisible();
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
