// The License page: an administrator sees how the installation stands, checks a file before
// importing it, is told exactly why a file is refused, and can send the issuer what it needs.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  activeLicense,
  activeSummary,
  checksStoppingAt,
  importableInspection,
  missingLicense,
  mockControlPlane,
  renewalTerms,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
});

const fileContent = '{"format_version":1}';
const chooseFile = (page: Page, content = fileContent) =>
  page.getByTestId('license-file').setInputFiles({
    name: 'renewal.license',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from(content),
  });
const importButton = (page: Page) => page.getByRole('button', { name: 'Import this license' });

test('is reached from the navigation and the sidebar, and shows the license', async ({ page }) => {
  await page.goto('/');
  await page
    .getByRole('complementary')
    .getByRole('link', { name: /^License\s*Valid/ })
    .click();
  await expect(page.getByRole('heading', { name: 'License', level: 1 })).toBeFocused();
  await expect(page.getByText('Northwind Compute')).toBeVisible();
  await expect(page.getByText(activeSummary.license_id)).toBeVisible();
  await expect(page.getByText('359 days left')).toBeVisible();
  await expect(page.getByText(activeLicense.deployment_id)).toBeVisible();
  await expect(page.getByText('The whole platform, with no limit')).toBeVisible();
  await expect(page.getByText('1 of 64')).toBeVisible();
  await expect(page.getByRole('meter', { name: 'Licenses imported' })).toBeVisible();
  // A healthy license has nothing to say.
  await expect(page.getByText(/renewal due/)).toBeHidden();
});

for (const width of [1440, 1024, 390]) {
  test(`keeps long identifiers inside their cards at ${String(width)} pixels`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/license');
    await page.getByText('Northwind Compute').waitFor();
    const spilled = await page.locator('section').evaluateAll((cards) =>
      cards.flatMap((card) => {
        const edge = card.getBoundingClientRect().right;
        return [...card.querySelectorAll('button, dd')]
          .filter((item) => item.getBoundingClientRect().right > edge)
          .map((item) => item.outerHTML);
      }),
    );
    expect(spilled).toEqual([]);
  });
}

test('imports a first license after showing what it will do', async ({ page }) => {
  control.license = missingLicense;
  control.inspection = {
    ...importableInspection,
    file: { ...renewalTerms, replaces_license_id: '' },
  };
  await page.goto('/license');
  await expect(page.getByText('No license is installed')).toBeVisible();
  await expect(page.getByRole('link', { name: /No license installed/ })).toBeVisible();
  await chooseFile(page);
  await expect(page.getByText('This file can be imported')).toBeVisible();
  await expect(page.getByText('It takes effect at once.')).toBeVisible();
  await expect(page.getByText('cannot be removed')).toBeVisible();
  expect(control.inspectedFiles).toEqual([fileContent]);
  await expect(importButton(page)).toBeDisabled();
  await page.getByLabel('Reason').fill('First license');
  await importButton(page).click();
  await expect(page.getByRole('status').filter({ hasText: 'License imported' })).toBeVisible();
  expect(control.licenseImports).toEqual([
    { artifact: fileContent, reason: 'First license', expected_current_license_id: '' },
  ]);
  await expect(page.getByText('No license is installed')).toBeHidden();
  await expect(
    page.getByRole('complementary').getByRole('link', { name: /^License\s*Valid/ }),
  ).toBeVisible();
  // Back to the start, ready for the next file.
  await expect(page.getByRole('button', { name: 'Choose a file' })).toBeVisible();
});

test('binds a renewal to the history it looked at', async ({ page }) => {
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText('replaces the current license')).toBeVisible();
  await page.getByLabel('Reason').fill('Annual renewal');
  await importButton(page).click();
  await expect(page.getByRole('status').filter({ hasText: 'License imported' })).toBeVisible();
  expect(control.licenseImports[0]?.expected_current_license_id).toBe(activeSummary.license_id);
  await expect(page.getByText('2 of 64')).toBeVisible();
});

test('says when a renewal starts later', async ({ page }) => {
  control.inspection = {
    ...importableInspection,
    file: { ...renewalTerms, not_before: '2027-01-01T00:00:00Z' },
  };
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText(/^It takes over on /)).toBeVisible();
});

test('says a file is already installed, and imports nothing', async ({ page }) => {
  control.inspection = { ...importableInspection, outcome: 'installed' };
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText('This file is already installed')).toBeVisible();
  await expect(importButton(page)).toBeHidden();
});

