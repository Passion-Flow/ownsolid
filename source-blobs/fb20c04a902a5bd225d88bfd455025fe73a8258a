// Scripted accounts and invitations for browser flows.
import type { Invitation, User } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-06T08:00:00Z';

/** The signed-in administrator, as the Users page lists it. */
export const adminUser: User = {
  id: '6f1c7a1e-0000-4000-8000-000000000001',
  email: 'ada@example.test',
  display_name: 'Ada Lovelace',
  locale: 'en',
  status: 'active',
  is_platform_admin: true,
  email_verified_at: stamp,
  version: 1,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

/** Another account an administrator can disable. */
export const memberUser: User = {
  ...adminUser,
  id: '6f1c7a1e-0000-4000-8000-000000000070',
  email: 'grace@example.test',
  display_name: 'Grace Hopper',
  locale: 'zh-CN',
  is_platform_admin: false,
  email_verified_at: null,
  version: 4,
  created_at: stamp,
};

/** An invitation waiting to be accepted, whose email failed. */
export const pendingInvitation: Invitation = {
  id: '6f1c7a1e-0000-4000-8000-000000000080',
  email: 'alan@example.test',
  locale: 'en',
  status: 'pending',
  version: 1,
  expires_at: '2026-11-13T08:00:00Z',
  created_at: stamp,
  updated_at: stamp,
  delivery: {
    id: '6f1c7a1e-0000-4000-8000-000000000081',
    state: 'failed',
    attempt_count: 5,
    error_category: 'smtp_unavailable',
  },
};

/** What a flow changes to script accounts and invitations. */
export interface UserScript {
  users: User[];
  usersProblem: { status: number; code: string } | null;
  /** The next status change fails like this. */
  nextStatusChange: { status: number; code: string } | null;
  invitations: Invitation[];
  /** The next new invitation fails like this. */
  nextInvite: { status: number; code: string } | null;
  /** The next resend of an invitation fails like this. */
  nextInvitationResend: { status: number; code: string } | null;
  /** Idempotency-Key of each invitation and resend, in order. */
  invitationKeys: string[];
  /** Reasons given for status changes, in order. */
  userReasons: string[];
}

/** The state of a mock with nothing scripted. */
export function emptyUserScript(): UserScript {
  return {
    users: [{ ...adminUser }],
    usersProblem: null,
    nextStatusChange: null,
    invitations: [],
    nextInvite: null,
    nextInvitationResend: null,
    invitationKeys: [],
    userReasons: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

type Scripted = { status: number; code: string } | null;

function take(
  state: UserScript,
  name: 'nextStatusChange' | 'nextInvite' | 'nextInvitationResend',
): Scripted {
  const scripted = state[name];
  state[name] = null;
  return scripted;
}

/** Serves the account and invitation routes of the admin entrance from `state`. */
export async function routeUsers(
  page: Page,
  state: UserScript & { audience: string },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const csrf = (route: Route) => route.request().headers()['x-csrf-token'] === 'csrf-token';
  await page.route('**/api/v1/admin/users?*', (route) => {
    if (state.audience !== 'admin')
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (state.usersProblem) return problem(route, state.usersProblem);
    const query = new URL(route.request().url()).searchParams;
    const status = query.get('status');
    const prefix = query.get('email_prefix');
    const search = query.get('q')?.toLowerCase();
    return data(route, {
      items: state.users.filter(
        (item) =>
          (!status || item.status === status) &&
          (!prefix || item.email.startsWith(prefix)) &&
          (!search ||
            item.email.toLowerCase().includes(search) ||
            item.display_name.toLowerCase().includes(search)),
      ),
      next_cursor: null,
    });
  });
  await page.route(/\/api\/v1\/admin\/users\/[0-9a-f-]{36}$/, (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-1);
    const found = state.users.find((item) => item.id === id);
    return found ? data(route, found) : problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
  });
  await page.route('**/api/v1/admin/users/*/status', (route) => {
    if (!csrf(route)) return problem(route, { status: 403, code: 'CSRF_INVALID' });
    const scripted = take(state, 'nextStatusChange');
    if (scripted) return problem(route, scripted);
    const id = new URL(route.request().url()).pathname.split('/').at(-2);
    const index = state.users.findIndex((item) => item.id === id);
    const found = state.users[index];
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    const change = route.request().postDataJSON() as {
      status: User['status'];
      version: number;
      reason: string;
    };
    if (change.version !== found.version)
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    state.userReasons.push(change.reason);
    const updated = { ...found, status: change.status, version: found.version + 1 };
    state.users[index] = updated;
    return data(route, updated);
  });
  await page.route('**/api/v1/admin/invitations?*', (route) => {
    const status = new URL(route.request().url()).searchParams.get('status');
    return data(route, {
      items: state.invitations.filter((item) => !status || item.status === status),
      next_cursor: null,
    });
  });
  await page.route('**/api/v1/admin/invitations', (route) => {
    if (!csrf(route)) return problem(route, { status: 403, code: 'CSRF_INVALID' });
    state.invitationKeys.push(route.request().headers()['idempotency-key'] ?? '');
    const scripted = take(state, 'nextInvite');
    if (scripted) return problem(route, scripted);
    const body = route.request().postDataJSON() as { email: string; locale: string };
    const invitation: Invitation = {
      ...pendingInvitation,
      id: `6f1c7a1e-0000-4000-8000-${String(state.invitations.length + 90).padStart(12, '0')}`,
      email: body.email,
      locale: body.locale,
      delivery: {
        id: crypto.randomUUID(),
        state: 'queued',
        attempt_count: 0,
        error_category: null,
      },
    };
    state.invitations.unshift(invitation);
    return data(route, invitation, 201);
  });
  await page.route('**/api/v1/admin/invitations/*/resends', (route) => {
    if (!csrf(route)) return problem(route, { status: 403, code: 'CSRF_INVALID' });
    state.invitationKeys.push(route.request().headers()['idempotency-key'] ?? '');
    const scripted = take(state, 'nextInvitationResend');
    if (scripted) return problem(route, scripted);
    const id = new URL(route.request().url()).pathname.split('/').at(-2);
    const index = state.invitations.findIndex((item) => item.id === id);
    const found = state.invitations[index];
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    const resent: Invitation = {
      ...found,
      status: 'pending',
      version: found.version + 1,
      delivery: {
        id: crypto.randomUUID(),
        state: 'queued',
        attempt_count: 0,
        error_category: null,
      },
    };
    state.invitations[index] = resent;
    return data(route, resent);
  });
}
