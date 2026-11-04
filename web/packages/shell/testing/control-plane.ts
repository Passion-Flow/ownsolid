// A scripted control plane for browser tests. It speaks the real wire format of the
// paths the shell uses, so specs exercise the production transport end to end.
import type { Page, Route } from '@playwright/test';

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

/** A scripted failure for one path. */
export interface ScriptedProblem {
  status: number;
  code: string;
  headers?: Record<string, string>;
}

/** Mutable state of the mock, so a spec can change it between steps. */
export interface ControlPlane {
  audience: 'admin' | 'portal';
  signedIn: boolean;
  /** Fails `/runtime-config.json` with a dropped connection while true. */
  unreachable: boolean;
  /** Answers the next sign-in with this failure instead of checking credentials. */
  nextSignIn: ScriptedProblem | null;
  /** Fails sign-out with this problem while set. */
  signOutProblem: ScriptedProblem | null;
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

function data(route: Route, value: unknown): Promise<void> {
  return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data: value }) });
}

/** Installs the mock on a page and returns its state. */
export async function mockControlPlane(
  page: Page,
  audience: ControlPlane['audience'],
): Promise<ControlPlane> {
  const state: ControlPlane = {
    audience,
    signedIn: false,
    unreachable: false,
    nextSignIn: null,
    signOutProblem: null,
  };
  const identity = () => ({ user, workspace, audience: state.audience });

  await page.route('**/runtime-config.json', (route) =>
    state.unreachable ? route.abort('connectionrefused') : data(route, runtimeConfig),
  );
  await page.route('**/api/v1/auth/csrf', (route) =>
    data(route, {
      token: 'csrf-token',
      expires_at: new Date(Date.now() + 3_600_000).toISOString(),
    }),
  );
  await page.route('**/api/v1/me', (route) =>
    state.signedIn
      ? data(route, identity())
      : problem(route, { status: 401, code: 'AUTH_REQUIRED' }),
  );
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
    if (body.email !== account.email || body.password !== account.password) {
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
  return state;
}
