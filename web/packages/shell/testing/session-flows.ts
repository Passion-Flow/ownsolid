// Browser flows every application must pass: sign-in, sign-out, failures, language
// and the offline guarantee. Each application's e2e/ directory runs them.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import { account, mockControlPlane, type ControlPlane } from './control-plane.ts';

/** The wording that differs between the applications. */
export interface SessionFlowOptions {
  audience: ControlPlane['audience'];
  subtitle: string;
}

async function signIn(page: Page, password = account.password): Promise<void> {
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
}

/** Registers the shared flows in the current spec file. */
export function sessionFlows({ audience, subtitle }: SessionFlowOptions): void {
  let control: ControlPlane;
  const failures: string[] = [];

  test.beforeEach(async ({ page, baseURL }) => {
    failures.length = 0;
    control = await mockControlPlane(page, audience);
    page.on('console', (message) => {
      if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
        failures.push(message.text());
      }
    });
    page.on('request', (request) => {
      if (!request.url().startsWith(baseURL ?? '') && !request.url().startsWith('data:')) {
        failures.push(`outside request: ${request.url()}`);
      }
    });
  });

  test.afterEach(() => {
    // Nothing may be blocked by the production CSP, and nothing may leave the origin.
    expect(failures).toEqual([]);
  });

  test('sends a visitor to sign-in and back to the requested page', async ({ page }) => {
    await page.goto('/?from=link');
    await expect(page).toHaveURL(/\/sign-in\?redirect=/);
    await expect(page.getByText(subtitle)).toBeVisible();
    await signIn(page);
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    await expect(page).toHaveURL(/\/\?from=link$/);
  });

  test('keeps the session across a reload and signs out', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    await page.reload();
    await expect(page.getByText('Research Lab')).toBeVisible();
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
    await page.goto('/');
    await expect(page).toHaveURL(/\/sign-in/);
  });

  test('skips sign-in when already signed in', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/sign-in');
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
  });

  test('asks for missing fields before calling the server', async ({ page }) => {
    await page.goto('/sign-in');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByText('Enter your email address.')).toBeVisible();
    await expect(page.getByText('Enter your password.')).toBeVisible();
    await expect(page.getByLabel('Email')).toHaveAttribute('aria-invalid', 'true');
  });

  test('explains wrong credentials and clears the password', async ({ page }) => {
    await page.goto('/sign-in');
    await signIn(page, 'wrong password');
    await expect(page.getByRole('alert')).toContainText('Email or password is incorrect');
    await expect(page.getByLabel('Password')).toHaveValue('');
    await expect(page.getByLabel('Password')).toBeFocused();
    await signIn(page);
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
  });

  test('shows the wait time when sign-in is rate limited', async ({ page }) => {
    await page.goto('/sign-in');
    control.nextSignIn = { status: 429, code: 'RATE_LIMITED', headers: { 'Retry-After': '30' } };
    await signIn(page);
    const alert = page.getByRole('alert');
    await expect(alert).toContainText('Too many requests');
    await expect(alert).toContainText('You can try again in 30 seconds.');
    await expect(alert).toContainText('Request ID');
  });

  test('recovers when the control plane comes back', async ({ page }) => {
    control.unreachable = true;
    await page.goto('/');
    await expect(page.getByRole('alert')).toContainText('The control plane cannot be reached');
    control.unreachable = false;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByLabel('Email')).toBeVisible();
  });

  test('stays signed in and offers a retry when sign-out fails', async ({ page }) => {
    control.signedIn = true;
    control.signOutProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/');
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page.getByRole('alert')).toContainText('The database is unavailable');
    control.signOutProblem = null;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  test('ignores a redirect to another site', async ({ page }) => {
    await page.goto('/sign-in?redirect=https://attacker.example/');
    await signIn(page);
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');
  });

  test('shows a page for unknown addresses', async ({ page }) => {
    await page.goto('/no/such/page');
    await expect(page.getByText('Page not found')).toBeVisible();
    await page.getByRole('link', { name: 'Go to the start page' }).click();
    await expect(page).toHaveURL(/\/sign-in/);
  });

  test('switches to Chinese and remembers the choice', async ({ page }) => {
    await page.goto('/sign-in');
    await page.getByLabel('Language').selectOption('zh-CN');
    await expect(page.getByRole('heading', { name: chinese['signIn.title'] })).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
    await page.reload();
    await expect(page.getByRole('heading', { name: chinese['signIn.title'] })).toBeVisible();
  });
}
