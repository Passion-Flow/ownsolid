// Reopens the real model import recovered from an immutable object with no chunk history.
import { readFileSync } from 'node:fs';
import { expectAccessible } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test } from '@playwright/test';

const fixtureFile = process.env.OWNSOLID_LIVE_MODEL_RECOVERY_FILE ?? '';
test.skip(origin === '' || fixtureFile === '', 'needs the isolated verified-object fixture');
test.use(liveOptions);

interface FixtureImport {
  id: string;
  name: string;
}

interface RecoveryFixture {
  recovered: FixtureImport;
  empty: FixtureImport;
  files: { path: string; sha256: string }[];
}

test('opens the recovered model without uploading and retains its files after retirement', async ({
  page,
}, testInfo) => {
  test.setTimeout(180_000);
  const fixture = JSON.parse(readFileSync(fixtureFile, 'utf8')) as RecoveryFixture;
  const { recovered, empty, files } = fixture;
  const importPath = `/library/imports/${recovered.id}`;
  const modelPath = `/library/${recovered.id}`;
  const allowed = new URL(origin).origin;
  const uploads: string[] = [];
  const outside = new Set<string>();
  page.on('request', (request) => {
    const address = new URL(request.url());
    if (address.origin !== allowed && address.protocol !== 'blob:') outside.add(address.origin);
    if (address.pathname.endsWith('/chunks') && request.method() !== 'GET') {
      uploads.push(address.pathname);
    }
  });

  await page.goto(importPath);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  const heading = page.getByRole('heading', { name: recovered.name, level: 1 });
  await expect(heading).toBeVisible();
  const model = page.getByRole('link', { name: 'Open the model' });
  await expect(model).toHaveAttribute('href', modelPath, { timeout: 90_000 });
  await expect(page.getByText('Ready', { exact: true })).toBeVisible();
  await expect(page.getByTestId('model-bundle-resume')).toHaveCount(0);
  await expectAccessible(page);
  await testInfo.attach('recovered-model-import', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await page.reload();
  await expect(model).toHaveAttribute('href', modelPath);
  await model.click();
  await expect(page).toHaveURL(`${allowed}${modelPath}`);
  await expect(heading).toBeVisible();
  await expect(page.getByRole('link', { name: /^Import of / })).toHaveAttribute('href', importPath);
  await page.getByRole('tab', { name: 'Files' }).click();
  const table = page.getByRole('table', { name: 'Files of this model' });
  await expect(table.getByRole('row')).toHaveCount(files.length + 1);
  for (const file of files) {
    const row = table
      .getByRole('row')
      .filter({ has: page.getByRole('cell', { name: file.path, exact: true }) });
    await expect(row).toContainText(file.sha256);
  }
  await expectAccessible(page);
  await testInfo.attach('recovered-model-files', {
    body: await page.screenshot({ animations: 'disabled' }),
    contentType: 'image/png',
  });

  await page.getByRole('button', { name: 'Retire', exact: true }).click();
  const retirement = page.getByRole('dialog', { name: `Retire ${recovered.name}` });
  await retirement.getByRole('textbox', { name: /^Type / }).fill(recovered.name);
  await retirement.getByLabel('Reason').fill('Retire the recovered model in the isolated run');
  await retirement.getByRole('button', { name: 'Retire', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No models yet' })).toBeVisible();

  await page.goto(`/library/imports/${empty.id}`);
  await expect(page.getByRole('heading', { name: empty.name, level: 1 })).toBeVisible();
  await expect(page.getByText('Upload incomplete', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  const discard = page.getByRole('dialog', { name: `Discard the import of ${empty.name}` });
  await discard.getByRole('textbox', { name: /^Type / }).fill(empty.name);
  await discard.getByLabel('Reason').fill('Discard the empty import in the isolated run');
  await discard.getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(page.getByText('Discarded', { exact: true })).toBeVisible();
  expect(uploads).toEqual([]);
  expect([...outside]).toEqual([]);
});
