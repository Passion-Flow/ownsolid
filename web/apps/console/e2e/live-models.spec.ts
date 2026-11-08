// Imports the real model into a real installation: a browser sends the whole bundle, is interrupted,
// refuses a different file, continues, and the server verifies every file against its published
// hash. Skipped unless the environment names a bundle; docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test, type Page } from '@playwright/test';

const bundle = process.env.OWNSOLID_LIVE_MODEL_BUNDLE ?? '';
// A file of the same size as the bundle, with different bytes.
const impostor = process.env.OWNSOLID_LIVE_MODEL_IMPOSTOR ?? '';
const modelName = 'Qwen3-0.6B';

test.skip(origin === '' || bundle === '' || impostor === '', 'needs the model bundle');
test.use(liveOptions);
// The second retires what the first imports.
test.describe.configure({ mode: 'serial' });

const signIn = async (page: Page, path: string, heading: string | RegExp) => {
  await page.goto(path);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: heading, level: 1 })).toBeVisible();
};

test('imports the model from the console, through an interruption', async ({ browser }) => {
  test.setTimeout(30 * 60_000);
  const context = await browser.newContext();
  const first = await context.newPage();
  const outside: string[] = [];
  context.on('request', (request) => {
    if (!request.url().startsWith(origin) && !request.url().startsWith('blob:')) {
      outside.push(request.url());
    }
  });
  await signIn(first, '/library/import', 'Import a bundle');
  await expectAccessible(first);
  await first.getByTestId('model-bundle').setInputFiles(bundle);
  await expect(
    first.getByText(/takes .* of the model store: the bundle, and the model/),
  ).toBeVisible();
  await first.getByLabel('Reason').fill('Live run of the Model library');
  await first.getByRole('checkbox').check();
  await first.getByRole('button', { name: 'Start import' }).click();
  await expect(first.getByRole('meter')).toBeVisible();
  await expect(first.getByRole('status').filter({ hasText: / of / })).toContainText(/^\d/);
  // Far enough that continuing means something.
  await expect(first.getByText(/^[1-9]\d{1,2}(\.\d)? MB of /)).toBeVisible({ timeout: 300_000 });
  const importPath = new URL(first.url()).pathname;
  first.on('dialog', (dialog) => {
    void dialog.accept();
  });
  await first.close({ runBeforeUnload: false });

  const page = await context.newPage();
  await page.goto('/library?tab=imports');
  // Earlier runs leave their discarded imports in the list.
  await page
    .getByRole('row', { name: /Uploading/ })
    .getByRole('link', { name: modelName })
    .click();
  await expect(page).toHaveURL(new RegExp(`${importPath}$`));
  await expect(page.getByText('This tab is not uploading')).toBeVisible();
  await expectAccessible(page);

  await page.getByTestId('model-bundle-resume').setInputFiles(impostor);
  await expect(page.getByText('This is not the file this import began with')).toBeVisible({
    timeout: 60_000,
  });

  await page.getByTestId('model-bundle-resume').setInputFiles(bundle);
  await expect(page.getByText('You can use the rest of the console')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the model' })).toBeVisible({
    timeout: 20 * 60_000,
  });
  await expectAccessible(page);

  await page.getByRole('link', { name: 'Open the model' }).click();
  await expect(page.getByRole('heading', { name: modelName, level: 1 })).toBeVisible();
  await expect(page.getByText('Apache-2.0')).toBeVisible();
  await expect(page.getByRole('link', { name: /^Import of / })).toBeVisible();
  await page.getByRole('tab', { name: 'Files' }).click();
  await expect(page.getByRole('cell', { name: 'model.safetensors' })).toBeVisible();
  await expectAccessible(page);
  expect(outside).toEqual([]);
  await context.close();
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
