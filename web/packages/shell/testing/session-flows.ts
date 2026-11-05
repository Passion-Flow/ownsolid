// Browser flows every application must pass: sign-in, sign-out, failures, language
// and the offline guarantee. Each application's e2e/ directory runs them.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import {
  account,
  failedRun,
  linkToken,
  mockControlPlane,
  type ControlPlane,
} from './control-plane.ts';

/** The wording that differs between the applications. */
export interface SessionFlowOptions {
  audience: ControlPlane['audience'];
  subtitle: string;
}

async function signOut(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
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
  const allowOutside: string[] = [];

  test.beforeEach(async ({ page, baseURL }) => {
    failures.length = 0;
    allowOutside.length = 0;
    control = await mockControlPlane(page, audience);
    page.on('console', (message) => {
      if (message.type() === 'error' && message.text().includes('Content Security Policy')) {
        failures.push(message.text());
      }
    });
    page.on('request', (request) => {
      const url = request.url();
      const expected = [baseURL ?? '', 'data:', ...allowOutside];
      if (!expected.some((prefix) => url.startsWith(prefix))) {
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
    await expect(page.getByRole('main').getByText('Research Lab')).toBeVisible();
    await signOut(page);
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
    await expect(alert).toContainText('You can try again in 30s.');
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
    await signOut(page);
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

  test('remembers the theme and the density', async ({ page }) => {
    control.signedIn = true;
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('/');
    const root = page.locator('html');
    await expect(root).toHaveAttribute('data-theme', 'light');
    await page.getByRole('button', { name: 'Use the dark theme' }).click();
    await expect(root).toHaveAttribute('data-theme', 'dark');
    await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(22, 21, 19)');
    await page.getByRole('button', { name: 'Use compact spacing' }).click();
    await expect(root).toHaveAttribute('data-density', 'compact');
    await page.reload();
    await expect(root).toHaveAttribute('data-theme', 'dark');
    await expect(root).toHaveAttribute('data-density', 'compact');
    await expect(page.getByRole('button', { name: 'Use the light theme' })).toBeVisible();
  });

  test('applies what the account saved, over what this browser remembers', async ({ page }) => {
    control.signedIn = true;
    control.preferences = { theme: 'dark', density: 'compact', locale: 'auto', revision: 4 };
    await page.emulateMedia({ colorScheme: 'light' });
    await page.addInitScript(() => {
      localStorage.setItem('ownsolid.theme', 'light');
    });
    await page.goto('/');
    const root = page.locator('html');
    await expect(root).toHaveAttribute('data-theme', 'dark');
    await expect(root).toHaveAttribute('data-density', 'compact');
    expect(control.preferenceWrites).toEqual([]);
  });

  test('saves a change on the account and picks up one made elsewhere', async ({ page }) => {
    control.signedIn = true;
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('/');
    const root = page.locator('html');
    await page.getByRole('button', { name: 'Use the dark theme' }).click();
    await expect.poll(() => control.preferences.theme).toBe('dark');
    expect(control.preferenceWrites).toEqual([{ theme: 'dark', expected_revision: 1 }]);

    control.preferences = { theme: 'light', density: 'compact', locale: 'auto', revision: 9 };
    await page.evaluate(() => {
      window.dispatchEvent(new Event('visibilitychange'));
    });
    await expect(root).toHaveAttribute('data-theme', 'light');
    await expect(root).toHaveAttribute('data-density', 'compact');
  });

  test('keeps the latest choice when another browser saved first', async ({ page }) => {
    control.signedIn = true;
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('/');
    await expect(page.getByRole('button', { name: 'Use compact spacing' })).toBeVisible();
    control.preferences = { theme: 'system', density: 'comfortable', locale: 'auto', revision: 7 };
    await page.getByRole('button', { name: 'Use the dark theme' }).click();
    await expect.poll(() => control.preferences.theme).toBe('dark');
    expect(control.preferences.revision).toBe(8);
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  });

  test('says when a change was not saved and saves it on retry', async ({ page }) => {
    control.signedIn = true;
    control.preferencesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('/');
    await page.getByRole('button', { name: 'Use the dark theme' }).click();
    await expect(page.getByText('was not saved to your account')).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    control.preferencesProblem = null;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect.poll(() => control.preferences.theme).toBe('dark');
  });

  test('follows the system theme until the user chooses', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.goto('/sign-in');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await page.emulateMedia({ colorScheme: 'light' });
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  });

  test('loads the bundled fonts from its own origin', async ({ page }) => {
    await page.goto('/sign-in');
    await page.getByLabel('Email').waitFor();
    const loaded = await page.evaluate(async () => {
      await document.fonts.ready;
      return [...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family);
    });
    expect(loaded.join()).toContain('Figtree Variable');
    await expect(page.locator('body')).toHaveCSS('font-family', /^"Figtree Variable"/);
  });

  test('loads the signed-in frame only after sign-in', async ({ page }) => {
    const scripts: string[] = [];
    page.on('request', (request) => {
      if (request.resourceType() === 'script') scripts.push(new URL(request.url()).pathname);
    });
    await page.goto('/sign-in');
    await page.getByLabel('Email').waitFor();
    expect(scripts.some((path) => path.includes('/sign-in-page-'))).toBe(true);
    expect(scripts.filter((path) => path.includes('/app-frame-'))).toEqual([]);
    await signIn(page);
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    expect(scripts.some((path) => path.includes('/app-frame-'))).toBe(true);
  });

  test('reloads once when a newer deployment replaced the code of a page', async ({ page }) => {
    control.signedIn = true;
    let requests = 0;
    // The first request finds the file gone, as after an upgrade; the reload gets it.
    await page.route('**/assets/welcome-page-*.js', async (route) => {
      requests += 1;
      if (requests === 1) await route.fulfill({ status: 404, body: '' });
      else await route.fallback();
    });
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    expect(requests).toBe(2);
  });

  test('offers a retry when the code of a page keeps failing to load', async ({ page }) => {
    control.signedIn = true;
    let available = false;
    await page.route('**/assets/welcome-page-*.js', async (route) => {
      if (available) await route.fallback();
      else await route.fulfill({ status: 404, body: '' });
    });
    await page.goto('/');
    await expect(page.getByText('Something went wrong')).toBeVisible();
    available = true;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
  });

  test('opens the command menu from the keyboard and runs an action', async ({ page }) => {
    control.signedIn = true;
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto('/');
    await page.getByRole('main').waitFor();
    await page.keyboard.press('ControlOrMeta+k');
    const input = page.getByPlaceholder('Type a command or a page name');
    await expect(input).toBeFocused();
    await input.fill('dark');
    await page.keyboard.press('Enter');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(input).toBeHidden();
  });

  test('closes the command menu with Escape and says when nothing matches', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await page.getByRole('button', { name: 'Search, or type a command' }).click();
    const input = page.getByPlaceholder('Type a command or a page name');
    await input.fill('no such thing');
    await expect(page.getByText('No results')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(input).toBeHidden();
    await expect(page.getByRole('button', { name: 'Search, or type a command' })).toBeFocused();
  });

  test('signs out from the command menu', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await page.getByRole('main').waitFor();
    await page.keyboard.press('ControlOrMeta+k');
    await page.getByPlaceholder('Type a command or a page name').fill('sign out');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  test('switches workspace and remembers it', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await page.getByRole('button', { name: /^Workspace: Research Lab/ }).click();
    await page.getByRole('menuitem', { name: 'Platform Team' }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Switched to' })).toContainText(
      'Switched to Platform Team',
    );
    await page.reload();
    await expect(page.getByRole('button', { name: /^Workspace: Platform Team/ })).toBeVisible();
  });

  test('offers a retry when workspaces do not load', async ({ page }) => {
    control.signedIn = true;
    control.workspacesProblem = { status: 403, code: 'ACCESS_DENIED' };
    await page.goto('/');
    await page.getByRole('button', { name: /^Workspace: Research Lab/ }).click();
    const retry = page.getByRole('menuitem', { name: /did not load/ });
    await expect(retry).toBeVisible();
    control.workspacesProblem = null;
    await retry.click();
    await expect(page.getByRole('menuitem', { name: 'Platform Team' })).toBeVisible();
  });

  test('lists notifications and marks them read', async ({ page }) => {
    control.signedIn = true;
    control.notifications = [{ ...failedRun }];
    await page.goto('/');
    await page.getByRole('button', { name: 'Notifications: 1 unread' }).click();
    const drawer = page.getByRole('dialog', { name: 'Notifications' });
    await expect(drawer).toContainText('Workflow run failed');
    await expect(drawer).toContainText('MODEL_UNAVAILABLE');
    await drawer.getByRole('button', { name: 'Mark as read' }).click();
    await expect(drawer.getByRole('button', { name: 'Mark as read' })).toBeHidden();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('button', { name: 'Notifications: 0 unread' })).toBeVisible();
  });

  test('explains an empty and a failed notification list', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await page.getByRole('button', { name: /^Notifications/ }).click();
    const drawer = page.getByRole('dialog', { name: 'Notifications' });
    await expect(drawer).toContainText('No notifications');
    await page.keyboard.press('Escape');
    control.notificationsProblem = { status: 403, code: 'ACCESS_DENIED' };
    await page.reload();
    await page.getByRole('button', { name: /^Notifications/ }).click();
    await expect(page.getByRole('alert')).toContainText('You do not have access');
  });

  test('uses a drawer for navigation on a narrow screen', async ({ page }) => {
    control.signedIn = true;
    await page.setViewportSize({ width: 600, height: 800 });
    await page.goto('/');
    await page.getByRole('button', { name: 'Open navigation' }).click();
    const panel = page.getByRole('dialog', { name: 'Main navigation' });
    await expect(panel.getByRole('link').first()).toBeVisible();
    await panel.getByRole('link').first().click();
    await expect(panel).toBeHidden();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(overflow).toBe(false);
  });

  test('switches language from the account menu', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/');
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: chinese['locale.zh-CN'] }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
    await expect(page.getByRole('button', { name: chinese['user.menu'] })).toBeVisible();
  });

  test('asks for a reset link without revealing whether the account exists', async ({ page }) => {
    await page.goto('/sign-in');
    await page.getByRole('link', { name: 'Forgot your password?' }).click();
    await page.getByRole('button', { name: 'Send the reset link' }).click();
    await expect(page.getByText('Enter your email address.')).toBeVisible();
    await page.getByLabel('Email').fill(account.email);
    await page.getByRole('button', { name: 'Send the reset link' }).click();
    await expect(page.getByText('If this address has an account')).toBeVisible();
    await page.getByRole('link', { name: 'Back to sign-in' }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  test('sets a new password from an emailed link', async ({ page }) => {
    await page.goto(`/reset-password#token=${linkToken}`);
    await expect(page.getByLabel('New password')).toBeVisible();
    // The token must not stay in the address or the history.
    await expect.poll(() => page.url()).not.toContain('token');
    await page.getByLabel('New password').fill('a sufficiently long password');
    await page.getByLabel('Confirm the password').fill('a different password');
    await page.getByRole('button', { name: 'Set the new password' }).click();
    await expect(page.getByText('The two passwords do not match.')).toBeVisible();
    control.nextAccountWrite = { status: 422, code: 'PASSWORD_BLOCKED' };
    await page.getByLabel('Confirm the password').fill('a sufficiently long password');
    await page.getByRole('button', { name: 'Set the new password' }).click();
    await expect(page.getByRole('alert')).toContainText('This password is too common');
    await page.getByRole('button', { name: 'Set the new password' }).click();
    await expect(page.getByText('Your password was changed')).toBeVisible();
    expect(control.lastPassword).toBe('a sufficiently long password');
  });

  test('explains a reset link that cannot be used', async ({ page }) => {
    await page.goto('/reset-password');
    await expect(page.getByRole('alert')).toContainText('This reset link cannot be used');
    await page.goto(`/reset-password#token=${'b'.repeat(43)}`);
    await page.getByLabel('New password').fill('a sufficiently long password');
    await page.getByLabel('Confirm the password').fill('a sufficiently long password');
    await page.getByRole('button', { name: 'Set the new password' }).click();
    await expect(page.getByRole('alert')).toContainText('This reset link cannot be used');
    await page.getByRole('link', { name: 'Request a new link' }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
  });

  test('offers single sign-on and sends the browser to the provider', async ({ page }) => {
    control.ssoConnectors = [
      { id: '6f1c7a1e-0000-4000-8000-000000000008', name: 'Company Gitea', provider: 'gitea' },
    ];
    await page.route('**/idp.example/**', (route) =>
      route.fulfill({ contentType: 'text/html', body: 'provider' }),
    );
    control.ssoAddress = 'https://idp.example/authorize';
    allowOutside.push('https://idp.example/');
    await page.goto('/sign-in');
    await page.getByRole('button', { name: 'Continue with Company Gitea' }).click();
    await expect(page).toHaveURL('https://idp.example/authorize');
  });

  test('finishes single sign-on at the start page', async ({ page }) => {
    control.signedIn = true;
    await page.goto('/login/sso-result?result=authenticated');
    await expect(page.getByRole('heading', { name: 'Welcome, Ada Lovelace' })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');
  });

  test('explains a failed single sign-on', async ({ page }) => {
    await page.goto('/login/sso-result?error=SSO_PROVIDER_DENIED');
    await expect(page.getByRole('alert')).toContainText('The provider refused the sign-in');
    await page.getByRole('link', { name: 'Back to sign-in' }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
  });

  test('never shows text from the address on the result page', async ({ page }) => {
    await page.goto('/login/sso-result?error=%3Cb%3Einjected%3C%2Fb%3E');
    await expect(page.getByRole('alert')).toContainText('Single sign-on could not finish');
    await expect(page.getByRole('alert')).not.toContainText('injected');
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

/** The invitation flows: the portal creates accounts, and the console has no such page. */
export function registrationFlows(audience: ControlPlane['audience']): void {
  test.describe('registration', () => {
    test.beforeEach(async ({ page }) => {
      await mockControlPlane(page, audience);
    });

    if (audience === 'admin') {
      test('has no registration page', async ({ page }) => {
        await page.goto(`/register#token=${linkToken}`);
        await expect(page.getByText('Page not found')).toBeVisible();
      });
      return;
    }

    test('creates an account from an invitation', async ({ page }) => {
      await page.goto(`/register#token=${linkToken}`);
      await expect(page.getByLabel('Email')).toHaveValue(account.email);
      await expect(page.getByLabel('Email')).not.toBeEditable();
      await expect.poll(() => page.url()).not.toContain('token');
      await page.getByRole('button', { name: 'Create the account' }).click();
      await expect(page.getByText('Enter your name.')).toBeVisible();
      await page.getByLabel('Your name').fill('Ada Lovelace');
      await page.getByLabel('New password').fill('a sufficiently long password');
      await page.getByLabel('Confirm the password').fill('a sufficiently long password');
      await page.getByRole('button', { name: 'Create the account' }).click();
      await expect(page.getByText('Your account is ready')).toBeVisible();
      await page.getByRole('link', { name: 'Sign in' }).click();
      await expect(page).toHaveURL(/\/sign-in$/);
    });

    test('explains an invitation that cannot be used', async ({ page }) => {
      await page.goto(`/register#token=${'b'.repeat(43)}`);
      await expect(page.getByRole('alert')).toContainText('This invitation cannot be used');
      await page.goto('/register');
      await expect(page.getByRole('alert')).toContainText('This invitation cannot be used');
    });
  });
}
