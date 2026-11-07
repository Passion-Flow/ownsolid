// Message catalogs are chunks of their own, so one can fail to load, usually because a release replaced the
// files under an open page.
import { mockControlPlane } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

test('a language that fails falls back to English', async ({ page }) => {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  await page.route(/assets\/zh-cn-.*\.js$/, (route) => route.abort());
  await page.addInitScript(() => {
    localStorage.setItem('ownsolid.locale', 'zh-CN');
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
});

test('with none at all, the page says so and a reload recovers', async ({ page }) => {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  let failing = true;
  await page.route(/assets\/en-.*\.js$/, (route) => (failing ? route.abort() : route.fallback()));
  await page.goto('/');
  await expect(page.getByRole('alert')).toContainText('could not load its text');
  failing = false;
  await page.getByRole('button', { name: 'Reload' }).click();
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
});

test('English readers do not download the Chinese catalog', async ({ page }) => {
  const control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  const asked: string[] = [];
  page.on('request', (request) => asked.push(request.url()));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /^Welcome/, level: 1 })).toBeVisible();
  expect(asked.some((url) => url.includes('assets/en-'))).toBe(true);
  expect(asked.some((url) => url.includes('assets/zh-cn-'))).toBe(false);
});

test.describe('in Chinese', () => {
  test.use({ locale: 'zh-CN' });

  test('Chinese readers do not download the English catalog', async ({ page }) => {
    const control = await mockControlPlane(page, 'admin');
    control.signedIn = true;
    const asked: string[] = [];
    page.on('request', (request) => asked.push(request.url()));
    await page.goto('/');
    await page.getByRole('main').waitFor();
    expect(asked.some((url) => url.includes('assets/zh-cn-'))).toBe(true);
    expect(asked.some((url) => url.includes('assets/en-'))).toBe(false);
  });
});
