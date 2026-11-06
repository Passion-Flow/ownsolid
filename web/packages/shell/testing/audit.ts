// A scripted audit trail for browser flows.
import type { AuditEvent } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const hoursAgo = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString();

/** An administrator's action. */
export const licenseEvent: AuditEvent = {
  id: '6f1c7a1e-0000-4000-8000-0000000000e1',
  occurred_at: hoursAgo(1),
  actor_user_id: '6f1c7a1e-0000-4000-8000-000000000001',
  actor_email: 'ada@example.test',
  subject_user_id: null,
  subject_email: null,
  audience: 'admin',
  action: 'license.imported',
  outcome: 'success',
  target_type: 'platform_license',
  target_id: '6f1c7a1e-0000-4000-8000-0000000000d2',
  reason: 'Annual renewal',
  request_id: '6f1c7a1e-0000-4000-8000-0000000000f1',
};

/** An action on someone's account, by another person, that failed. */
export const failedSignIn: AuditEvent = {
  ...licenseEvent,
  id: '6f1c7a1e-0000-4000-8000-0000000000e2',
  occurred_at: hoursAgo(2),
  actor_user_id: '6f1c7a1e-0000-4000-8000-000000000070',
  actor_email: 'grace@example.test',
  subject_user_id: '6f1c7a1e-0000-4000-8000-000000000070',
  subject_email: 'grace@example.test',
  audience: 'portal',
  action: 'auth.login_failed',
  outcome: 'failure',
  target_type: null,
  target_id: null,
  reason: null,
  request_id: '6f1c7a1e-0000-4000-8000-0000000000f2',
};

/** Something the platform did by itself. */
export const systemEvent: AuditEvent = {
  ...licenseEvent,
  id: '6f1c7a1e-0000-4000-8000-0000000000e3',
  occurred_at: hoursAgo(30),
  actor_user_id: null,
  actor_email: null,
  audience: 'system',
  action: 'system.retention',
  target_type: null,
  target_id: null,
  reason: null,
  request_id: '6f1c7a1e-0000-4000-8000-0000000000f3',
};

/** What a flow changes to script the audit trail. */
export interface AuditScript {
  auditEvents: AuditEvent[];
  auditProblem: { status: number; code: string } | null;
  /** The query of each read, in order. */
  auditQueries: URLSearchParams[];
}

/** The state of a mock with nothing scripted. */
export function emptyAuditScript(): AuditScript {
  return { auditEvents: [], auditProblem: null, auditQueries: [] };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

/** Serves the audit trail from `state`, paging by position as a cursor would. */
export async function routeAudit(
  page: Page,
  state: AuditScript & { audience: string },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  await page.route('**/api/v1/admin/audit-events?*', (route) => {
    if (state.audience !== 'admin') {
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    }
    if (state.auditProblem) return problem(route, state.auditProblem);
    const query = new URL(route.request().url()).searchParams;
    state.auditQueries.push(query);
    const category = query.get('category');
    const outcome = query.get('outcome');
    const actor = query.get('actor_user_id');
    const request = query.get('request_id');
    const from = query.get('from');
    const to = query.get('to');
    const matching = state.auditEvents.filter(
      (event) =>
        (!category || event.action.startsWith(`${category}.`)) &&
        (!outcome || event.outcome === outcome) &&
        (!actor || event.actor_user_id === actor) &&
        (!request || event.request_id === request) &&
        (!from || Date.parse(event.occurred_at) >= Date.parse(from)) &&
        (!to || Date.parse(event.occurred_at) <= Date.parse(to)),
    );
    const start = Number(query.get('cursor') ?? 0);
    const limit = Number(query.get('limit') ?? matching.length);
    const end = start + limit;
    return data(route, {
      items: matching.slice(start, end),
      next_cursor: end < matching.length ? String(end) : null,
    });
  });
}
