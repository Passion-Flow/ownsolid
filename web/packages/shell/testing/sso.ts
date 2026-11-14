// Scripted single sign-on connectors and the installation's configuration, for browser flows.
import type { ConnectorCheckOutcome, ManagedConnector, SsoConfiguration } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-04T00:00:00Z';

/** The approved Gitea the mock's configuration names. */
export const giteaOrigin = 'https://git.example.test';

/** A connector people sign in with. */
export const activeConnector: ManagedConnector = {
  id: '6f1c7a1e-0000-4000-8000-000000000301',
  name: 'Company Gitea',
  provider: 'gitea',
  origin: giteaOrigin,
  client_id: '2556a814-4038-45e7-bd95-d82cd250d5e9',
  enabled: true,
  revision: 2,
  auth_version: 2,
  created_at: stamp,
  updated_at: stamp,
};

/** A connector whose origin left the configuration. */
export const retiredConnector: ManagedConnector = {
  ...activeConnector,
  id: '6f1c7a1e-0000-4000-8000-000000000302',
  name: 'Old Gitea',
  origin: 'https://old-git.example.test',
  enabled: false,
  revision: 1,
  auth_version: 1,
};

/** Single sign-on on, one approved provider, one entrance origin. */
export const ssoOn: SsoConfiguration = {
  enabled: true,
  providers: [{ provider: 'gitea', origin: giteaOrigin }],
  callback_origins: ['https://ownsolid.example.test'],
};

/** What a flow changes to script single sign-on. */
export interface SsoScript {
  ssoConfiguration: SsoConfiguration;
  ssoConfigurationProblem: { status: number; code: string } | null;
  ssoConnectorList: ManagedConnector[];
  ssoConnectorsProblem: { status: number; code: string } | null;
  /** What checking each connector finds; `passed` when not named. */
  ssoChecks: Record<string, ConnectorCheckOutcome>;
  /** The next write or check fails like this. */
  nextSsoChange: { status: number; code: string } | null;
  /** Reasons given for changes, in order. */
  ssoReasons: string[];
  /** Idempotency-Key of each write, in order. */
  ssoKeys: string[];
  /** Bodies of connector writes, in order, without secrets. */
  ssoWrites: Record<string, unknown>[];
}

/** The state of a mock with nothing scripted. */
export function emptySsoScript(): SsoScript {
  return {
    ssoConfiguration: ssoOn,
    ssoConfigurationProblem: null,
    ssoConnectorList: [],
    ssoConnectorsProblem: null,
    ssoChecks: {},
    nextSsoChange: null,
    ssoReasons: [],
    ssoKeys: [],
    ssoWrites: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

/** Serves the connector API from `state`, with the server's rules. */
export async function routeSso(
  page: Page,
  state: SsoScript & { audience: string },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const refused = (route: Route) => {
    if (state.audience !== 'admin') return { status: 404, code: 'RESOURCE_NOT_FOUND' };
    if (route.request().headers()['x-csrf-token'] !== 'csrf-token') {
      return { status: 403, code: 'CSRF_INVALID' };
    }
    const scripted = state.nextSsoChange;
    state.nextSsoChange = null;
    return scripted;
  };
  const record = (route: Route, body: Record<string, unknown>) => {
    state.ssoKeys.push(route.request().headers()['idempotency-key'] ?? '');
    state.ssoReasons.push(String(body.reason));
    const { client_secret: secret, ...rest } = body;
    state.ssoWrites.push({ ...rest, has_secret: typeof secret === 'string' });
  };
  await page.route('**/api/v1/admin/sso/configuration', (route) => {
    if (state.audience !== 'admin') {
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    }
    if (state.ssoConfigurationProblem) return problem(route, state.ssoConfigurationProblem);
    return data(route, state.ssoConfiguration);
  });
  await page.route(/\/api\/v1\/admin\/sso\/connectors(\?.*)?$/, (route) => {
    const request = route.request();
    if (request.method() === 'GET') {
      if (state.audience !== 'admin') {
        return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      }
      if (state.ssoConnectorsProblem) return problem(route, state.ssoConnectorsProblem);
      return data(route, { items: [...state.ssoConnectorList].reverse(), next_cursor: null });
    }
    const failure = refused(route);
    if (failure) return problem(route, failure);
    const body = request.postDataJSON() as Record<string, unknown> & ManagedConnector;
    if (!state.ssoConfiguration.enabled) {
      return problem(route, { status: 503, code: 'SSO_DISABLED' });
    }
    if (!state.ssoConfiguration.providers.some((p) => p.origin === body.origin)) {
      return problem(route, { status: 503, code: 'SSO_PROVIDER_DESTINATION_REJECTED' });
    }
    record(route, body);
    const created: ManagedConnector = {
      id: body.id,
      name: body.name,
      provider: 'gitea',
      origin: body.origin,
      client_id: body.client_id,
      enabled: body.enabled,
      revision: 1,
      auth_version: 1,
      created_at: stamp,
      updated_at: stamp,
    };
    state.ssoConnectorList.push(created);
    return data(route, created, 201);
  });
  await page.route(/\/api\/v1\/admin\/sso\/connectors\/[0-9a-f-]{36}$/, (route) => {
    const failure = refused(route);
    if (failure) return problem(route, failure);
    const id = new URL(route.request().url()).pathname.split('/').at(-1) ?? '';
    const found = state.ssoConnectorList.find((item) => item.id === id);
    if (!found) return problem(route, { status: 404, code: 'SSO_CONNECTOR_NOT_FOUND' });
    const body = route.request().postDataJSON() as Record<string, unknown> & {
      revision: number;
      name: string;
      enabled: boolean;
      client_id: string | null;
    };
    if (body.revision !== found.revision) {
      return problem(route, { status: 409, code: 'SSO_REVISION_CONFLICT' });
    }
    if (!state.ssoConfiguration.enabled && (body.enabled || body.client_id !== null)) {
      return problem(route, { status: 503, code: 'SSO_DISABLED' });
    }
    record(route, body);
    if (found.enabled !== body.enabled || body.client_id !== null) found.auth_version += 1;
    found.name = body.name;
    found.enabled = body.enabled;
    found.client_id = body.client_id ?? found.client_id;
    found.revision += 1;
    return data(route, found);
  });
  await page.route(/\/api\/v1\/admin\/sso\/connectors\/[0-9a-f-]{36}\/tests$/, (route) => {
    const failure = refused(route);
    if (failure) return problem(route, failure);
    const id = new URL(route.request().url()).pathname.split('/').at(-2) ?? '';
    const found = state.ssoConnectorList.find((item) => item.id === id);
    if (!found) return problem(route, { status: 404, code: 'SSO_CONNECTOR_NOT_FOUND' });
    if (!state.ssoConfiguration.enabled) {
      return problem(route, { status: 503, code: 'SSO_DISABLED' });
    }
    const approved = state.ssoConfiguration.providers.some((p) => p.origin === found.origin);
    const outcome = approved ? (state.ssoChecks[id] ?? 'passed') : 'not_configured';
    const origin = state.ssoConfiguration.callback_origins[0] ?? '';
    return data(route, {
      connector_id: id,
      outcome,
      callback_url:
        outcome === 'callback_not_registered' ? `${origin}/api/v1/auth/sso/${id}/callback` : null,
      provider_error: outcome === 'unexpected_answer' ? 'invalid_grant' : null,
      checked_at: '2026-11-07T08:00:00Z',
    });
  });
}
