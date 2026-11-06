// Scripted nodes and join tokens for browser flows.
import type { AgentPacing, JoinToken, Node, NodeCounts } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-06T08:00:00Z';

/** A joined node that is reporting. */
export const node: Node = {
  id: '6f1c7a1e-0000-4000-8000-000000000031',
  site_id: 'default',
  cell_id: 'default',
  tenant_id: '6f1c7a1e-0000-4000-8000-000000000003',
  workspace_id: '6f1c7a1e-0000-4000-8000-000000000002',
  name: 'gpu-a02',
  status: 'active',
  connectivity: 'online',
  version: 3,
  protocol_version: '1.0',
  facts: {
    os_id: 'ubuntu',
    os_version: '24.04',
    architecture: 'amd64',
    hostname: 'gpu-a02.lab.internal',
    logical_cpus: 128,
    memory_total_bytes: 1_500_000_000_000,
    disk_path: '/',
    disk_total_bytes: 4_000_000_000_000,
    disk_available_bytes: 2_500_000_000_000,
    observed_at: stamp,
    agent_version: '1.0.3',
  },
  connectivity_changed_at: stamp,
  last_heartbeat_at: stamp,
  created_at: stamp,
  updated_at: stamp,
  execution_profile: 'read_only',
  capabilities: ['collect_facts.v1'],
};

/** A node that stopped reporting. */
export const offlineNode: Node = {
  ...node,
  id: '6f1c7a1e-0000-4000-8000-000000000032',
  name: 'gpu-a03',
  connectivity: 'offline',
  facts: { ...node.facts, hostname: 'gpu-a03.lab.internal' },
};

/** A node an administrator disabled. */
export const disabledNode: Node = {
  ...node,
  id: '6f1c7a1e-0000-4000-8000-000000000033',
  name: 'cpu-s01',
  status: 'disabled',
  facts: { ...node.facts, hostname: 'cpu-s01.lab.internal' },
};

/** A token waiting for its node. */
export const joinToken: JoinToken = {
  id: '6f1c7a1e-0000-4000-8000-000000000050',
  tenant_id: node.tenant_id,
  workspace_id: node.workspace_id,
  node_name: 'edge-e01',
  status: 'pending',
  created_by: '6f1c7a1e-0000-4000-8000-000000000001',
  node_id: null,
  created_at: stamp,
  expires_at: '2026-11-06T09:00:00Z',
  execution_profile: 'read_only',
};

/** The default pacing in the default 30-second window, less a 5-second liveness flush. */
export const pacing: AgentPacing = {
  heartbeat_seconds: 10,
  jitter_percent: 10,
  reconnect_base_seconds: 1,
  reconnect_max_seconds: 60,
  version: 1,
  updated_at: stamp,
  online_window_seconds: 25,
};

/** What a flow changes to script nodes and join tokens. */
export interface NodeScript {
  /** Joined nodes, newest first. */
  nodes: Node[];
  /** Tokens that can still be used. */
  joinTokens: JoinToken[];
  nodesProblem: { status: number; code: string } | null;
  /** The next change of a node fails like this. */
  nextNodeChange: { status: number; code: string } | null;
  /** The next new token fails like this. */
  nextMint: { status: number; code: string } | null;
  /** The next revocation fails like this. */
  nextRevoke: { status: number; code: string } | null;
  /** Reasons given for changes and revocations, in order. */
  nodeReasons: string[];
  /** Whether each list read was marked as background, in order. */
  nodePolls: boolean[];
  pacing: AgentPacing;
  pacingProblem: { status: number; code: string } | null;
  /** The next pacing change fails like this. */
  nextPacingChange: { status: number; code: string } | null;
}

