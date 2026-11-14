// A session's idle timeout against a real installation, with the page's poll running the
// whole time. It waits out real timeouts, so it needs a short one configured and runs only
// when told how long it is; docs/development/environment.md has the recipe.
import { expect, test, type Page } from '@playwright/test';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

const idleSeconds = Number(process.env.OWNSOLID_LIVE_IDLE_SECONDS ?? '0');
const warningSeconds = Number(process.env.OWNSOLID_LIVE_WARNING_SECONDS ?? '0');
const title = 'You are about to be signed out';
// A little over the time left, for the page's next look and the tunnel.
const beyond = (seconds: number) => ({ timeout: (seconds + 15) * 1_000 });

async function signIn(page: Page): Promise<void> {
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(adminPassword());
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}

test.skip(origin === '' || idleSeconds === 0, 'needs OWNSOLID_LIVE_IDLE_SECONDS');
test.use(liveOptions);

test('an open page warns, keeps the session on request and ends it otherwise', async ({ page }) => {
  test.setTimeout((idleSeconds * 4 + 60) * 1_000);
  let polls = 0;
  page.on('request', (request) => {
    if (request.url().endsWith('/api/v1/notifications/configuration')) polls += 1;
  });
  await page.goto('/account');
  await signIn(page);
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();

  // Hands off, with the notification poll running: only the warning may happen.
  const dialog = page.getByRole('dialog', { name: title });
  await expect(dialog).toBeVisible(beyond(idleSeconds - warningSeconds));
  expect(polls).toBeGreaterThan(1);
  await expect(dialog.getByRole('timer')).toHaveText(/^\d+:\d\d$/);

  // One action keeps the session, and the server agrees.
  const stay = dialog.getByRole('button', { name: 'Stay signed in' });
  await expect(stay).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(dialog).toHaveCount(0);
  const left = await page.evaluate(async () => {
    const response = await fetch('/api/v1/me/session', {
      headers: { 'X-Ownsolid-Background': 'true' },
    });
    return ((await response.json()) as { data: { idle_remaining_seconds: number } }).data
      .idle_remaining_seconds;
  });
  expect(left).toBeGreaterThan(warningSeconds);

  // Working in the page without a single request of its own keeps the session too.
  const working = Date.now() + (idleSeconds + 5) * 1_000;
  for (let step = 0; Date.now() < working; step += 1) {
    await page.mouse.move(100 + (step % 2) * 40, 200);
    await page.waitForTimeout(5_000);
    await expect(dialog).toHaveCount(0);
  }

  // Left alone again, the warning returns and the session ends at the deadline. The last
  // input may still extend the session once, a window after it happened.
  await expect(dialog).toBeVisible(beyond(idleSeconds));
  await expect(page).toHaveURL(/\/sign-in\?redirect=/, beyond(warningSeconds));
  await expect(page.getByText('Your session has ended')).toBeVisible();
  await signIn(page);
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
});
