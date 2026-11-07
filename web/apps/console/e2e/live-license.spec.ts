// Runs the License page on a real installation: real signed files are refused for the real reasons,
// a real renewal is imported and survives a reload, and diagnostics download. Each run renews the
// license by a day. Skipped unless the environment names a way to sign test files;
// docs/development/environment.md has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { execSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

// Reads the license state as JSON on standard input and prints the test files as JSON.
const signFiles = process.env.OWNSOLID_LIVE_LICENSE_COMMAND ?? '';

test.skip(origin === '' || signFiles === '', 'needs a way to sign test license files');
test.use(liveOptions);

interface TestFiles {
  renewal: string;
  otherDeployment: string;
  otherIssuer: string;
  changed: string;
  addsNoTime: string;
}

const choose = async (page: Page, name: string, content: string) => {
  await page.getByTestId('license-file').setInputFiles({
    name,
    mimeType: 'application/octet-stream',
    buffer: Buffer.from(content),
  });
};

test('refuses bad files for the right reason and imports a renewal', async ({ page }) => {
  test.setTimeout(120_000);
  const elsewhere: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).origin !== origin) elsewhere.push(request.url());
  });

  await page.goto('/license');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'License', level: 1 })).toBeVisible();
  await expect(page.getByText('The whole platform, with no limit')).toBeVisible();
  await expectAccessible(page);

  // Read in the page: a request from the test process has neither the host rules nor the pin.
  const before = await page.evaluate(async () => {
    const response = await fetch('/api/v1/admin/license');
    const body = (await response.json()) as {
      data: { history_head_license_id: string; deployment_id: string; accepted_licenses: number };
    };
    return body.data;
  });
  const files = JSON.parse(
    execSync(signFiles, { input: JSON.stringify(before), encoding: 'utf8', timeout: 60_000 }),
  ) as TestFiles;

  const diagnostics = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download diagnostics' }).click();
  const saved = await diagnostics;
  expect(saved.suggestedFilename()).toBe('license-diagnostics.json');
  const report = JSON.parse(readFileSync(await saved.path(), 'utf8')) as {
    state: { deployment_id: string };
  };
  expect(report.state.deployment_id).toBe(before.deployment_id);

  await choose(page, 'notes.txt', 'this is not a license');
  await expect(page.getByRole('alert').getByText('This is not a license file')).toBeVisible();
  await choose(page, 'issuer.license', files.otherIssuer);
  await expect(page.getByRole('alert').getByText('Another issuer signed this file')).toBeVisible();
  await choose(page, 'changed.license', files.changed);
  await expect(
    page.getByRole('alert').getByText('The file changed after it was signed'),
  ).toBeVisible();
  await choose(page, 'other.license', files.otherDeployment);
  await expect(
    page.getByRole('alert').getByText('This file is for another installation'),
  ).toBeVisible();
  await expect(page.getByText('In the file')).toBeVisible();
  await expectAccessible(page);
  await choose(page, 'short.license', files.addsNoTime);
  await expect(page.getByRole('alert').getByText('This file adds no time')).toBeVisible();

  await choose(page, 'renewal.license', files.renewal);
  await expect(page.getByText('It takes effect at once and replaces')).toBeVisible();
  await expectAccessible(page);
  await page.getByLabel('Reason').fill('Live renewal run');
  await page.getByRole('button', { name: 'Import this license' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'License imported' })).toBeVisible();

  await page.reload();
  await expect(page.getByText(`${String(before.accepted_licenses + 1)} of`)).toBeVisible();
  await choose(page, 'renewal.license', files.renewal);
  await expect(page.getByText('This file is already installed')).toBeVisible();
  expect(elsewhere).toEqual([]);
});