/** The state of a mock with nothing scripted. */
export function emptyNodeScript(): NodeScript {
  return {
    nodes: [node],
    joinTokens: [],
    nodesProblem: null,
    nextNodeChange: null,
    nextMint: null,
    nextRevoke: null,
    nodeReasons: [],
    nodePolls: [],
    pacing: { ...pacing },
    pacingProblem: null,
    nextPacingChange: null,
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

function counts(nodes: readonly Node[]): NodeCounts {
  const live = nodes.filter((item) => item.status !== 'removed');
  const of = (connectivity: Node['connectivity']) =>
    live.filter((item) => item.connectivity === connectivity).length;
  return {
    total: live.length,
    online: of('online'),
    unknown: of('unknown'),
    offline: of('offline'),
    disabled: live.filter((item) => item.status === 'disabled').length,
  };
}

/** Serves the node and join token routes of the admin entrance from `state`. */
export async function routeNodes(
  page: Page,
  state: NodeScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const base = '**/api/v1/sites/*/cells/*/admin';
  await page.route(`${base}/nodes?*`, (route) => {
    const request = route.request();
    state.nodePolls.push(request.headers()['x-ownsolid-background'] === 'true');
    if (state.nodesProblem) return problem(route, state.nodesProblem);
    const query = new URL(request.url()).searchParams;
    const connectivity = query.get('connectivity');
    const status = query.get('status');
    const prefix = query.get('name_prefix')?.toLowerCase();
    const limit = Number(query.get('limit') ?? state.nodes.length);
    const matching = state.nodes.filter(
      (item) =>
        (status ? item.status === status : item.status !== 'removed') &&
        (!connectivity || item.connectivity === connectivity) &&
        (!prefix || item.name.toLowerCase().startsWith(prefix)),
    );
    return data(route, {
      items: matching.slice(0, limit),
      next_cursor: matching.length > limit ? 'next' : null,
    });
  });
  await page.route(`${base}/nodes/summary`, (route) => data(route, counts(state.nodes)));
  await page.route(/\/admin\/nodes\/[0-9a-f-]{36}$/, async (route) => {
    const request = route.request();
    const id = new URL(request.url()).pathname.split('/').at(-1);
    const index = state.nodes.findIndex((item) => item.id === id);
    const found = state.nodes[index];
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (request.method() === 'GET') return data(route, found);
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextNodeChange) {
      const scripted = state.nextNodeChange;
      state.nextNodeChange = null;
      return problem(route, scripted);
    }
    const change = request.postDataJSON() as {
      name: string;
      status: Node['status'];
      version: number;
      reason: string;
    };
    if (change.version !== found.version) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    state.nodeReasons.push(change.reason);
    const updated: Node = {
      ...found,
      name: change.name,
      status: change.status,
      version: found.version + 1,
    };
    state.nodes[index] = updated;
    return data(route, updated);
  });
  await page.route(`${base}/node-join-tokens?*`, (route) =>
    data(route, { items: state.joinTokens, next_cursor: null }),
  );
  await page.route(`${base}/node-join-tokens`, (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextMint) {
      const scripted = state.nextMint;
      state.nextMint = null;
      return problem(route, scripted);
    }
    const body = request.postDataJSON() as { workspace_id: string; node_name: string };
    const minted: JoinToken = {
      ...joinToken,
      id: `6f1c7a1e-0000-4000-8000-${String(state.joinTokens.length + 60).padStart(12, '0')}`,
      workspace_id: body.workspace_id,
      node_name: body.node_name,
    };
    state.joinTokens.unshift(minted);
    return data(route, { ...minted, token: 'scripted-join-token-only-shown-once-0123456' }, 201);
  });
  await page.route(`${base}/node-join-tokens/*/revocations`, (route) => {
    const request = route.request();
    if (state.nextRevoke) {
      const scripted = state.nextRevoke;
      state.nextRevoke = null;
      return problem(route, scripted);
    }
    const id = new URL(request.url()).pathname.split('/').at(-2);
    const found = state.joinTokens.find((item) => item.id === id);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    state.nodeReasons.push((request.postDataJSON() as { reason: string }).reason);
    state.joinTokens = state.joinTokens.filter((item) => item !== found);
    return data(route, { ...found, status: 'revoked' });
  });
  await page.route(`${base}/agent-pacing`, (route) => {
    const request = route.request();
    if (request.method() === 'GET') {
      if (state.pacingProblem) return problem(route, state.pacingProblem);
      return data(route, state.pacing);
    }
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    if (state.nextPacingChange) {
      const scripted = state.nextPacingChange;
      state.nextPacingChange = null;
      return problem(route, scripted);
    }
    const change = request.postDataJSON() as AgentPacing & { reason: string };
    if (change.version !== state.pacing.version) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    if (
      3 * change.heartbeat_seconds * (100 + change.jitter_percent) >
      200 * state.pacing.online_window_seconds
    ) {
      return problem(route, { status: 409, code: 'AGENT_PACING_EXCEEDS_ONLINE_WINDOW' });
    }
    state.nodeReasons.push(change.reason);
    state.pacing = {
      heartbeat_seconds: change.heartbeat_seconds,
      jitter_percent: change.jitter_percent,
      reconnect_base_seconds: change.reconnect_base_seconds,
      reconnect_max_seconds: change.reconnect_max_seconds,
      version: state.pacing.version + 1,
      updated_at: '2026-11-06T09:00:00Z',
      online_window_seconds: state.pacing.online_window_seconds,
    };
    return data(route, state.pacing);
  });
}
