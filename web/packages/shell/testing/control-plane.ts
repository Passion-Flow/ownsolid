// A scripted control plane for browser tests. It speaks the real wire format of the
// paths the shell uses, so specs exercise the production transport end to end.
import type { Page, Route } from '@playwright/test';
import { emptyNodeScript, routeNodes, type NodeScript } from './nodes.ts';
import { emptyTaskScript, routeTasks, type TaskScript } from './tasks.ts';

/** The account the mock accepts. */
export const account = { email: 'ada@example.test', password: 'correct horse battery staple' };

const user = {
  id: '6f1c7a1e-0000-4000-8000-000000000001',
  email: account.email,
  display_name: 'Ada Lovelace',
  locale: 'en',
  status: 'active',
  is_platform_admin: true,
  email_verified_at: null,
  version: 1,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

const workspace = {
  id: '6f1c7a1e-0000-4000-8000-000000000002',
  tenant_id: '6f1c7a1e-0000-4000-8000-000000000003',
  name: 'Research Lab',
  role: 'owner',
};

const runtimeConfig = {
  topology: { size: 'single-node' },
  transport: { mode: 'direct_https', plaintext: false },
  request_timeout_milliseconds: 30_000,
  ui: {
    poll_milliseconds: 5_000,
    search_debounce_milliseconds: 150,
    page_size: 20,
    lookup_page_size: 100,
  },
};

const defaultWorkspace = {
  ...workspace,
  status: 'active',
  version: 1,
  is_default: true,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

const secondWorkspace = {
  ...defaultWorkspace,
  id: '6f1c7a1e-0000-4000-8000-000000000004',
  name: 'Platform Team',
  is_default: false,
};

/** A provider connector the account page offers to link. */
export const giteaConnector = {
  id: '6f1c7a1e-0000-4000-8000-000000000008',
  name: 'Company Gitea',
  provider: 'gitea',
};

/** A provider account already linked to the signed-in account. */
export const giteaBinding = {
  id: '6f1c7a1e-0000-4000-8000-000000000009',
  connector_id: giteaConnector.id,
  name: giteaConnector.name,
  provider: giteaConnector.provider,
  subject: '4242',
  active: true,
  revision: 3,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

/** What is left of a session nobody has touched for a moment. */
export const freshSession = {
  idle_timeout_seconds: 900,
  idle_remaining_seconds: 900,
  absolute_remaining_seconds: 14_400,
  warning_seconds: 120,
};

/** The token of a valid emailed link. */
export const linkToken = 'a'.repeat(43);

/** A notification the mock lists while it is unread. */
export const failedRun = {
  id: '6f1c7a1e-0000-4000-8000-000000000005',
  workspace_id: workspace.id,
  workflow_id: '6f1c7a1e-0000-4000-8000-000000000006',
  run_id: '6f1c7a1e-0000-4000-8000-000000000007',
  outcome: 'failed',
  error_code: 'MODEL_UNAVAILABLE',
  occurred_at: '2026-11-04T08:00:00Z',
  read_at: null as string | null,
};

/** An email in the delivery list. */
export interface ScriptedMail {
  id: string;
  purpose: 'invitation' | 'password_reset' | 'password_changed';
  recipient_email: string;
  locale: string;
  state: 'queued' | 'sending' | 'sent' | 'failed' | 'cancelled';
  attempt_count: number;
  next_attempt_at: string | null;
  error_category: string | null;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
  invitation: { id: string; version: number; status: string } | null;
}

/** A failed invitation email, which the console offers to send again. */
export const failedMail: ScriptedMail = {
  id: '6f1c7a1e-0000-4000-8000-000000000010',
  purpose: 'invitation',
  recipient_email: 'grace@example.test',
  locale: 'en',
  state: 'failed',
  attempt_count: 5,
  next_attempt_at: null,
  error_category: 'smtp_unavailable',
  created_at: '2026-11-04T08:00:00Z',
  updated_at: '2026-11-04T09:00:00Z',
  completed_at: '2026-11-04T09:00:00Z',
  invitation: { id: '6f1c7a1e-0000-4000-8000-000000000030', version: 2, status: 'pending' },
};

/** Another failed email, for bulk actions. */
export const secondFailedMail: ScriptedMail = {
  ...failedMail,
  id: '6f1c7a1e-0000-4000-8000-000000000012',
  recipient_email: 'hedy@example.test',
  invitation: { id: '6f1c7a1e-0000-4000-8000-000000000031', version: 2, status: 'pending' },
};

/** A delivered password reset email. */
export const sentMail: ScriptedMail = {
  ...failedMail,
  id: '6f1c7a1e-0000-4000-8000-000000000011',
  purpose: 'password_reset',
  recipient_email: account.email,
  state: 'sent',
  attempt_count: 1,
  error_category: null,
  invitation: null,
};

/** A saved view of a list. */
export interface ScriptedView {
  id: string;
  list: string;
  name: string;
  state: Record<string, unknown>;
  revision: number;
  created_at: string;
  updated_at: string;
}

/** A scripted failure for one path. */
export interface ScriptedProblem {
  status: number;
  code: string;
  headers?: Record<string, string>;
}

/** Mutable state of the mock, so a spec can change it between steps. */
export interface ControlPlane extends TaskScript, NodeScript {
  audience: 'admin' | 'portal';
  /** How often pages poll, as the runtime configuration says. */
  pollMilliseconds: number;
  signedIn: boolean;
  /** The signed-in account, which profile saves and password changes update. */
  user: typeof user;
  /** The password sign-in and password changes check. */
  password: string;
  /** Answers the next profile save with this failure instead of saving. */
  nextProfileSave: ScriptedProblem | null;
  /** Answers the next password change with this failure instead of checking it. */
  nextPasswordChange: ScriptedProblem | null;
  /** Provider accounts linked to the account, past links included. */
  ssoBindings: (typeof giteaBinding)[];
  /** Fails the linked provider list with this problem while set. */
  ssoBindingsProblem: ScriptedProblem | null;
  /** This session started through the provider, so unlinking it ends the session. */
  sessionFromProvider: boolean;
  /** Answers the next unlink with this failure instead of unlinking. */
  nextUnlink: ScriptedProblem | null;
  /** Idempotency-Key of each unlink request, in order. */
  unlinkKeys: string[];
  /** Bodies of the single sign-on starts the server received, in order. */
  ssoStarts: Record<string, unknown>[];
  /** How many CSRF tokens the page has fetched. */
  csrfFetches: number;
  /** What the session reports having left. A read that is not background extends it. */
  session: typeof freshSession;
  /** Whether each session read was marked as background, in order. */
  sessionReads: boolean[];
  /** Whether each notification count read was marked as background, in order. */
  notificationPolls: boolean[];
  /** Fails `/runtime-config.json` with a dropped connection while true. */
  unreachable: boolean;
  /** Answers the next sign-in with this failure instead of checking credentials. */
  nextSignIn: ScriptedProblem | null;
  /** Fails sign-out with this problem while set. */
  signOutProblem: ScriptedProblem | null;
  /** Fails the notification list with this problem while set. */
  notificationsProblem: ScriptedProblem | null;
  /** Fails the workspace list with this problem while set. */
  workspacesProblem: ScriptedProblem | null;
  notifications: (typeof failedRun)[];
  /** Single sign-on providers the sign-in page offers. */
  ssoConnectors: { id: string; name: string; provider: string }[];
  /** Where starting single sign-on sends the browser. */
  ssoAddress: string;
  /** Failure of the next password reset or registration, instead of succeeding. */
  nextAccountWrite: ScriptedProblem | null;
  /** The password the last reset or registration set. */
  lastPassword: string | null;
  /** The theme, density and language saved on the account. */
  preferences: { theme: string; density: string; locale: string; revision: number };
  /** The tenant's settings, which also set the public default language. */
  tenantSettings: { default_locale: string; revision: number };
  /** Fails reading the tenant's settings with this problem while set. */
  tenantSettingsProblem: ScriptedProblem | null;
  /** Fails saving preferences with this problem while set. */
  preferencesProblem: ScriptedProblem | null;
  /** Bodies of the preference saves the server received, in order. */
  preferenceWrites: Record<string, unknown>[];
  /** Emails in the delivery list, newest first. */
  mail: ScriptedMail[];
  /** The caller's saved views. */
  savedViews: ScriptedView[];
  /** Fails listing saved views with this problem while set. */
  savedViewsProblem: ScriptedProblem | null;
  /** Fails the next save of a view with this problem. */
  nextViewSave: ScriptedProblem | null;
  /** Fails the delivery list with this problem while set. */
  mailProblem: ScriptedProblem | null;
  /** Fails the next revocation with this problem, instead of revoking. */
  nextRevocation: ScriptedProblem | null;
  /** Bodies of the revocations the server received, in order. */
  revocations: Record<string, unknown>[];
  /** Fails the next resend with this problem, instead of queuing a new email. */
  nextResend: ScriptedProblem | null;
  /** Idempotency-Key of each resend request, in order. */
  resendKeys: string[];
}

function problem(route: Route, { status, code, headers }: ScriptedProblem): Promise<void> {
  return route.fulfill({
    status,
    contentType: 'application/json',
    ...(headers ? { headers } : {}),
    body: JSON.stringify({
      type: 'about:blank',
      title: code,
      status,
      code,
      request_id: '6f1c7a1e-0000-4000-8000-0000000000ff',
    }),
  });
}

function data(route: Route, value: unknown, status = 200): Promise<void> {
  return route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify({ data: value }),
  });
}

/** Installs the mock on a page and returns its state. */
export async function mockControlPlane(
  page: Page,
  audience: ControlPlane['audience'],
): Promise<ControlPlane> {
  const state: ControlPlane = {
    ...emptyTaskScript(),
    ...emptyNodeScript(),
    audience,
    pollMilliseconds: runtimeConfig.ui.poll_milliseconds,
    signedIn: false,
    user: { ...user },
    password: account.password,
    nextProfileSave: null,
    nextPasswordChange: null,
    ssoBindings: [],
    ssoBindingsProblem: null,
    sessionFromProvider: false,
    nextUnlink: null,
    unlinkKeys: [],
    ssoStarts: [],
    csrfFetches: 0,
    session: { ...freshSession },
    sessionReads: [],
    notificationPolls: [],
    unreachable: false,
    nextSignIn: null,
    signOutProblem: null,
    notificationsProblem: null,
    workspacesProblem: null,
    notifications: [],
    ssoConnectors: [],
    ssoAddress: '',
    nextAccountWrite: null,
    lastPassword: null,
    preferences: { theme: 'system', density: 'comfortable', locale: 'auto', revision: 1 },
    tenantSettings: { default_locale: 'auto', revision: 1 },
    tenantSettingsProblem: null,
    preferencesProblem: null,
    preferenceWrites: [],
    mail: [],
    savedViews: [],
    savedViewsProblem: null,
    nextViewSave: null,
    mailProblem: null,
    nextRevocation: null,
    revocations: [],
    nextResend: null,
    resendKeys: [],
  };
  const identity = () => ({ user: state.user, workspace, audience: state.audience });

  await page.route('**/runtime-config.json', (route) =>
    state.unreachable
      ? route.abort('connectionrefused')
      : data(route, {
          ...runtimeConfig,
          default_locale: state.tenantSettings.default_locale,
          ui: { ...runtimeConfig.ui, poll_milliseconds: state.pollMilliseconds },
        }),
  );
  await page.route('**/api/v1/auth/csrf', (route) => {
    state.csrfFetches += 1;
    return data(route, {
      token: 'csrf-token',
      expires_at: new Date(Date.now() + 3_600_000).toISOString(),
    });
  });
  await page.route('**/api/v1/me', (route) => {
    if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_REQUIRED' });
    const request = route.request();
    if (request.method() === 'GET') return data(route, identity());
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextProfileSave) {
      const scripted = state.nextProfileSave;
      state.nextProfileSave = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as { display_name: string; version: number };
    if (body.version !== state.user.version) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    state.user = {
      ...state.user,
      display_name: body.display_name,
      version: state.user.version + 1,
    };
    return data(route, identity());
  });
  await page.route('**/api/v1/me/session', (route) => {
    if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_SESSION_EXPIRED' });
    const background = route.request().headers()['x-ownsolid-background'] === 'true';
    state.sessionReads.push(background);
    if (!background) {
      state.session = {
        ...state.session,
        idle_remaining_seconds: state.session.idle_timeout_seconds,
      };
    }
    return data(route, state.session);
  });
  await page.route('**/api/v1/me/password-changes', (route) => {
    const request = route.request();
    if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_REQUIRED' });
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextPasswordChange) {
      const scripted = state.nextPasswordChange;
      state.nextPasswordChange = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as { current_password: string; new_password: string };
    if (body.current_password !== state.password) {
      return problem(route, { status: 403, code: 'PASSWORD_CURRENT_INVALID' });
    }
    if (body.new_password === state.password) {
      return problem(route, { status: 422, code: 'PASSWORD_UNCHANGED' });
    }
    if (body.new_password.length < 15) {
      return problem(route, { status: 422, code: 'PASSWORD_POLICY_VIOLATION' });
    }
    state.password = body.new_password;
    state.user = { ...state.user, version: state.user.version + 1 };
    return data(route, identity());
  });
  await page.route('**/api/v1/auth/login', (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextSignIn) {
      const scripted = state.nextSignIn;
      state.nextSignIn = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as { email?: string; password?: string };
    if (body.email !== account.email || body.password !== state.password) {
      return problem(route, { status: 401, code: 'AUTH_INVALID_CREDENTIALS' });
    }
    state.signedIn = true;
    return data(route, {
      ...identity(),
      session: { absolute_expires_at: '2026-11-05T00:00:00Z', idle_timeout_seconds: 1800 },
    });
  });
  await page.route('**/api/v1/auth/logout', (route) => {
    if (state.signOutProblem) return problem(route, state.signOutProblem);
    state.signedIn = false;
    return route.fulfill({ status: 204 });
  });
  const accountWrite = (route: Route, answer: unknown) => {
    if (state.nextAccountWrite) {
      const scripted = state.nextAccountWrite;
      state.nextAccountWrite = null;
      return problem(route, scripted);
    }
    const body = route.request().postDataJSON() as { token?: string; password?: string };
    if (body.token !== linkToken) return problem(route, { status: 400, code: 'TOKEN_UNUSABLE' });
    state.lastPassword = body.password ?? null;
    return data(route, answer);
  };
  await page.route('**/api/v1/auth/password-reset-requests', (route) =>
    data(route, { accepted: true }),
  );
  await page.route('**/api/v1/auth/password-resets', (route) =>
    accountWrite(route, { reset: true }),
  );
  await page.route('**/api/v1/auth/register', (route) => accountWrite(route, { registered: true }));
  await page.route('**/api/v1/auth/invitation-validations', (route) => {
    const body = route.request().postDataJSON() as { token?: string };
    return body.token === linkToken
      ? data(route, {
          email: account.email,
          locale: 'en',
          expires_at: '2026-11-05T00:00:00Z',
        })
      : problem(route, { status: 400, code: 'TOKEN_UNUSABLE' });
  });
  await page.route('**/api/v1/auth/sso/connectors', (route) =>
    data(route, { items: state.ssoConnectors }),
  );
  await page.route('**/api/v1/auth/sso/*/starts', (route) => {
    const body = route.request().postDataJSON() as { mode: string; password: string | null };
    state.ssoStarts.push(body);
    if (body.mode === 'link' && body.password !== state.password) {
      return problem(route, { status: 403, code: 'SSO_PASSWORD_REQUIRED' });
    }
    return data(route, {
      authorization_url: state.ssoAddress,
      expires_at: '2026-11-05T00:00:00Z',
    });
  });
  await page.route('**/api/v1/account/sso/bindings', (route) => {
    if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_SESSION_EXPIRED' });
    return state.ssoBindingsProblem
      ? problem(route, state.ssoBindingsProblem)
      : data(route, { items: state.ssoBindings });
  });
  await page.route('**/api/v1/account/sso/bindings/*/unlink', (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    state.unlinkKeys.push(request.headers()['idempotency-key'] ?? '');
    if (state.nextUnlink) {
      const scripted = state.nextUnlink;
      state.nextUnlink = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as { revision: number; password: string };
    const id = new URL(request.url()).pathname.split('/').at(-2);
    const binding = state.ssoBindings.find((item) => item.id === id);
    if (!binding) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (body.password !== state.password) {
      return problem(route, { status: 403, code: 'SSO_PASSWORD_REQUIRED' });
    }
    if (body.revision !== binding.revision) {
      return problem(route, { status: 409, code: 'SSO_REVISION_CONFLICT' });
    }
    const unlinked = { ...binding, active: false, revision: binding.revision + 1 };
    state.ssoBindings = state.ssoBindings.map((item) => (item.id === id ? unlinked : item));
    if (state.sessionFromProvider) state.signedIn = false;
    return data(route, unlinked);
  });

  // Each origin serves only its own workspace list, as the control plane does.
  const workspaces = (owner: ControlPlane['audience']) => (route: Route) => {
    if (state.audience !== owner)
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (state.workspacesProblem) return problem(route, state.workspacesProblem);
    return data(route, { items: [defaultWorkspace, secondWorkspace], next_cursor: null });
  };
  await page.route('**/api/v1/admin/workspaces?*', workspaces('admin'));
  await page.route('**/api/v1/workspaces?*', workspaces('portal'));
  await page.route('**/api/v1/notifications/configuration', (route) => {
    state.notificationPolls.push(route.request().headers()['x-ownsolid-background'] === 'true');
    return data(route, {
      available: true,
      unread_count: state.notifications.filter((item) => !item.read_at).length,
      retention_days: 30,
    });
  });
  await page.route('**/api/v1/notifications?*', (route) =>
    state.notificationsProblem
      ? problem(route, state.notificationsProblem)
      : data(route, { items: state.notifications, next_cursor: null }),
  );
  await page.route('**/api/v1/notifications/*/read', (route) => {
    const item = state.notifications[0];
    if (!item) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    item.read_at = '2026-11-04T09:00:00Z';
    return data(route, item);
  });
  await page.route('**/api/v1/license/status', (route) =>
    data(route, { state: 'active', new_business_allowed: true, warning: false }),
  );
  await page.route('**/api/v1/preferences', (route) => {
    const request = route.request();
    if (request.method() === 'GET') {
      return data(route, { ...state.preferences, updated_at: '2026-11-04T00:00:00Z' });
    }
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.preferencesProblem) return problem(route, state.preferencesProblem);
    const body = request.postDataJSON() as Record<string, unknown>;
    state.preferenceWrites.push(body);
    const { expected_revision: expected, ...change } = body;
    if (expected !== state.preferences.revision) {
      return problem(route, { status: 409, code: 'PREFERENCE_REVISION_CONFLICT' });
    }
    state.preferences = {
      ...state.preferences,
      ...change,
      revision: state.preferences.revision + 1,
    };
    return data(route, { ...state.preferences, updated_at: '2026-11-04T00:00:00Z' });
  });
  await page.route('**/api/v1/saved-views?*', (route) => {
    if (state.savedViewsProblem) return problem(route, state.savedViewsProblem);
    const list = new URL(route.request().url()).searchParams.get('list');
    return data(route, { items: state.savedViews.filter((view) => view.list === list) });
  });
  await page.route(/\/api\/v1\/saved-views\/[^/?]+(\?.*)?$/, (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    const url = new URL(request.url());
    const id = url.pathname.split('/').at(-1) ?? '';
    const current = state.savedViews.find((view) => view.id === id);
    if (request.method() === 'DELETE') {
      if (!current) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      if (url.searchParams.get('revision') !== String(current.revision)) {
        return problem(route, { status: 409, code: 'PREFERENCE_REVISION_CONFLICT' });
      }
      state.savedViews = state.savedViews.filter((view) => view.id !== id);
      return route.fulfill({ status: 204 });
    }
    if (state.nextViewSave) {
      const scripted = state.nextViewSave;
      state.nextViewSave = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as {
      list: string;
      name: string;
      state: Record<string, unknown>;
      expected_revision: number;
    };
    if (body.expected_revision !== (current?.revision ?? 0)) {
      return problem(route, { status: 409, code: 'PREFERENCE_REVISION_CONFLICT' });
    }
    if (state.savedViews.some((view) => view.id !== id && view.name === body.name)) {
      return problem(route, { status: 409, code: 'SAVED_VIEW_NAME_EXISTS' });
    }
    const saved: ScriptedView = {
      id,
      list: body.list,
      name: body.name,
      state: body.state,
      revision: body.expected_revision + 1,
      created_at: current?.created_at ?? '2026-11-04T00:00:00Z',
      updated_at: '2026-11-04T00:00:00Z',
    };
    state.savedViews = [...state.savedViews.filter((view) => view.id !== id), saved];
    return data(route, saved);
  });
  await page.route('**/api/v1/admin/tenant-settings', (route) => {
    if (state.audience !== 'admin') {
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    }
    const request = route.request();
    if (request.method() === 'GET') {
      return state.tenantSettingsProblem
        ? problem(route, state.tenantSettingsProblem)
        : data(route, state.tenantSettings);
    }
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    const body = request.postDataJSON() as { default_locale: string; expected_revision: number };
    if (body.expected_revision !== state.tenantSettings.revision) {
      return problem(route, { status: 409, code: 'PREFERENCE_REVISION_CONFLICT' });
    }
    state.tenantSettings = {
      default_locale: body.default_locale,
      revision: state.tenantSettings.revision + 1,
    };
    return data(route, state.tenantSettings);
  });
  await routeNodes(page, state, data, problem);
  await routeTasks(page, state, data, problem);
  await page.route('**/api/v1/admin/mail-deliveries?*', (route) => {
    if (state.audience !== 'admin')
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (state.mailProblem) return problem(route, state.mailProblem);
    const query = new URL(route.request().url()).searchParams;
    const wanted = query.get('state');
    const purpose = query.get('purpose');
    const recipient = query.get('recipient_prefix')?.toLowerCase();
    const matching = state.mail.filter(
      (item) =>
        (!wanted || item.state === wanted) &&
        (!purpose || item.purpose === purpose) &&
        (!recipient || item.recipient_email.startsWith(recipient)),
    );
    // The scripted list is newest first, as the server sends it by default.
    const items = query.get('order') === 'oldest' ? [...matching].reverse() : matching;
    return data(route, { items, next_cursor: null });
  });
  await page.route('**/api/v1/admin/invitations/*/revocations', (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextRevocation) {
      const scripted = state.nextRevocation;
      state.nextRevocation = null;
      return problem(route, scripted);
    }
    const id = new URL(request.url()).pathname.split('/').at(-2);
    state.revocations.push(request.postDataJSON() as Record<string, unknown>);
    state.mail = state.mail.map((item) => {
      if (item.invitation === null || item.invitation.id !== id) return item;
      return { ...item, invitation: { ...item.invitation, status: 'revoked' } };
    });
    return data(route, { id, status: 'revoked', version: 3 });
  });
  await page.route('**/api/v1/admin/mail-deliveries/*/resends', (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    state.resendKeys.push(request.headers()['idempotency-key'] ?? '');
    if (state.nextResend) {
      const scripted = state.nextResend;
      state.nextResend = null;
      return problem(route, scripted);
    }
    const id = new URL(request.url()).pathname.split('/').at(-2);
    const original = state.mail.find((item) => item.id === id);
    if (original?.state !== 'failed') {
      return problem(route, { status: 409, code: 'MAIL_DELIVERY_NOT_RESENDABLE' });
    }
    const queued: ScriptedMail = {
      ...original,
      id: `6f1c7a1e-0000-4000-8000-${String(100 + state.mail.length).padStart(12, '0')}`,
      state: 'queued',
      attempt_count: 0,
      next_attempt_at: '2026-11-04T10:00:00Z',
      error_category: null,
      completed_at: null,
    };
    state.mail = [queued, ...state.mail];
    return data(route, queued);
  });
  return state;
}
