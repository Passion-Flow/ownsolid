// Scripted gateway keys for browser flows. Writes follow the server's rules: a secret comes back
// once per idempotency key, a key has one successor, and a key's calls page by cursor, so a flow
// proves what the page really sends (docs/research/gateway-keys.md).
import type { GatewayCall, GatewayKey, ModelAlias } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';
import { readyModel } from './models.ts';
import { node } from './nodes.ts';
import { adminUser, memberUser } from './users.ts';

const workspace = readyModel.workspace_id;
const issued = '2026-11-01T08:00:00Z';
const day = 24 * 60 * 60 * 1000;

function later(days: number): string {
  return new Date(Date.now() + days * day).toISOString();
}

/** A model the gateway serves, which a new key can call. */
export const deploymentAlias: ModelAlias = {
  id: '6f1c7a1e-0000-4000-8000-000000000096',
  workspace_id: workspace,
  gateway_id: '6f1c7a1e-0000-4000-8000-0000000000a0',
  node_id: node.id,
  name: 'support-chat',
  client_origin: 'https://gateway.example.test',
  deployment_id: '6f1c7a1e-0000-4000-8000-000000000091',
  deployment_revision: 2,
  revision: 1,
  disabled_at: null,
  created_at: issued,
  available: true,
  unavailable_code: null,
  held_requests: 0,
};

/** A model on the gateway that cannot take a key now: its deployment is stopped. */
export const unavailableAlias: ModelAlias = {
  ...deploymentAlias,
  id: '6f1c7a1e-0000-4000-8000-0000000000a1',
  name: 'archive-search',
  available: false,
  unavailable_code: 'MODEL_ALIAS_UNAVAILABLE',
};

/** A key in use, which calls {@link deploymentAlias}. */
export const deploymentKey: GatewayKey = {
  id: '6f1c7a1e-0000-4000-8000-000000000097',
  workspace_id: workspace,
  alias_id: deploymentAlias.id,
  alias_name: deploymentAlias.name,
  label: 'Support bot',
  prefix: 'pk1.6f1c7a1e-0000-4000-8000-000000000097',
  created_by: adminUser.id,
  state: 'active',
  created_at: issued,
  expires_at: later(60),
  last_used_at: '2026-11-07T09:30:00Z',
  revoked_at: null,
  rotated_from: null,
  replaced_by: null,
};

/** A key ending in two days, held by another person. */
export const expiringKey: GatewayKey = {
  ...deploymentKey,
  id: '6f1c7a1e-0000-4000-8000-0000000000a2',
  label: 'Nightly report',
  prefix: 'pk1.6f1c7a1e-0000-4000-8000-0000000000a2',
  created_by: memberUser.id,
  expires_at: later(2),
};

/** A key never used. */
export const unusedKey: GatewayKey = {
  ...deploymentKey,
  id: '6f1c7a1e-0000-4000-8000-0000000000a3',
  label: 'Evaluation script',
  prefix: 'pk1.6f1c7a1e-0000-4000-8000-0000000000a3',
  last_used_at: null,
};

/** A key that was revoked. */
export const revokedKey: GatewayKey = {
  ...deploymentKey,
  id: '6f1c7a1e-0000-4000-8000-0000000000a4',
  label: 'Old pilot',
  prefix: 'pk1.6f1c7a1e-0000-4000-8000-0000000000a4',
  state: 'revoked',
  revoked_at: '2026-11-05T10:00:00Z',
};

