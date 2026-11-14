// Reads a real installation in Chinese: the pages show no text outside the catalogs and the
// server's data, the audit trail names a real sign-in, and an invitation in Chinese arrives in
// Chinese. Leaves one open invitation. Skipped unless the environment describes an installation;
// docs/development/environment.md has the recipe.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { collectResponses, unexplainedText, type ResponseData } from '@ownsolid/shell/testing';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';
import { expect, test } from '@playwright/test';
import { execSync } from 'node:child_process';

const readMail = process.env.OWNSOLID_LIVE_MAIL_COMMAND ?? '';

test.skip(origin === '' || readMail === '', 'needs an installation and its mail');
test.use({ ...liveOptions, locale: 'zh-CN' });

const pages = [
  '/',
  '/nodes',
  '/tasks',
  '/users',
  '/users/workspaces',
  '/mail',
  '/audit',
  '/license',
  '/sso',
  '/settings',
  '/support',
  '/account',
];

/** A message with its base64 parts decoded, as the outbox sends them. */
function decoded(message: string): string {
  return message.replace(
    /Content-Transfer-Encoding: base64\r?\n(?:.+\r?\n)*\r?\n([A-Za-z0-9+/=\r\n]+)/g,
    (_, body: string) => `${Buffer.from(body.replace(/\s/g, ''), 'base64').toString('utf8')}\n`,
  );
}

function mailTo(address: string): string | undefined {
  const mailbox = execSync(readMail, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  // The files are joined with nothing between them, so a message starts wherever its headers do.
  return mailbox
    .split(/(?=From: [^\r\n]+\r?\nTo: )/)
    .find((message) => message.includes(`To: ${address}`));
}

test('reads in Chinese from the pages to the email', async ({ page }) => {
  test.setTimeout(180_000);
  const data: ResponseData = { whole: new Set(), words: new Set() };
  collectResponses(page, data);

  await page.goto('/');
  await page.getByLabel(chinese['signIn.email']).fill(email);
  await page.getByLabel(chinese['signIn.password']).fill(adminPassword());
  await page.getByRole('button', { name: chinese['signIn.submit'] }).click();
  await expect(page.getByRole('navigation').first()).toBeVisible();

  for (const path of pages) {
    await page.goto(path);
    await expect(page.locator('main h1').first()).toBeVisible();
    await page.waitForLoadState('networkidle');
    expect({ path, text: await unexplainedText(page, data) }).toEqual({ path, text: [] });
  }

  await page.goto('/audit');
  await expect(
    page.getByRole('row').filter({ hasText: chinese['audit.action.auth.login_succeeded'] }).first(),
  ).toBeVisible();

  const address = `live-zh-${String(Date.now())}@lane-b.test`;
  await page.goto('/users#invite');
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel(chinese['users.invite.email']).fill(address);
  await dialog.getByLabel(chinese['users.invite.language']).selectOption('zh-CN');
  await dialog.getByRole('button', { name: chinese['users.invite.submit'] }).click();
  await expect.poll(() => mailTo(address), { timeout: 60_000 }).toBeTruthy();
  const message = mailTo(address) ?? '';
  expect(message).toContain('Content-Language: zh-CN');
  expect(/^Subject: (.+)$/m.exec(message)?.[1]).toContain('=?UTF-8?');
  expect(decoded(message)).toMatch(/[\u4e00-\u9fff]{4}/);
});
