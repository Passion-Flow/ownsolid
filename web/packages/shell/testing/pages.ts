// Every page state the accessibility flows check, by route path. It imports Playwright
// only for types, so each application's unit tests can compare it with its router and
// fail when a route has no state here.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import type { Page } from '@playwright/test';
import {
  account,
  giteaBinding,
  giteaConnector,
  linkToken,
  type ControlPlane,
} from './control-plane.ts';

/** One state of one page: how to reach it, and the route path it belongs to. */
export interface PageState {
  /** Unique within a list; it names the test. */
  name: string;
  /** The route's full path as the router declares it. */
  path: string;
  /** Set when only one entrance has this state. */
  audience?: ControlPlane['audience'];
  /** Opens the state and returns once its content is on screen. */
  open: (page: Page, control: ControlPlane) => Promise<void>;
}

async function signedIn(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/');
  await page.getByRole('heading', { name: 'Welcome, Ada Lovelace' }).waitFor();
}

async function openAccount(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/account');
  await page.getByRole('heading', { name: 'Account', level: 1 }).waitFor();
}

/** The pages the shell defines, in every state worth checking on its own. */
export const shellPages: readonly PageState[] = [
  {
    name: 'sign-in',
    path: '/sign-in',
    open: async (page) => {
      await page.goto('/sign-in');
      await page.getByLabel('Email').waitFor();
    },
  },
  {
    name: 'sign-in with a failure and single sign-on',
    path: '/sign-in',
    open: async (page, control) => {
      control.ssoConnectors = [
        { id: '6f1c7a1e-0000-4000-8000-000000000008', name: 'Company Gitea', provider: 'gitea' },
      ];
      await page.goto('/sign-in');
      await page.getByRole('button', { name: 'Continue with Company Gitea' }).waitFor();
      await page.getByLabel('Email').fill(account.email);
      await page.getByLabel('Password').fill('wrong password');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'sign-in with missing fields',
    path: '/sign-in',
    open: async (page) => {
      await page.goto('/sign-in');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.getByText('Enter your email address.').waitFor();
    },
  },
  {
    name: 'sign-in in Chinese',
    path: '/sign-in',
    open: async (page) => {
      await page.goto('/sign-in');
      await page.getByLabel('Language').selectOption('zh-CN');
      await page.getByRole('heading', { name: chinese['signIn.title'] }).waitFor();
    },
  },
  {
    name: 'control plane unreachable',
    path: '/sign-in',
    open: async (page, control) => {
      control.unreachable = true;
      await page.goto('/sign-in');
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'forgot password',
    path: '/forgot-password',
    open: async (page) => {
      await page.goto('/forgot-password');
      await page.getByLabel('Email').waitFor();
    },
  },
  {
    name: 'forgot password sent',
    path: '/forgot-password',
    open: async (page) => {
      await page.goto('/forgot-password');
      await page.getByLabel('Email').fill(account.email);
      await page.getByRole('button', { name: 'Send the reset link' }).click();
      await page.getByText('If this address has an account').waitFor();
    },
  },
  {
    name: 'reset password',
    path: '/reset-password',
    open: async (page) => {
      await page.goto(`/reset-password#token=${linkToken}`);
      await page.getByLabel('New password').waitFor();
    },
  },
  {
    name: 'reset password with mismatched passwords',
    path: '/reset-password',
    open: async (page) => {
      await page.goto(`/reset-password#token=${linkToken}`);
      await page.getByLabel('New password').fill('a sufficiently long password');
      await page.getByLabel('Confirm the password').fill('a different password');
      await page.getByRole('button', { name: 'Set the new password' }).click();
      await page.getByText('The two passwords do not match.').waitFor();
    },
  },
  {
    name: 'reset link unusable',
    path: '/reset-password',
    open: async (page) => {
      await page.goto('/reset-password');
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'registration',
    path: '/register',
    audience: 'portal',
    open: async (page) => {
      await page.goto(`/register#token=${linkToken}`);
      await page.getByLabel('Your name').waitFor();
    },
  },
  {
    name: 'registration with missing fields',
    path: '/register',
    audience: 'portal',
    open: async (page) => {
      await page.goto(`/register#token=${linkToken}`);
      await page.getByRole('button', { name: 'Create the account' }).click();
      await page.getByText('Enter your name.').waitFor();
    },
  },
  {
    name: 'invitation unusable',
    path: '/register',
    audience: 'portal',
    open: async (page) => {
      await page.goto('/register');
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'no registration on the admin entrance',
    path: '/register',
    audience: 'admin',
    open: async (page) => {
      await page.goto(`/register#token=${linkToken}`);
      await page.getByText('Page not found').waitFor();
    },
  },
  {
    name: 'single sign-on failed',
    path: '/login/sso-result',
    open: async (page) => {
      await page.goto('/login/sso-result?error=SSO_PROVIDER_DENIED');
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'account linked',
    path: '/account/sso-result',
    open: async (page) => {
      await page.goto('/account/sso-result?result=linked');
      await page.getByRole('link').first().waitFor();
    },
  },
  {
    name: 'account with no providers',
    path: '/account',
    open: async (page, control) => {
      await openAccount(page, control);
      await page.getByText('No providers to link').waitFor();
    },
  },
  {
    name: 'account with linked and available providers',
    path: '/account',
    open: async (page, control) => {
      control.ssoBindings = [giteaBinding];
      control.ssoConnectors = [
        giteaConnector,
        { ...giteaConnector, id: '6f1c7a1e-0000-4000-8000-00000000000a', name: 'Lab Gitea' },
      ];
      await openAccount(page, control);
      await page.getByRole('button', { name: 'Unlink Company Gitea' }).waitFor();
      await page.getByRole('button', { name: 'Link Lab Gitea' }).waitFor();
    },
  },
  {
    name: 'account with providers unavailable',
    path: '/account',
    open: async (page, control) => {
      control.ssoBindingsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openAccount(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'account with a refused profile save',
    path: '/account',
    open: async (page, control) => {
      control.nextProfileSave = { status: 409, code: 'VERSION_CONFLICT' };
      await openAccount(page, control);
      await page.getByLabel('Display name').fill('Ada King');
      await page.getByRole('button', { name: 'Save' }).click();
      await page.getByRole('alert').waitFor();
    },
  },
  {
    name: 'account with a wrong current password',
    path: '/account',
    open: async (page, control) => {
      await openAccount(page, control);
      await page.getByLabel('Current password').fill('wrong password');
      await page.getByLabel('New password').fill('a sufficiently long password');
      await page.getByLabel('Confirm the password').fill('a sufficiently long password');
      await page.getByRole('button', { name: 'Change password' }).click();
      await page.getByText('The current password is not correct').waitFor();
    },
  },
  {
    name: 'account with empty password fields',
    path: '/account',
    open: async (page, control) => {
      await openAccount(page, control);
      await page.getByRole('button', { name: 'Change password' }).click();
      await page.getByText('Enter your current password.').waitFor();
    },
  },
  {
    name: 'account link dialog',
    path: '/account',
    open: async (page, control) => {
      control.ssoConnectors = [giteaConnector];
      await openAccount(page, control);
      await page.getByRole('button', { name: 'Link Company Gitea' }).click();
      await page.getByRole('dialog', { name: 'Link Company Gitea' }).waitFor();
    },
  },
  {
    name: 'account unlink dialog with a wrong password',
    path: '/account',
    open: async (page, control) => {
      control.ssoBindings = [giteaBinding];
      await openAccount(page, control);
      await page.getByRole('button', { name: 'Unlink Company Gitea' }).click();
      const dialog = page.getByRole('dialog', { name: 'Unlink Company Gitea' });
      await dialog.getByLabel('Current password').fill('wrong password');
      await dialog.getByRole('button', { name: 'Unlink' }).click();
      await dialog.getByText('The password is not correct').waitFor();
    },
  },
  { name: 'start page', path: '/', open: signedIn },
  {
    name: 'command menu',
    path: '/',
    open: async (page, control) => {
      await signedIn(page, control);
      await page.keyboard.press('ControlOrMeta+k');
      await page.getByPlaceholder('Type a command or a page name').waitFor();
    },
  },
  {
    name: 'notifications',
    path: '/',
    open: async (page, control) => {
      control.notifications = [
        {
          id: '6f1c7a1e-0000-4000-8000-000000000005',
          workspace_id: '6f1c7a1e-0000-4000-8000-000000000002',
          workflow_id: '6f1c7a1e-0000-4000-8000-000000000006',
          run_id: '6f1c7a1e-0000-4000-8000-000000000007',
          outcome: 'failed',
          error_code: 'MODEL_UNAVAILABLE',
          occurred_at: '2026-11-04T08:00:00Z',
          read_at: null,
        },
      ];
      await signedIn(page, control);
      await page.getByRole('button', { name: /^Notifications/ }).click();
      await page
        .getByRole('dialog', { name: 'Notifications' })
        .getByText('MODEL_UNAVAILABLE')
        .waitFor();
    },
  },
  {
    name: 'account menu',
    path: '/',
    open: async (page, control) => {
      await signedIn(page, control);
      await page.getByRole('button', { name: 'Account menu' }).click();
      await page.getByRole('menuitem', { name: 'Sign out' }).waitFor();
    },
  },
  {
    name: 'workspace menu',
    path: '/',
    open: async (page, control) => {
      await signedIn(page, control);
      await page.getByRole('button', { name: /^Workspace: / }).click();
      await page.getByRole('menuitem', { name: 'Platform Team' }).waitFor();
    },
  },
  {
    name: 'navigation drawer on a narrow screen',
    path: '/',
    open: async (page, control) => {
      await page.setViewportSize({ width: 600, height: 800 });
      await signedIn(page, control);
      await page.getByRole('button', { name: 'Open navigation' }).click();
      await page
        .getByRole('dialog', { name: 'Main navigation' })
        .getByRole('link')
        .first()
        .waitFor();
    },
  },
  {
    name: 'page not found',
    path: '*',
    open: async (page) => {
      await page.goto('/no/such/page');
      await page.getByText('Page not found').waitFor();
    },
  },
  {
    name: 'page failed to display',
    path: '*',
    open: async (page, control) => {
      control.signedIn = true;
      await page.route('**/assets/welcome-page-*.js', (route) =>
        route.fulfill({ status: 404, body: '' }),
      );
      await page.goto('/');
      await page.getByText('Something went wrong').waitFor();
    },
  },
];
