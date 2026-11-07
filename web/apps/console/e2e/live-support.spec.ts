// Runs What is supported on a real installation: the page is read from the running server, a person
// who is not signed in is refused, and the portal's page answers from the same declaration. Skipped
// unless the environment describes an installation; docs/development/environment.md has the recipe.
import { declaredCapabilities, expectAccessible } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from './live-install.ts';

test.skip(origin === '', 'needs an installation');
test.use(liveOptions);

test('shows the shipped declaration and refuses a stranger', async ({ page }) => {
  test.setTimeout(120_000);
  const refused: string[] = [];
  const elsewhere: string[] = [];
  page.on('response', (response) => {
    const { pathname } = new URL(response.url());
    if (pathname.startsWith('/api/') && pathname !== '/api/v1/me' && response.status() >= 400) {
      refused.push(`${String(response.status())} ${pathname}`);
    }
  });
  page.on('request', (sent) => {
    if (new URL(sent.url()).origin !== origin) elsewhere.push(sent.url());
  });

  await page.goto('/support');
  await expect(page.getByLabel('Email')).toBeVisible();
  // Asked from the browser, so the request takes the installation's own TLS and host names.
  const stranger = await page.evaluate(async () => {
    const response = await fetch('/api/v1/capabilities', { credentials: 'omit' });
    return response.status;
  });
  expect(stranger).toBe(401);
  refused.length = 0;

  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'What is supported', level: 1 })).toBeVisible();

  for (const registry of declaredCapabilities().registries) {
    const table = page.getByRole('table', { name: registry.name });
    await expect(table.locator('tbody tr')).toHaveCount(registry.options.length);
  }
  await expectAccessible(page);

  await page.getByRole('button', { name: /^Available/ }).click();
  await expect(page.getByRole('row', { name: /^Single node/ })).toContainText('DEL-001');
  await expect(page.getByRole('row', { name: /^NVIDIA/ })).toHaveCount(0);

  expect(refused).toEqual([]);
  expect(elsewhere).toEqual([]);
});