/** A call made with {@link deploymentKey}, numbered from the newest. */
export function keyCall(index: number, key: GatewayKey = deploymentKey): GatewayCall {
  const id = `6f1c7a1e-0000-4000-8001-${String(index).padStart(12, '0')}`;
  const admitted = new Date(Date.parse('2026-11-07T09:30:00Z') - index * 60_000).toISOString();
  return {
    id,
    workspace_id: workspace,
    gateway_id: deploymentAlias.gateway_id,
    node_id: node.id,
    api_key_id: key.id,
    actor_id: key.created_by,
    alias_id: key.alias_id,
    alias_name: key.alias_name,
    deployment_id: deploymentAlias.deployment_id,
    deployment_revision: 2,
    state: 'reconciled',
    capacity_held: false,
    stream: false,
    admitted_at: admitted,
    deadline_at: admitted,
    reported_at: admitted,
    reconciled_at: admitted,
    active_recovery_id: null,
    outcome: {
      request_id: id,
      status: index % 5 === 4 ? 'upstream_timeout' : 'succeeded',
      error_code: index % 5 === 4 ? 'upstream_timeout' : null,
      usage:
        index % 5 === 4 ? null : { prompt_tokens: 40, completion_tokens: 12, total_tokens: 52 },
      elapsed_milliseconds: 1_250,
      output_started: index % 5 !== 4,
    },
  };
}

interface ScriptedFailure {
  status: number;
  code: string;
}

/** The keys and calls a flow starts from, and what it makes go wrong. */
export interface GatewayScript {
  aliases: ModelAlias[];
  gatewayKeys: GatewayKey[];
  keyCalls: GatewayCall[];
  keysProblem: ScriptedFailure | null;
  callsProblem: ScriptedFailure | null;
  /** Failures of the next writes, in order; `lost` writes and drops the answer. */
  keyWriteFailures: (ScriptedFailure | 'lost')[];
  /** Every write the page sent: its path's last part, its idempotency key and its body. */
  keyWrites: { kind: 'issue' | 'rotate' | 'revoke'; key: string; body: Record<string, unknown> }[];
}

/** A script with no key. */
export function emptyGatewayScript(): GatewayScript {
  return {
    aliases: [],
    gatewayKeys: [],
    keyCalls: [],
    keysProblem: null,
    callsProblem: null,
    keyWriteFailures: [],
    keyWrites: [],
  };
}

type Reply = (route: Route, value: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: ScriptedFailure) => Promise<void>;

function pathPart(route: Route, fromEnd: number): string {
  return new URL(route.request().url()).pathname.split('/').at(-fromEnd) ?? '';
}

/** The secret a scripted key gets: its prefix and a token derived from the request's key. */
function secretOf(key: GatewayKey, intent: string): string {
  return `${key.prefix}.${intent.replaceAll('-', '')}`;
}

