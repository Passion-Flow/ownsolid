import {
  activeConnector,
  activeLicense,
  activeSummary,
  adminUser,
  checksStoppingAt,
  failedMail,
  disabledNode,
  failedSignIn,
  failedTask,
  finishedTask,
  importableInspection,
  joinToken,
  licenseEvent,
  memberUser,
  missingLicense,
  node,
  offlineNode,
  pendingInvitation,
  retiredConnector,
  runningTask,
  secondFailedMail,
  sentMail,
  ssoOn,
  systemEvent,
  teamWorkspace,
  type ControlPlane,
  type ScriptedView,
} from '@ownsolid/shell/testing';
import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';
import type { Page } from '@playwright/test';

/** A saved view that shows only failed emails. */
export const failedView: ScriptedView = {
  id: '6f1c7a1e-0000-4000-8000-000000000020',
  list: 'mail-deliveries',
  name: 'Failures',
  state: { filters: { state: 'failed' }, hiddenColumns: [] },
  revision: 1,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

async function openMail(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/mail');
  await page.getByRole('heading', { name: 'Email delivery', level: 1 }).waitFor();
}

async function openTasks(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/tasks');
  await page.getByRole('heading', { name: 'Tasks', level: 1 }).waitFor();
}

async function openTask(page: Page, control: ControlPlane, id: string): Promise<void> {
  control.signedIn = true;
  await page.goto(`/tasks/${id}`);
  await page.getByRole('log').waitFor();
}

async function openNodes(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/nodes');
  await page.getByRole('heading', { name: 'Nodes', level: 1 }).waitFor();
}

async function openOverview(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/');
  await page.getByRole('heading', { name: 'Welcome, Ada Lovelace', level: 1 }).waitFor();
}

async function openAudit(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/audit');
  await page.getByRole('heading', { name: 'Audit trail', level: 1 }).waitFor();
}

async function openLicense(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/license');
  await page.getByRole('heading', { name: 'License', level: 1 }).waitFor();
}

async function chooseLicenseFile(page: Page): Promise<void> {
  await page.getByTestId('license-file').setInputFiles({
    name: 'license.json',
    mimeType: 'application/json',
    buffer: Buffer.from('{"payload":"..."}'),
  });
}

async function openSettings(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/settings');
  await page.getByRole('heading', { name: 'Settings', level: 1 }).waitFor();
}

/**
 * Every page state of the console, checked by accessibility.spec.ts. A page this
 * application adds gets its states here; src/router.test.ts fails until it does.
 */
export const consolePages: readonly PageState[] = [
  ...shellPages,
  {
    name: 'overview of a new installation',
    path: '/',
    open: async (page, control) => {
      control.license = missingLicense;
      control.nodes = [];
      await openOverview(page, control);
      await page.getByRole('heading', { name: 'Finish setup' }).waitFor();
    },
  },
  {
    name: 'overview with items that need attention',
    path: '/',
    open: async (page, control) => {
      control.nodes = [node, offlineNode, disabledNode];
      control.tasks = [
        { ...failedTask, task: { ...failedTask.task, completed_at: new Date().toISOString() } },
      ];
      control.mail = [{ ...failedMail, completed_at: new Date().toISOString() }];
      await openOverview(page, control);
      await page.getByText('gpu-a03 last reported').waitFor();
      await page.getByText('1 email failed').waitFor();
    },
  },
  {
    name: 'overview with nothing to do',
    path: '/',
    open: async (page, control) => {
      control.nodes = [node];
      control.users = [adminUser, memberUser];
      await openOverview(page, control);
      await page.getByRole('heading', { name: 'Nothing needs you' }).waitFor();
    },
  },
  {
    name: 'overview that could not load',
    path: '/',
    open: async (page, control) => {
      control.tasksProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openOverview(page, control);
      await page.getByRole('button', { name: 'Try again' }).first().waitFor();
    },
  },
  {
    name: 'a node on its own page',
    path: '/nodes/$nodeId',
    open: async (page, control) => {
      control.signedIn = true;
      control.nodes = [node];
      await page.goto(`/nodes/${node.id}`);
      await page.getByRole('heading', { name: node.name, level: 1 }).waitFor();
    },
  },
  {
    name: 'a node with its tasks',
    path: '/nodes/$nodeId',
    open: async (page, control) => {
      control.signedIn = true;
      control.nodes = [node];
      control.tasks = [finishedTask, failedTask];
      await page.goto(`/nodes/${node.id}?tab=tasks`);
      await page.getByRole('tabpanel').getByRole('link').first().waitFor();
    },
  },
  {
    name: 'a node with its activity',
    path: '/nodes/$nodeId',
    open: async (page, control) => {
      control.signedIn = true;
      control.nodes = [node];
      control.auditEvents = [{ ...systemEvent, occurred_at: node.created_at, target_id: node.id }];
      await page.goto(`/nodes/${node.id}?tab=activity`);
      await page.getByRole('tabpanel').getByRole('table').waitFor();
    },
  },
  {
    name: 'a connector on its own page',
    path: '/sso/$connectorId',
    open: async (page, control) => {
      control.signedIn = true;
      control.ssoConfiguration = { ...ssoOn };
      control.ssoConnectorList = [{ ...activeConnector }];
      await page.goto(`/sso/${activeConnector.id}`);
      await page.getByRole('heading', { name: activeConnector.name, level: 1 }).waitFor();
    },
  },
  {
    name: 'a person on their own page',
    path: '/users/$userId',
    open: async (page, control) => {
      control.signedIn = true;
      control.users = [adminUser, memberUser];
      await page.goto(`/users/${memberUser.id}`);
      await page.getByRole('heading', { name: 'Grace Hopper', level: 1 }).waitFor();
    },
  },
  {
    name: 'nodes with none yet',
    path: '/nodes',
    open: async (page, control) => {
      control.nodes = [];
      await openNodes(page, control);
      await page.getByText('No nodes yet').waitFor();
    },
  },
  {
    name: 'nodes with rows and a token waiting',
    path: '/nodes',
    open: async (page, control) => {
      control.nodes = [node, offlineNode, disabledNode];
      control.joinTokens = [joinToken];
      await openNodes(page, control);
      await page.getByRole('row', { name: /gpu-a03/ }).waitFor();
    },
  },
  {
    name: 'nodes with several selected',
    path: '/nodes',
    open: async (page, control) => {
      control.nodes = [node, offlineNode];
      await openNodes(page, control);
      await page.getByRole('checkbox', { name: 'Select all' }).check();
      await page.getByRole('toolbar').waitFor();
    },
  },
  {
    name: 'nodes that could not load',
    path: '/nodes',
    open: async (page, control) => {
      control.nodesProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openNodes(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'a node that went quiet',
    path: '/nodes',
    open: async (page, control) => {
      control.nodes = [offlineNode];
      await openNodes(page, control);
      await page.getByRole('button', { name: 'Open details of gpu-a03' }).click();
      await page.getByRole('dialog').getByText('Last heard from').waitFor();
    },
  },
  {
    name: 'users with an invitation waiting',
    path: '/users',
    open: async (page, control) => {
      control.signedIn = true;
      control.users = [memberUser, adminUser];
      control.invitations = [
        pendingInvitation,
        {
          ...pendingInvitation,
          id: '6f1c7a1e-0000-4000-8000-000000000082',
          email: 'kay@example.test',
          status: 'expired',
        },
      ];
      await page.goto('/users');
      await page.getByRole('region', { name: 'Invitations' }).waitFor();
    },
  },
  {
    name: 'users matching nothing',
    path: '/users',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/users?status=disabled');
      await page.getByText('No accounts match').waitFor();
    },
  },
  {
    name: 'users that could not load',
    path: '/users',
    open: async (page, control) => {
      control.signedIn = true;
      control.usersProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await page.goto('/users');
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'an account and disabling it',
    path: '/users',
    open: async (page, control) => {
      control.signedIn = true;
      control.users = [memberUser, adminUser];
      await page.goto('/users');
      await page.getByRole('button', { name: 'Open details of Grace Hopper' }).click();
      await page.getByRole('dialog').getByRole('button', { name: 'Disable' }).click();
      await page.getByRole('dialog', { name: 'Disable grace@example.test' }).waitFor();
    },
  },
  {
    name: 'inviting someone',
    path: '/users',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/users');
      await page.getByRole('button', { name: 'Invite' }).click();
      await page.getByRole('dialog', { name: 'Invite someone' }).waitFor();
    },
  },
  {
    name: 'agent pacing',
    path: '/nodes/pacing',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/nodes/pacing');
      await page.getByRole('heading', { name: 'Agent pacing', level: 1 }).waitFor();
      await page.getByLabel('Heartbeat interval (seconds)').waitFor();
    },
  },
  {
    name: 'agent pacing too slow for the window',
    path: '/nodes/pacing',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/nodes/pacing');
      await page.getByLabel('Heartbeat interval (seconds)').fill('60');
      await page.getByText('the interval can be at most').waitFor();
    },
  },
  {
    name: 'agent pacing that could not load',
    path: '/nodes/pacing',
    open: async (page, control) => {
      control.signedIn = true;
      control.pacingProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await page.goto('/nodes/pacing');
      await page.getByRole('button', { name: 'Try again' }).first().waitFor();
    },
  },
  {
    name: 'adding a node',
    path: '/nodes',
    open: async (page, control) => {
      await openNodes(page, control);
      await page.getByRole('button', { name: 'Add node' }).click();
      await page.getByRole('dialog', { name: 'Add a node' }).waitFor();
    },
  },
  {
    name: 'the steps that join a node',
    path: '/nodes',
    open: async (page, control) => {
      await openNodes(page, control);
      await page.getByRole('button', { name: 'Add node' }).click();
      await page.getByLabel('Node name').fill('edge-e02');
      await page.getByRole('button', { name: 'Continue' }).click();
      await page.getByRole('dialog', { name: 'Join edge-e02' }).waitFor();
    },
  },
  {
    name: 'tasks with none yet',
    path: '/tasks',
    open: async (page, control) => {
      await openTasks(page, control);
      await page.getByText('No tasks yet').waitFor();
    },
  },
  {
    name: 'tasks with rows',
    path: '/tasks',
    open: async (page, control) => {
      control.tasks = [failedTask, finishedTask, runningTask];
      await openTasks(page, control);
      await page.getByRole('link', { name: 'Collect node facts' }).first().waitFor();
    },
  },
  {
    name: 'tasks unavailable',
    path: '/tasks',
    open: async (page, control) => {
      control.tasksProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openTasks(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'starting a task',
    path: '/tasks',
    open: async (page, control) => {
      await openTasks(page, control);
      await page.getByRole('button', { name: 'Collect node facts' }).click();
      await page.getByRole('combobox', { name: 'Node' }).waitFor();
    },
  },
  {
    name: 'a running task',
    path: '/tasks/$taskId',
    open: async (page, control) => {
      control.tasks = [runningTask];
      await openTask(page, control, runningTask.task.id);
      await page.getByRole('button', { name: 'Pause' }).waitFor();
    },
  },
  {
    name: 'a finished task',
    path: '/tasks/$taskId',
    open: async (page, control) => {
      control.tasks = [finishedTask];
      await openTask(page, control, finishedTask.task.id);
      await page.getByText('Memory: 33554432000 bytes').waitFor();
    },
  },
  {
    name: 'a failed task',
    path: '/tasks/$taskId',
    open: async (page, control) => {
      control.tasks = [failedTask];
      await openTask(page, control, failedTask.task.id);
      await page.getByRole('button', { name: 'Run again' }).waitFor();
    },
  },
  {
    name: 'a task that cannot be found',
    path: '/tasks/$taskId',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto(`/tasks/${runningTask.task.id}`);
      await page.getByRole('link', { name: 'All tasks' }).waitFor();
    },
  },
  {
    name: 'settings',
    path: '/settings',
    open: async (page, control) => {
      await openSettings(page, control);
      await page.getByLabel('Default language').waitFor();
    },
  },
  {
    name: 'settings unavailable',
    path: '/settings',
    open: async (page, control) => {
      control.tenantSettingsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openSettings(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'email delivery with a failed email',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: /^Send again to/ }).waitFor();
    },
  },
  {
    name: 'email delivery with rows selected',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, secondFailedMail, sentMail];
      await openMail(page, control);
      await page.getByRole('checkbox', { name: 'Select grace@example.test' }).check();
      await page.getByRole('toolbar', { name: 'Actions for the selected rows' }).waitFor();
    },
  },
  {
    name: 'email delivery details',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail];
      await openMail(page, control);
      await page.getByRole('button', { name: /^Show details of the email to / }).click();
      await page.getByRole('dialog', { name: failedMail.recipient_email }).waitFor();
    },
  },
  {
    name: 'email delivery columns menu',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Choose the columns to show' }).click();
      await page.getByRole('menuitemcheckbox', { name: 'Attempts' }).waitFor();
    },
  },
  {
    name: 'email delivery views menu with a changed view',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, sentMail];
      control.savedViews = [failedView];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Views: Default view' }).click();
      await page.getByRole('menuitem', { name: 'Failures' }).click();
      await page
        .getByRole('group', { name: 'Show emails' })
        .getByRole('button', { name: 'All' })
        .click();
      await page.getByRole('button', { name: /^Views: Failures, Unsaved/ }).click();
      await page.getByRole('menuitem', { name: 'Save the changes' }).waitFor();
    },
  },
  {
    name: 'email delivery save view dialog',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Views: Default view' }).click();
      await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
      await page.getByRole('dialog', { name: 'Save this view' }).waitFor();
    },
  },
  {
    name: 'email delivery from a link, oldest first, with a drawer',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, secondFailedMail, sentMail];
      control.signedIn = true;
      await page.goto(`/mail?order=oldest&open=${failedMail.id}`);
      await page.getByRole('dialog', { name: failedMail.recipient_email }).waitFor();
    },
  },
  {
    name: 'email delivery revoking an invitation',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail];
      await openMail(page, control);
      await page.getByRole('button', { name: /^Show details of the email to / }).click();
      await page.getByRole('button', { name: 'Revoke the invitation' }).click();
      await page.getByRole('dialog', { name: 'Revoke this invitation' }).waitFor();
    },
  },
  {
    name: 'email delivery with no matches',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByLabel('Filter by recipient').fill('nobody');
      await page.getByText('No emails match').waitFor();
    },
  },
  {
    name: 'email delivery empty',
    path: '/mail',
    open: async (page, control) => {
      await openMail(page, control);
      await page.getByText('No emails yet').waitFor();
    },
  },
  {
    name: 'email delivery unavailable',
    path: '/mail',
    open: async (page, control) => {
      control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openMail(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'workspaces listed',
    path: '/users/workspaces',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/users/workspaces');
      await page.getByRole('heading', { name: 'Workspaces', level: 1 }).waitFor();
      await page.getByRole('link', { name: teamWorkspace.name }).waitFor();
    },
  },
  {
    name: 'workspaces creating one',
    path: '/users/workspaces',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/users/workspaces#create');
      await page.getByRole('dialog', { name: 'Create a workspace' }).waitFor();
    },
  },
  {
    name: 'workspace with members',
    path: '/users/workspaces/$workspaceId',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto(`/users/workspaces/${teamWorkspace.id}`);
      await page.getByText('grace@example.test').first().waitFor();
    },
  },
  {
    name: 'workspace removing a member',
    path: '/users/workspaces/$workspaceId',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto(`/users/workspaces/${teamWorkspace.id}`);
      await page.getByRole('button', { name: 'Remove grace@example.test' }).click();
      await page.getByRole('dialog', { name: 'Remove grace@example.test' }).waitFor();
    },
  },
  {
    name: 'workspace that does not exist',
    path: '/users/workspaces/$workspaceId',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/users/workspaces/6f1c7a1e-0000-4000-8000-0000000000ee');
      await page.getByRole('heading', { name: 'Not found' }).waitFor();
    },
  },
  {
    name: 'keyboard shortcuts listed',
    path: '/',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/');
      await page.getByRole('heading', { name: /^Welcome/, level: 1 }).waitFor();
      await page.keyboard.press('?');
      await page.getByRole('dialog', { name: 'Keyboard shortcuts' }).waitFor();
    },
  },
  {
    name: 'single sign-on listed',
    path: '/sso',
    open: async (page, control) => {
      control.signedIn = true;
      control.ssoConnectorList = [{ ...retiredConnector }, { ...activeConnector }];
      await page.goto('/sso');
      await page.getByRole('row', { name: /Company Gitea/ }).waitFor();
    },
  },
  {
    name: 'single sign-on adding a connector',
    path: '/sso',
    open: async (page, control) => {
      control.signedIn = true;
      await page.goto('/sso#add');
      await page.getByRole('dialog', { name: 'Add a connector' }).waitFor();
    },
  },
  {
    name: 'single sign-on connector with a failed check',
    path: '/sso',
    open: async (page, control) => {
      control.signedIn = true;
      control.ssoConnectorList = [{ ...activeConnector }];
      control.ssoChecks[activeConnector.id] = 'callback_not_registered';
      await page.goto('/sso');
      await page.getByRole('button', { name: 'Open Company Gitea' }).click();
      await page.getByRole('button', { name: 'Test connection' }).click();
      await page.getByText('Gitea does not accept a callback URL').waitFor();
    },
  },
  {
    name: 'single sign-on turning a connector off',
    path: '/sso',
    open: async (page, control) => {
      control.signedIn = true;
      control.ssoConnectorList = [{ ...activeConnector }];
      await page.goto(`/sso?open=${activeConnector.id}`);
      await page.getByRole('button', { name: 'Turn off' }).click();
      await page.getByRole('dialog', { name: 'Turn off Company Gitea' }).waitFor();
    },
  },
  {
    name: 'single sign-on off for the installation',
    path: '/sso',
    open: async (page, control) => {
      control.signedIn = true;
      control.ssoConfiguration = { ...ssoOn, enabled: false };
      await page.goto('/sso');
      await page.getByText('Single sign-on is off for this installation').waitFor();
    },
  },
  {
    name: 'audit trail with events',
    path: '/audit',
    open: async (page, control) => {
      control.auditEvents = [licenseEvent, failedSignIn, systemEvent];
      await openAudit(page, control);
      await page.getByText('license.imported').waitFor();
    },
  },
  {
    name: 'audit event details',
    path: '/audit',
    open: async (page, control) => {
      control.auditEvents = [licenseEvent];
      await openAudit(page, control);
      await page.getByRole('button', { name: /^Show details of license.imported/ }).click();
      await page.getByRole('dialog', { name: 'license.imported' }).waitFor();
    },
  },
  {
    name: 'audit trail with nothing recorded',
    path: '/audit',
    open: async (page, control) => {
      await openAudit(page, control);
      await page.getByText('Nothing recorded in the last 7 days').waitFor();
    },
  },
  {
    name: 'audit trail that could not load',
    path: '/audit',
    open: async (page, control) => {
      control.auditProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openAudit(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'license in force',
    path: '/license',
    open: async (page, control) => {
      await openLicense(page, control);
      await page.getByText('Northwind Compute').waitFor();
    },
  },
  {
    name: 'license missing',
    path: '/license',
    open: async (page, control) => {
      control.license = missingLicense;
      await openLicense(page, control);
      await page.getByText('No license is installed').waitFor();
    },
  },
  {
    name: 'license expired with a renewal waiting',
    path: '/license',
    open: async (page, control) => {
      control.license = {
        ...activeLicense,
        state: 'expired',
        new_business_allowed: false,
        warning: true,
        pending: { ...activeSummary, license_id: importableInspection.file?.license_id ?? '' },
      };
      await openLicense(page, control);
      await page.getByText('The license has expired').waitFor();
    },
  },
  {
    name: 'license that could not load',
    path: '/license',
    open: async (page, control) => {
      control.licenseProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openLicense(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'license file ready to import',
    path: '/license',
    open: async (page, control) => {
      await openLicense(page, control);
      await chooseLicenseFile(page);
      await page.getByRole('button', { name: 'Import this license' }).waitFor();
    },
  },
  {
    name: 'license file refused',
    path: '/license',
    open: async (page, control) => {
      control.inspection = {
        ...importableInspection,
        outcome: 'refused',
        code: 'LICENSE_DEPLOYMENT_MISMATCH',
        checks: checksStoppingAt('deployment'),
      };
      await openLicense(page, control);
      await chooseLicenseFile(page);
      await page.getByText('This file is for another installation').waitFor();
    },
  },
  {
    name: 'email delivery with a refused resend',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail];
      control.nextResend = { status: 409, code: 'INVITATION_STATE_CONFLICT' };
      await openMail(page, control);
      await page.getByRole('button', { name: /^Send again to/ }).click();
      await page.getByText('The invitation changed').waitFor();
    },
  },
];
