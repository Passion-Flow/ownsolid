// Workspaces, notifications and license status, which the application frame shows.
import type { Audience } from './identity.ts';
import { get, write, type RequestOptions } from './transport.ts';

/** One page of a list; `next_cursor` is null on the last page. */
export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

/** A workspace in a list. */
export interface WorkspaceSummary {
  id: string;
  tenant_id: string;
  name: string;
  status: string;
  version: number;
  is_default: boolean;
  role: 'owner' | 'member' | 'viewer' | null;
  created_at: string;
  updated_at: string;
}

/**
 * Lists workspaces the session may use. Administrators list from the admin API, which
 * only the admin origin serves.
 */
export function listWorkspaces(
  audience: Audience,
  limit: number,
  options?: RequestOptions,
): Promise<Page<WorkspaceSummary>> {
  const root = audience === 'admin' ? '/api/v1/admin/workspaces' : '/api/v1/workspaces';
  return get<Page<WorkspaceSummary>>(`${root}?limit=${limit}`, options);
}

/** Whether notifications are on, and how many are unread. */
export interface NotificationSummary {
  available: boolean;
  unread_count: number;
  retention_days: number;
}

/** How a finished workflow run ended. */
export type RunOutcome = 'succeeded' | 'failed' | 'cancelled' | 'interrupted';

/** A notification that a workflow run finished. */
export interface RunNotification {
  id: string;
  workspace_id: string;
  workflow_id: string;
  run_id: string;
  outcome: RunOutcome;
  error_code: string | null;
  occurred_at: string;
  read_at: string | null;
}

/** Reads the unread count. */
export function getNotificationSummary(options?: RequestOptions): Promise<NotificationSummary> {
  return get<NotificationSummary>('/api/v1/notifications/configuration', options);
}

/** Lists notifications, newest first. */
export function listNotifications(
  limit: number,
  cursor: string | null,
  options?: RequestOptions,
): Promise<Page<RunNotification>> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) query.set('cursor', cursor);
  return get<Page<RunNotification>>(`/api/v1/notifications?${query.toString()}`, options);
}

/** Marks one notification as read. */
export function markNotificationRead(id: string): Promise<RunNotification> {
  return write<RunNotification>('POST', `/api/v1/notifications/${encodeURIComponent(id)}/read`, {});
}

/** License condition every signed-in user may read. */
export interface LicenseStatus {
  state: 'missing' | 'not_yet_valid' | 'active' | 'grace' | 'expired' | 'clock_suspect';
  new_business_allowed: boolean;
  warning: boolean;
}

/** Reads the license condition. */
export function getLicenseStatus(options?: RequestOptions): Promise<LicenseStatus> {
  return get<LicenseStatus>('/api/v1/license/status', options);
}
