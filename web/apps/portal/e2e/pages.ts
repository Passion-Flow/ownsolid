import {
  deploymentAlias,
  deploymentKey,
  failedRun,
  publishedApplication,
  readyModel,
  recentConversation,
  runningDeployment,
  unavailableAlias,
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

async function openModels(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/models');
  await page.getByRole('heading', { name: 'Models', level: 1 }).waitFor();
}

function publishModels(control: ControlPlane): void {
  control.aliases = [deploymentAlias, unavailableAlias];
  control.deployments = [runningDeployment];
  control.models = [readyModel];
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
    name: 'home waiting for workspaces',
    path: '/',
    open: async (page, control) => {
      await page.route('**/api/v1/workspaces?*', () => {
        // Keep the request pending while the loading state is checked.
      });
      await openHome(page, control);
      await page
        .getByRole('main')
        .getByRole('status')
        .getByText('Loading', { exact: true })
        .waitFor();
    },
  },
  {
    name: 'home whose workspace list could not be read',
    path: '/',
    open: async (page, control) => {
      control.workspaceList = [];
      control.sessionWorkspace = null;
      control.workspacesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openHome(page, control);
      await page.getByRole('main').getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'models with one that cannot be called',
    path: '/models',
    open: async (page, control) => {
      publishModels(control);
      await openModels(page, control);
      await page.getByRole('row', { name: /support-chat/ }).waitFor();
      await page.getByText('Qwen3-0.6B').first().waitFor();
    },
  },
  {
    name: "a model's drawer",
    path: '/models',
    open: async (page, control) => {
      publishModels(control);
      await openModels(page, control);
      await page.getByRole('button', { name: 'Show archive-search' }).click();
      await page.getByRole('dialog').getByText('This model cannot be called now').waitFor();
    },
  },
  {
    name: 'models whose facts could not be read',
    path: '/models',
    open: async (page, control) => {
      publishModels(control);
      control.modelsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openModels(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'models, none yet',
    path: '/models',
    open: async (page, control) => {
      await openModels(page, control);
      await page.getByRole('heading', { name: 'No models yet' }).waitFor();
    },
  },
  {
    name: 'models without a workspace',
    path: '/models',
    open: async (page, control) => {
      control.workspaceList = [];
      control.sessionWorkspace = null;
      await openModels(page, control);
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
      await page.getByRole('tab', { name: 'cURL', exact: true }).waitFor();
      await page.getByText('No call has been made with this key.', { exact: true }).waitFor();
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
