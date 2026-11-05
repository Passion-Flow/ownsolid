// Every page state the accessibility flows check, by route path. It imports Playwright
// only for types, so each application's unit tests can compare it with its router and
// fail when a route has no state here.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import type { Page } from '@playwright/test';
import { account, linkToken, type ControlPlane } from './control-plane.ts';

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