const refusals = [
  { check: 'format', code: 'LICENSE_ARTIFACT_INVALID', title: 'This is not a license file' },
  { check: 'key', code: 'LICENSE_ARTIFACT_INVALID', title: 'Another issuer signed this file' },
  {
    check: 'signature',
    code: 'LICENSE_ARTIFACT_INVALID',
    title: 'The file changed after it was signed',
  },
  { check: 'terms', code: 'LICENSE_ARTIFACT_INVALID', title: "The file's terms are not valid" },
  {
    check: 'deployment',
    code: 'LICENSE_DEPLOYMENT_MISMATCH',
    title: 'This file is for another installation',
  },
  {
    check: 'identity',
    code: 'LICENSE_ID_CONFLICT',
    title: 'Another file already uses this license ID',
  },
  {
    check: 'capacity',
    code: 'LICENSE_IMPORT_CAPACITY_REQUIRED',
    title: 'The license history is full',
  },
  {
    check: 'renewal',
    code: 'LICENSE_PREDECESSOR_REQUIRED',
    title: 'This file renews a license that is not installed',
  },
  {
    check: 'renewal',
    code: 'LICENSE_PENDING_SUCCESSOR_EXISTS',
    title: 'A renewal is already waiting',
  },
] as const;

for (const { check, code, title } of refusals) {
  test(`explains a refusal: ${title}`, async ({ page }) => {
    control.inspection = {
      ...importableInspection,
      outcome: 'refused',
      code,
      checks: checksStoppingAt(check),
      file: code === 'LICENSE_ARTIFACT_INVALID' ? null : renewalTerms,
    };
    await page.goto('/license');
    await chooseFile(page);
    await expect(page.getByRole('alert').getByText(title)).toBeVisible();
    await expect(importButton(page)).toBeHidden();
    await expect(page.getByRole('listitem').filter({ hasText: 'Failed' })).toHaveCount(1);
    await expect(page.getByRole('button', { name: 'Choose another file' })).toBeVisible();
  });
}

const windows = [
  ['wrong_predecessor', 'This file renews a different license'],
  ['issued_before_current', 'This file is older than the current license'],
  ['starts_before_current', 'This file starts before the current license'],
  ['starts_after_grace', 'This file starts after the grace period ends'],
  ['expires_no_later', 'This file adds no time'],
] as const;

for (const [detail, title] of windows) {
  test(`names the renewal rule: ${detail}`, async ({ page }) => {
    control.inspection = {
      ...importableInspection,
      outcome: 'refused',
      code: 'LICENSE_RENEWAL_WINDOW_INVALID',
      detail,
      checks: checksStoppingAt('renewal'),
    };
    await page.goto('/license');
    await chooseFile(page);
    await expect(page.getByRole('alert').getByText(title)).toBeVisible();
  });
}

test('sets the two deployment IDs side by side', async ({ page }) => {
  const other = '6f1c7a1e-0000-4000-8000-0000000000ff';
  control.inspection = {
    ...importableInspection,
    outcome: 'refused',
    code: 'LICENSE_DEPLOYMENT_MISMATCH',
    checks: checksStoppingAt('deployment'),
    file: { ...renewalTerms, deployment_id: other },
  };
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText(other)).toBeVisible();
  await expect(page.getByText('In the file')).toBeVisible();
  await expect(page.getByText(activeLicense.deployment_id).first()).toBeVisible();
});

test('names both keys when another issuer signed the file', async ({ page }) => {
  control.inspection = {
    ...importableInspection,
    outcome: 'refused',
    code: 'LICENSE_ARTIFACT_INVALID',
    checks: checksStoppingAt('key'),
    key_id: 'other-issuer',
    file: null,
  };
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText('other-issuer')).toBeVisible();
  await expect(page.getByText('issuer-2026')).toBeVisible();
});

test('downloads the report of a refusal', async ({ page }) => {
  control.inspection = {
    ...importableInspection,
    outcome: 'refused',
    code: 'LICENSE_ID_CONFLICT',
    checks: checksStoppingAt('identity'),
  };
  await page.goto('/license');
  await chooseFile(page);
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download report' }).click();
  expect((await download).suggestedFilename()).toBe('license-inspection.json');
});

test('a person can choose another file after a refusal', async ({ page }) => {
  control.inspection = {
    ...importableInspection,
    outcome: 'refused',
    code: 'LICENSE_ARTIFACT_INVALID',
    checks: checksStoppingAt('format'),
    file: null,
  };
  await page.goto('/license');
  await chooseFile(page);
  await expect(page.getByText('This is not a license file')).toBeVisible();
  control.inspection = importableInspection;
  await chooseFile(page, '{"second":true}');
  await expect(page.getByText('This file can be imported')).toBeVisible();
  await expect(page.getByText('This is not a license file')).toBeHidden();
});

test('refuses an empty or oversized file without sending it', async ({ page }) => {
  await page.goto('/license');
  await chooseFile(page, '');
  await expect(page.getByText('This file is empty.')).toBeVisible();
  await chooseFile(page, 'x'.repeat(65_537));
  await expect(page.getByText('so it is not a license file')).toBeVisible();
  expect(control.inspectedFiles).toEqual([]);
});

test('a failed inspection can be retried', async ({ page }) => {
  control.nextInspection = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/license');
  await chooseFile(page);
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByText('This file can be imported')).toBeVisible();
});

test('a failed import keeps the file and can be retried', async ({ page }) => {
  control.nextLicenseImport = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/license');
  await chooseFile(page);
  await page.getByLabel('Reason').fill('Annual renewal');
  await importButton(page).click();
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'License imported' })).toBeVisible();
  expect(control.licenseImports).toHaveLength(1);
});