/** Routes the distribution contract's aliases, keys and calls onto `state`. */
export async function routeGateway(
  page: Page,
  state: GatewayScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const base = /\/admin\/distribution\/workspaces\/[0-9a-f-]{36}/;
  const madeBy = new Map<string, string>();
  const written = (route: Route, kind: GatewayScript['keyWrites'][number]['kind']) => {
    const request = route.request();
    const intent = request.headers()['idempotency-key'] ?? '';
    const body = request.postDataJSON() as Record<string, unknown>;
    state.keyWrites.push({ kind, key: intent, body });
    return { intent, body, failure: state.keyWriteFailures.shift() };
  };
  await page.route(new RegExp(`${base.source}/aliases\\?`), (route) =>
    data(route, { items: state.aliases, next_cursor: null }),
  );
  await page.route(new RegExp(`${base.source}/keys\\?`), (route) => {
    if (state.keysProblem) return problem(route, state.keysProblem);
    return data(route, { items: state.gatewayKeys, next_cursor: null });
  });
  await page.route(new RegExp(`${base.source}/keys$`), (route) => {
    const { intent, body, failure } = written(route, 'issue');
    if (failure && failure !== 'lost') return problem(route, failure);
    if (!intent || typeof body.reason !== 'string' || body.reason === '') {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const alias = state.aliases.find((item) => item.id === body.alias_id);
    if (!alias?.available) return problem(route, { status: 409, code: 'MODEL_ALIAS_UNAVAILABLE' });
    const id = `6f1c7a1e-0000-4000-8002-${intent.replaceAll('-', '').slice(0, 12)}`;
    let key = state.gatewayKeys.find((item) => item.id === id);
    const first = !key;
    if (!key) {
      key = {
        ...deploymentKey,
        id,
        prefix: `pk1.${id}`,
        alias_id: alias.id,
        alias_name: alias.name,
        label: String(body.label),
        created_at: new Date().toISOString(),
        expires_at: new Date(Date.now() + Number(body.ttl_seconds) * 1000).toISOString(),
        last_used_at: null,
      };
      state.gatewayKeys.unshift(key);
      madeBy.set(id, intent);
    }
    if (failure === 'lost') return route.abort('connectionreset');
    return data(
      route,
      first
        ? { key, secret: secretOf(key, intent), secret_available: true }
        : { key, secret: null, secret_available: false },
      first ? 201 : 200,
    );
  });
  await page.route(new RegExp(`${base.source}/keys/[0-9a-f-]{36}/rotate$`), (route) => {
    const { intent, body, failure } = written(route, 'rotate');
    if (failure && failure !== 'lost') return problem(route, failure);
    const index = state.gatewayKeys.findIndex((item) => item.id === pathPart(route, 2));
    const old = state.gatewayKeys[index];
    if (!old) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (!intent || typeof body.reason !== 'string' || body.reason === '') {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const existing = state.gatewayKeys.find((item) => item.rotated_from === old.id);
    if (existing) {
      if (madeBy.get(existing.id) !== intent) {
        return problem(route, { status: 409, code: 'API_KEY_ALREADY_ROTATED' });
      }
      return data(route, { key: existing, secret: null, secret_available: false });
    }
    if (old.state !== 'active') return problem(route, { status: 409, code: 'API_KEY_NOT_ACTIVE' });
    const id = `6f1c7a1e-0000-4000-8003-${intent.replaceAll('-', '').slice(0, 12)}`;
    const successor: GatewayKey = {
      ...old,
      id,
      prefix: `pk1.${id}`,
      created_at: new Date().toISOString(),
      expires_at: new Date(
        Date.now() + Date.parse(old.expires_at) - Date.parse(old.created_at),
      ).toISOString(),
      last_used_at: null,
      rotated_from: old.id,
      replaced_by: null,
    };
    const overlap = Number(body.overlap_seconds);
    state.gatewayKeys[index] =
      overlap === 0
        ? { ...old, state: 'revoked', revoked_at: new Date().toISOString(), replaced_by: id }
        : {
            ...old,
            replaced_by: id,
            expires_at: new Date(
              Math.min(Date.parse(old.expires_at), Date.now() + overlap * 1000),
            ).toISOString(),
          };
    state.gatewayKeys.unshift(successor);
    madeBy.set(id, intent);
    if (failure === 'lost') return route.abort('connectionreset');
    return data(
      route,
      { key: successor, secret: secretOf(successor, intent), secret_available: true },
      201,
    );
  });
  await page.route(new RegExp(`${base.source}/keys/[0-9a-f-]{36}/revoke$`), (route) => {
    const { body, failure } = written(route, 'revoke');
    if (failure && failure !== 'lost') return problem(route, failure);
    const index = state.gatewayKeys.findIndex((item) => item.id === pathPart(route, 2));
    const found = state.gatewayKeys[index];
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (typeof body.reason !== 'string' || body.reason === '') {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const revoked: GatewayKey =
      found.state === 'revoked'
        ? found
        : { ...found, state: 'revoked', revoked_at: new Date().toISOString() };
    state.gatewayKeys[index] = revoked;
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, revoked);
  });
  await page.route(new RegExp(`${base.source}/requests\\?`), (route) => {
    if (state.callsProblem) return problem(route, state.callsProblem);
    const query = new URL(route.request().url()).searchParams;
    const key = query.get('api_key_id');
    const limit = Number(query.get('limit') ?? '20');
    const start = Number(query.get('cursor') ?? '0');
    const mine = state.keyCalls.filter((call) => key === null || call.api_key_id === key);
    const items = mine.slice(start, start + limit);
    const next = start + limit < mine.length ? String(start + limit) : null;
    return data(route, { items, next_cursor: next });
  });
}
