// The portal home (S-301): what needs the person, where they left off and what is published to their
// workspace, each read from the server and failing on its own, in the workspace the person chose.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  defaultWorkspace,
  failedRun,
  mockControlPlane,
  publishedApplication,
  recentConversation,
  teamWorkspace,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'portal');
  control.signedIn = true;
});

const section = (page: Page, heading: string) =>
  page.locator('section').filter({ has: page.getByRole('heading', { name: heading }) });

test('shows what needs the person, their conversations and what is published', async ({ page }) => {
  control.notifications = [
    { ...failedRun },
    {
      ...failedRun,
      id: '6f1c7a1e-0000-4000-8000-000000000030',
      outcome: 'succeeded',
      error_code: null,
    },
  ];
  control.applications = [publishedApplication];
  control.conversations = [recentConversation];
  await page.goto('/');
  await expect(page.getByText('Working in Research Lab.', { exact: false })).toBeVisible();
  const needs = section(page, 'Needs you');
  await expect(needs.getByText('Contract reviewer')).toBeVisible();
  await expect(needs.getByText('Workflow run failed')).toBeVisible();
  await expect(needs.getByText('MODEL_UNAVAILABLE')).toBeVisible();
  // A run that succeeded needs nobody.
  await expect(needs.getByText('Workflow run succeeded')).toBeHidden();
  const conversations = section(page, 'Pick up where you left off');
  await expect(conversations.getByText('Contract reviewer')).toBeVisible();
  await expect(conversations.getByText('4 turns', { exact: false })).toBeVisible();
  const published = section(page, 'Published to you');
  await expect(published.getByText('Contract reviewer')).toBeVisible();
  await expect(published.getByText('Version 3', { exact: false })).toBeVisible();
  // Their pages have not shipped, so the rows do not pretend to open.
  await expect(conversations.getByRole('link')).toHaveCount(0);
  await expect(published.getByRole('link')).toHaveCount(0);
  const usage = section(page, 'Usage');
  await expect(usage.getByText('Planned')).toBeVisible();
  await expect(usage.getByText('POR-111')).toBeVisible();
  // Every read names the session's workspace and no other.
  expect(new Set(control.applicationReads)).toEqual(new Set([defaultWorkspace.id]));
});

test('marks a run as read and leaves the rest', async ({ page }) => {
  control.notifications = [{ ...failedRun }];
  await page.goto('/');
  const needs = section(page, 'Needs you');
  await needs
    .getByRole('button', { name: 'Mark as read: An application that is no longer published' })
    .click();
  await expect(needs.getByText('Nothing needs you')).toBeVisible();
  expect(control.notifications[0]?.read_at).not.toBeNull();
});

test('says what will appear in each section when there is nothing yet', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('Nothing needs you')).toBeVisible();
  await expect(page.getByText('No conversations yet')).toBeVisible();
  await expect(page.getByText('Nothing published yet')).toBeVisible();
  await expect(
    page.getByText('An administrator publishes applications', { exact: false }),
  ).toBeVisible();
});

test('a section that cannot read fails alone and recovers on retry', async ({ page }) => {
  control.applications = [publishedApplication];
  control.conversations = [recentConversation];
  control.applicationsProblem = { status: 503, code: 'WORKFLOW_UNAVAILABLE' };
  await page.goto('/');
  const published = section(page, 'Published to you');
  await expect(published.getByText('Applications are not available')).toBeVisible();
  // It never claims to be empty, and the other sections still read.
  await expect(published.getByText('Nothing published yet')).toBeHidden();
  await expect(
    section(page, 'Pick up where you left off').getByText('4 turns', { exact: false }),
  ).toBeVisible();
  await expect(page.getByText('Nothing needs you')).toBeVisible();
  control.applicationsProblem = null;
  await published.getByRole('button', { name: 'Try again' }).click();
  await expect(published.getByText('Contract reviewer')).toBeVisible();
});

test('notifications that cannot be read leave the lists standing', async ({ page }) => {
  control.conversations = [recentConversation];
  control.notificationsProblem = { status: 503, code: 'NOTIFICATION_UNAVAILABLE' };
  await page.goto('/');
  const needs = section(page, 'Needs you');
  await expect(needs.getByText('Notifications are not available')).toBeVisible();
  await expect(needs.getByText('Nothing needs you')).toBeHidden();
  await expect(
    section(page, 'Pick up where you left off').getByText('Contract reviewer'),
  ).toBeVisible();
  control.notificationsProblem = null;
  await needs.getByRole('button', { name: 'Try again' }).click();
  await expect(needs.getByText('Nothing needs you')).toBeVisible();
});

test('switching the workspace shows that workspace and nothing of the last', async ({ page }) => {
  const team = { ...teamWorkspace, role: 'member' as const };
  control.workspaceList = [defaultWorkspace, team];
  control.notifications = [{ ...failedRun }];
  control.applications = [
    publishedApplication,
    {
      ...publishedApplication,
      workflow_id: '6f1c7a1e-0000-4000-8000-000000000031',
      workspace_id: team.id,
      name: 'Incident summarizer',
    },
  ];
  await page.goto('/');
  await expect(section(page, 'Published to you').getByText('Contract reviewer')).toBeVisible();
  await page.getByRole('button', { name: /^Workspace: Research Lab/ }).click();
  await page.getByRole('menuitem', { name: 'Platform Team' }).click();
  await expect(page.getByText('Working in Platform Team.', { exact: false })).toBeVisible();
  const published = section(page, 'Published to you');
  await expect(published.getByText('Incident summarizer')).toBeVisible();
  await expect(published.getByText('Contract reviewer')).toBeHidden();
  // The failed run belongs to the other workspace.
  await expect(page.getByText('Nothing needs you')).toBeVisible();
  expect(control.applicationReads).toContain(team.id);
});

test('a workspace the person lost is not found, and says so', async ({ page }) => {
  control.workspaceList = [defaultWorkspace];
  control.applications = [publishedApplication];
  await page.goto('/');
  await expect(section(page, 'Published to you').getByText('Contract reviewer')).toBeVisible();
  // Membership ends while the page is open: the server answers as if the workspace did not exist.
  control.workspaceList = [{ ...defaultWorkspace, role: null }];
  await page.reload();
  await expect(section(page, 'Published to you').getByText('Not found')).toBeVisible();
  await expect(section(page, 'Published to you').getByText('Contract reviewer')).toBeHidden();
});

test('tells a person in no workspace who can add them', async ({ page }) => {
  control.workspaceList = [];
  control.sessionWorkspace = null;
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'No workspace yet' })).toBeVisible();
  expect(control.applicationReads).toEqual([]);
});

test('reads in Chinese', async ({ page }) => {
  control.preferences.locale = 'zh-CN';
  control.applications = [publishedApplication];
  await page.goto('/');
  await expect(page.getByRole('heading', { name: chinese['home.needs'] })).toBeVisible();
  await expect(page.getByRole('heading', { name: chinese['home.conversations'] })).toBeVisible();
  await expect(page.getByRole('heading', { name: chinese['home.apps'] })).toBeVisible();
  await expect(page.getByText(chinese['home.needs.clear'])).toBeVisible();
});
