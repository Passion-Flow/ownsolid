// Scripted gateway keys for browser flows. Writes follow the server's rules: a secret comes back
// once per idempotency key, a key has one successor, and a key's calls page by cursor, so a flow
// proves what the page really sends (docs/research/gateway-keys.md).
import type { GatewayCall, GatewayInstance, GatewayKey, ModelAlias } from '@ownsolid/api';
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

/** The gateway that serves {@link node} in the workspace. */
export const nodeGatewayInstance: GatewayInstance = {
  id: '6f1c7a1e-0000-4000-8000-0000000000a0',
  workspace_id: workspace,
  node_id: node.id,
  name: 'rack7 gateway',
  client_origin: 'https://gateway.example.test',
  created_by: adminUser.id,
  state: 'active',
  created_at: issued,
  revoked_at: null,
  delivery: 'manual',
  delivery_state: null,
  delivery_error_code: null,
  certificate_not_after: null,
};

/** Where callers reach a gateway a scripted node's agent runs: the origin the node declares. */
export const agentGatewayOrigin = 'https://gw.rack7.example:18443';

/** A model the gateway serves, which a new key can call. */
export const deploymentAlias: ModelAlias = {
  id: '6f1c7a1e-0000-4000-8000-000000000096',
  workspace_id: workspace,
  gateway_id: nodeGatewayInstance.id,
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
  gateways: GatewayInstance[];
  aliases: ModelAlias[];
  gatewayKeys: GatewayKey[];
  keyCalls: GatewayCall[];
  keysProblem: ScriptedFailure | null;
  callsProblem: ScriptedFailure | null;
  /** Failures of the next writes, in order; `lost` writes and drops the answer. */
  keyWriteFailures: (ScriptedFailure | 'lost')[];
  /** Every write the page sent: its path's last part, its idempotency key and its body. */
  keyWrites: { kind: 'issue' | 'rotate' | 'revoke'; key: string; body: Record<string, unknown> }[];
  /** Failures of the next gateway or name writes, in order; `lost` writes and drops the answer. */
  publishFailures: (ScriptedFailure | 'lost')[];
  /** Every gateway or name write the page sent, with its idempotency key and body. */
  publishWrites: {
    kind: 'provision' | 'publish' | 'disable' | 'revokeGateway';
    key: string;
    body: Record<string, unknown>;
  }[];
  /** Nodes whose agent runs their gateway (ADR 0052); other nodes take an administrator's. */
  agentNodes: string[];
  /**
   * How the agent's next starts end, in order: `running`, or the error code the start failed with.
   * A start is seen starting by one read of the list and ends at the next; unscripted ones run.
   */
  agentStarts: string[];
}

