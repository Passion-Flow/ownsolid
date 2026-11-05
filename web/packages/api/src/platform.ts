// Workspaces, notifications and license status, which the application frame shows.
import { createClient, type Success } from './client.ts';
import type {
  licensingContract,
  notificationsContract,
  preferencesContract,
  ssoContract,
  workspacesContract,
} from './gen/index.ts';
import type { Audience } from './identity.ts';
import type { RequestOptions } from './transport.ts';

/** A workspace in a list. */
export type WorkspaceSummary = workspacesContract.components['schemas']['Workspace'];

/** One page of workspaces. */
export type WorkspacePage = Success<workspacesContract.operations['listPortalWorkspaces']>;

/** Whether notifications are on, and how many are unread. */
export type NotificationSummary =
  notificationsContract.components['schemas']['NotificationConfiguration'];

/** A notification that a workflow run finished. */
export type RunNotification = notificationsContract.components['schemas']['TerminalNotification'];

/** How a finished workflow run ended. */
export type RunOutcome = RunNotification['outcome'];

/** One page of notifications. */
export type NotificationPage =
  notificationsContract.components['schemas']['TerminalNotificationPage'];

/** License condition every signed-in user may read. */
export type LicenseStatus = licensingContract.components['schemas']['PortalLicenseState'];

/** A single sign-on provider offered on the sign-in page. */
export type SsoConnector = ssoContract.components['schemas']['PublicSSOConnector'];

/** The theme and density saved on the account. */
export type Preference = preferencesContract.components['schemas']['Preference'];

/** The account fields to change; fields left out keep their saved value. */
export type PreferenceChange = Partial<Pick<Preference, 'theme' | 'density'>>;

const sso = createClient<ssoContract.paths>();
const workspaces = createClient<workspacesContract.paths>();
const notifications = createClient<notificationsContract.paths>();
const licensing = createClient<licensingContract.paths>();
const preferences = createClient<preferencesContract.paths>();

/**
 * Lists workspaces the session may use. Administrators list from the admin API, which
 * only the admin origin serves.
 */
export function listWorkspaces(
  audience: Audience,
  limit: number,
  options?: RequestOptions,
): Promise<WorkspacePage> {
  const path = audience === 'admin' ? '/api/v1/admin/workspaces' : '/api/v1/workspaces';
  return workspaces.get(path, { ...options, query: { limit } });
}

/** Reads the unread count. */
export function getNotificationSummary(options?: RequestOptions): Promise<NotificationSummary> {
  return notifications.get('/api/v1/notifications/configuration', options);
}

/** Lists notifications, newest first. */
export function listNotifications(
  limit: number,
  cursor: string | null,
  options?: RequestOptions,
): Promise<NotificationPage> {
  return notifications.get('/api/v1/notifications', {
    ...options,
    query: { limit, ...(cursor ? { cursor } : {}) },
  });
}

/** Marks one notification as read. */
export function markNotificationRead(id: string): Promise<RunNotification> {
  return notifications.post('/api/v1/notifications/{notification_id}/read', {
    path: { notification_id: id },
    body: {},
  });
}

/** Reads the license condition. */
export function getLicenseStatus(options?: RequestOptions): Promise<LicenseStatus> {
  return licensing.get('/api/v1/license/status', options);
}

/** Reads the theme and density saved on the account. */
export function getPreferences(options?: RequestOptions): Promise<Preference> {
  return preferences.get('/api/v1/preferences', options);
}

/**
 * Saves a change on top of `revision`. Fails with `PREFERENCE_REVISION_CONFLICT` when
 * another browser saved first.
 */
export function updatePreferences(change: PreferenceChange, revision: number): Promise<Preference> {
  return preferences.patch('/api/v1/preferences', {
    body: { ...change, expected_revision: revision },
  });
}

/** Lists the single sign-on providers this entrance offers. */
export async function listSsoConnectors(options?: RequestOptions): Promise<SsoConnector[]> {
  return (await sso.get('/api/v1/auth/sso/connectors', options)).items;
}

/** Starts single sign-on and returns the provider's address to go to. */
export async function startSsoLogin(connectorId: string): Promise<string> {
  const started = await sso.post('/api/v1/auth/sso/{connector_id}/starts', {
    path: { connector_id: connectorId },
    body: { mode: 'login', password: null, reason: null },
  });
  return started.authorization_url;
}