test('checks the file again when another administrator imported first', async ({ page }) => {
  await page.goto('/license');
  await chooseFile(page);
  await page.getByLabel('Reason').fill('Annual renewal');
  control.nextLicenseImport = { status: 409, code: 'LICENSE_CURRENT_CHANGED' };
  control.inspection = {
    ...importableInspection,
    outcome: 'refused',
    code: 'LICENSE_RENEWAL_WINDOW_INVALID',
    detail: 'wrong_predecessor',
    checks: checksStoppingAt('renewal'),
  };
  await importButton(page).click();
  await expect(page.getByText('The license changed while you were working')).toBeVisible();
  await expect(page.getByText('This file renews a different license')).toBeVisible();
  expect(control.inspectedFiles).toHaveLength(2);
  await expect(importButton(page)).toBeHidden();
});

const standings = [
  [
    'grace',
    { state: 'grace', warning: true },
    'The license expired on',
    'New work is allowed until',
  ],
  [
    'expired',
    { state: 'expired', new_business_allowed: false, warning: true },
    'The license has expired',
    'running work carries on',
  ],
  [
    'clock',
    { state: 'clock_suspect', new_business_allowed: false, warning: true },
    "The server's clock moved backwards",
    'this clears by itself',
  ],
] as const;

for (const [name, change, title, next] of standings) {
  test(`explains the standing: ${name}`, async ({ page }) => {
    control.license = { ...activeLicense, ...change };
    await page.goto('/license');
    await expect(page.getByText(title)).toBeVisible();
    await expect(page.getByText(next)).toBeVisible();
  });
}

test('says when a license has not started', async ({ page }) => {
  control.license = {
    ...missingLicense,
    state: 'not_yet_valid',
    pending: activeSummary,
    accepted_licenses: 1,
  };
  await page.goto('/license');
  await expect(page.getByText(/^The license starts on /)).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Next license' })).toBeVisible();
});

test('asks for a renewal sixty days ahead, by the server clock', async ({ page }) => {
  control.license = {
    ...activeLicense,
    current: { ...activeSummary, expires_at: '2027-01-06T08:00:00Z' },
  };
  await page.goto('/license');
  await expect(page.getByText('License renewal due in 60 days')).toBeVisible();
  await expect(page.getByText('Nothing stops working until then.')).toBeVisible();
});

test('does not ask for a renewal that is already waiting', async ({ page }) => {
  control.license = {
    ...activeLicense,
    current: { ...activeSummary, expires_at: '2027-01-06T08:00:00Z' },
    pending: renewalTerms,
  };
  await page.goto('/license');
  await expect(page.getByRole('heading', { name: 'Next license' })).toBeVisible();
  await expect(page.getByText(/renewal due/)).toBeHidden();
});

test('downloads diagnostics, and a failure can be retried', async ({ page }) => {
  control.nextDiagnostics = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/license');
  const button = page.getByRole('button', { name: 'Download diagnostics' });
  await button.click();
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Try again' }).click();
  expect((await download).suggestedFilename()).toBe('license-diagnostics.json');
});

test('a license that cannot load offers a retry', async ({ page }) => {
  control.licenseProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
  await page.goto('/license');
  const retry = page.getByRole('button', { name: 'Try again' });
  await expect(retry).toBeVisible();
  await expect(page.getByRole('button', { name: 'Download diagnostics' })).toBeHidden();
  control.licenseProblem = null;
  await retry.click();
  await expect(page.getByText('Northwind Compute')).toBeVisible();
});

test('accepts a dropped file', async ({ page }) => {
  await page.goto('/license');
  await page.getByText('Drop a license file here').waitFor();
  const transfer = await page.evaluateHandle((content) => {
    const data = new DataTransfer();
    data.items.add(new File([content], 'dropped.license'));
    return data;
  }, fileContent);
  await page
    .getByRole('presentation')
    .filter({ hasText: 'Drop a license file here' })
    .dispatchEvent('drop', { dataTransfer: transfer });
  await expect(page.getByText('dropped.license')).toBeVisible();
  expect(control.inspectedFiles).toEqual([fileContent]);
});

test('tells someone without the right that the license is not theirs to see', async ({ page }) => {
  control.licenseProblem = { status: 403, code: 'ACCESS_DENIED' };
  await page.goto('/license');
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Choose a file' })).toBeHidden();
});

test('is not part of the portal', async ({ page }) => {
  control.audience = 'portal';
  await page.goto('/license');
  await expect(page.getByText('Northwind Compute')).toHaveCount(0);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('reads in the interface language', async ({ page }) => {
    control.tenantSettings.default_locale = 'zh-CN';
    control.license = missingLicense;
    await page.goto('/license');
    await expect(page.getByText(chinese['license.standing.missing'])).toBeVisible();
    await expect(page.getByRole('button', { name: chinese['license.file.choose'] })).toBeVisible();
  });
});