/** A script with no key. */
export function emptyGatewayScript(): GatewayScript {
  return {
    gateways: [],
    aliases: [],
    gatewayKeys: [],
    keyCalls: [],
    keysProblem: null,
    callsProblem: null,
    keyWriteFailures: [],
    keyWrites: [],
    publishFailures: [],
    publishWrites: [],
    agentNodes: [],
    agentStarts: [],
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

/** The credential a scripted gateway gets, derived from the request's key. */
function credentialOf(gateway: GatewayInstance, intent: string): string {
  return `gw1.${gateway.id}.${intent.replaceAll('-', '')}`;
}

/**
 * Routes gateway instances and publishing names: one active gateway per node, a credential once
 * per idempotency key, a name used once in a workspace, and a disable that names the revision it
 * saw (docs/research/gateway-publishing.md).
 */
async function routePublishing(page: Page, state: GatewayScript, data: Reply, problem: Refuse) {
  const base = /\/admin\/distribution\/workspaces\/[0-9a-f-]{36}/;
  const madeBy = new Map<string, string>();
  const written = (route: Route, kind: GatewayScript['publishWrites'][number]['kind']) => {
    const request = route.request();
    const intent = request.headers()['idempotency-key'] ?? '';
    const body = request.postDataJSON() as Record<string, unknown>;
    state.publishWrites.push({ kind, key: intent, body });
    const reason = typeof body.reason === 'string' && body.reason !== '';
    return { intent, body, reason, failure: state.publishFailures.shift() };
  };
  const seenStarting = new Set<string>();
  await page.route(new RegExp(`${base.source}/gateways\\?`), (route) => {
    state.gateways = state.gateways.map((gateway) => {
      if (gateway.delivery !== 'agent' || gateway.delivery_state !== 'starting') return gateway;
      if (!seenStarting.has(gateway.id)) {
        seenStarting.add(gateway.id);
        return gateway;
      }
      const outcome = state.agentStarts.shift() ?? 'running';
      return outcome === 'running'
        ? {
            ...gateway,
            delivery_state: 'running',
            client_origin: agentGatewayOrigin,
            certificate_not_after: later(365),
          }
        : { ...gateway, delivery_state: 'failed', delivery_error_code: outcome };
    });
    return data(route, { items: state.gateways, next_cursor: null });
  });
  await page.route(new RegExp(`${base.source}/gateways$`), (route) => {
    const { intent, body, reason, failure } = written(route, 'provision');
    if (failure && failure !== 'lost') return problem(route, failure);
    const delivered = state.agentNodes.includes(String(body.node_id));
    if (delivered && 'client_origin' in body) {
      return problem(route, { status: 400, code: 'GATEWAY_ORIGIN_DECLARED_BY_NODE' });
    }
    if (
      !intent ||
      !reason ||
      (!delivered && !/^https:\/\/[^/]+$/.test(String(body.client_origin)))
    ) {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const replayed = state.gateways.find((item) => madeBy.get(item.id) === intent);
    if (replayed) return data(route, { gateway: replayed, secret: null, secret_available: false });
    if (state.gateways.some((item) => item.node_id === body.node_id && item.state === 'active')) {
      return problem(route, { status: 409, code: 'GATEWAY_INSTANCE_EXISTS' });
    }
    const gateway: GatewayInstance = {
      ...nodeGatewayInstance,
      id: `6f1c7a1e-0000-4000-8004-${intent.replaceAll('-', '').slice(0, 12)}`,
      node_id: String(body.node_id),
      name: String(body.name),
      client_origin: delivered ? null : String(body.client_origin),
      created_at: new Date().toISOString(),
      ...(delivered ? { delivery: 'agent', delivery_state: 'starting' } : {}),
    };
    state.gateways.unshift(gateway);
    madeBy.set(gateway.id, intent);
    if (failure === 'lost') return route.abort('connectionreset');
    // The agent generates a delivered gateway's credential on the node; nobody receives it.
    const secret = delivered ? null : credentialOf(gateway, intent);
    return data(route, { gateway, secret, secret_available: secret !== null }, 201);
  });
  await page.route(new RegExp(`${base.source}/gateways/[0-9a-f-]{36}/revoke$`), (route) => {
    const { reason, failure } = written(route, 'revokeGateway');
    if (failure && failure !== 'lost') return problem(route, failure);
    const index = state.gateways.findIndex((item) => item.id === pathPart(route, 2));
    const found = state.gateways[index];
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (!reason) return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    const stops =
      found.delivery === 'agent' &&
      (found.delivery_state === 'running' || found.delivery_state === 'failed');
    const revoked: GatewayInstance = {
      ...(found.state === 'active'
        ? { ...found, state: 'revoked', revoked_at: new Date().toISOString() }
        : found),
      ...(stops ? { delivery_state: 'stopping', delivery_error_code: null } : {}),
    };
    state.gateways[index] = revoked;
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, revoked);
  });
  await page.route(new RegExp(`${base.source}/aliases$`), (route) => {
    const { intent, body, reason, failure } = written(route, 'publish');
    if (failure && failure !== 'lost') return problem(route, failure);
    if (!intent || !reason || !/^[a-z][a-z0-9.-]{0,63}$/.test(String(body.name))) {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const replayed = state.aliases.find((item) => madeBy.get(item.id) === intent);
    if (replayed) return data(route, replayed);
    const gateway = state.gateways.find((item) => item.id === body.gateway_id);
    if (!gateway) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (gateway.state !== 'active') {
      return problem(route, { status: 409, code: 'GATEWAY_INSTANCE_REVOKED' });
    }
    if (gateway.delivery === 'agent' && gateway.delivery_state !== 'running') {
      return problem(route, { status: 409, code: 'GATEWAY_NOT_STARTED' });
    }
    if (state.aliases.some((item) => item.name === body.name)) {
      return problem(route, { status: 409, code: 'MODEL_ALIAS_EXISTS' });
    }
    const alias: ModelAlias = {
      ...deploymentAlias,
      id: `6f1c7a1e-0000-4000-8005-${intent.replaceAll('-', '').slice(0, 12)}`,
      gateway_id: gateway.id,
      node_id: gateway.node_id,
      client_origin: gateway.client_origin ?? '',
      name: String(body.name),
      deployment_id: String(body.deployment_id),
      deployment_revision: Number(body.deployment_revision),
      created_at: new Date().toISOString(),
    };
    state.aliases.unshift(alias);
    madeBy.set(alias.id, intent);
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, alias);
  });
  await page.route(new RegExp(`${base.source}/aliases/[0-9a-f-]{36}/disable$`), (route) => {
    const { body, reason, failure } = written(route, 'disable');
    if (failure && failure !== 'lost') return problem(route, failure);
    const index = state.aliases.findIndex((item) => item.id === pathPart(route, 2));
    const found = state.aliases[index];
    if (!found) return problem(route, { status: 404, code: 'MODEL_ALIAS_NOT_FOUND' });
    if (!reason) return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    if (body.expected_revision !== found.revision) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    const disabled: ModelAlias = {
      ...found,
      revision: found.revision + 1,
      disabled_at: new Date().toISOString(),
      available: false,
      unavailable_code: 'MODEL_ALIAS_UNAVAILABLE',
    };
    state.aliases[index] = disabled;
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, disabled);
  });
}

/** Routes the distribution contract's gateways, aliases, keys and calls onto `state`. */
export async function routeGateway(
  page: Page,
  state: GatewayScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  // The console's and the portal's paths to the same keys.
  const base =
    /\/(?:admin\/distribution\/workspaces\/[0-9a-f-]{36}|workspaces\/[0-9a-f-]{36}\/distribution)/;
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
  await routePublishing(page, state, data, problem);
}
