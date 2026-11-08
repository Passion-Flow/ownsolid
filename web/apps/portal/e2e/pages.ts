import {
  deploymentAlias,
  deploymentKey,
  failedRun,
  publishedApplication,
  recentConversation,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';
import type { Page } from '@playwright/test';

async function openHome(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/');
  await page.getByRole('heading', { name: 'Welcome, Ada Lovelace', level: 1 }).waitFor();
}

async function openKeys(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/keys');
  await page.getByRole('heading', { name: 'My API keys', level: 1 }).waitFor();
}

/**
 * Every page state of the portal, checked by accessibility.spec.ts. A page this
 * application adds gets its states here; src/router.test.ts fails until it does.
 */
export const portalPages: readonly PageState[] = [
  ...shellPages,
  {
    name: 'home with runs, conversations and applications',
    path: '/',
    open: async (page, control) => {
      control.notifications = [{ ...failedRun }];
      control.applications = [publishedApplication];
      control.conversations = [recentConversation];
      await openHome(page, control);
      await page.getByRole('button', { name: /^Mark as read/ }).waitFor();
      await page.getByText('4 turns', { exact: false }).waitFor();
    },
  },
  {
    name: 'home with a section that could not read',
    path: '/',
    open: async (page, control) => {
      control.applicationsProblem = { status: 503, code: 'WORKFLOW_UNAVAILABLE' };
      await openHome(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'home without a workspace',
    path: '/',
    open: async (page, control) => {
      control.workspaceList = [];
      control.sessionWorkspace = null;
      await openHome(page, control);
      await page.getByRole('heading', { name: 'No workspace yet' }).waitFor();
    },
  },
  {
    name: 'keys with one key',
    path: '/keys',
    open: async (page, control) => {
      control.aliases = [deploymentAlias];
      control.gatewayKeys = [deploymentKey];
      await openKeys(page, control);
      await page.getByRole('row', { name: /Support bot/ }).waitFor();
    },
  },
  {
    name: "a key's drawer with its sample calls",
    path: '/keys',
    open: async (page, control) => {
      control.aliases = [deploymentAlias];
      control.gatewayKeys = [deploymentKey];
      await openKeys(page, control);
      await page.getByRole('button', { name: 'Show Support bot' }).click();
      await page.getByRole('heading', { name: 'Use this key' }).waitFor();
    },
  },
  {
    name: 'keys, none yet',
    path: '/keys',
    open: async (page, control) => {
      await openKeys(page, control);
      await page.getByRole('heading', { name: 'No keys yet' }).waitFor();
    },
  },
  {
    name: 'keys without a workspace',
    path: '/keys',
    open: async (page, control) => {
      control.workspaceList = [];
      control.sessionWorkspace = null;
      await openKeys(page, control);
      await page.getByRole('heading', { name: 'No workspace yet' }).waitFor();
    },
  },
];
