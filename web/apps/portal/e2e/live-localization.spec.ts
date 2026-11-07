// Reads the portal of a real installation in Chinese: the pages show no text outside the catalogs
// and the server's data. Skipped unless the environment describes an installation;
// docs/development/environment.md has the recipe.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { collectResponses, unexplainedText, type ResponseData } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test } from '@playwright/test';

test.skip(origin === '', 'needs an installation');
test.use({ ...liveOptions, locale: 'zh-CN' });

test('reads in Chinese', async ({ page }) => {
  test.setTimeout(120_000);
  const data: ResponseData = { whole: new Set(), words: new Set() };
  collectResponses(page, data);

  await page.goto('/');
  await page.getByLabel(chinese['signIn.email']).fill(email);
  await page.getByLabel(chinese['signIn.password']).fill(adminPassword());
  await page.getByRole('button', { name: chinese['signIn.submit'] }).click();
  await expect(page.getByRole('navigation').first()).toBeVisible();

  for (const path of ['/', '/account', '/support']) {
    await page.goto(path);
    await expect(page.locator('main h1').first()).toBeVisible();
    await page.waitForLoadState('networkidle');
    expect({ path, text: await unexplainedText(page, data) }).toEqual({ path, text: [] });
  }
});
