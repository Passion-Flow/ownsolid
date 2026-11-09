// Nodes and the tokens that join them, for platform administrators (api/openapi/v1/nodes).
import { localCell } from './cell.ts';
import { createClient } from './client.ts';
import type { cellClustersContract, nodesContract } from './gen/index.ts';
import { ApiProblem } from './problem.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = nodesContract.components['schemas'];

/** One joined node. */
export type Node = Schemas['Node'];

/** One page of joined nodes. */
export type NodePage = Schemas['NodeList'];

/** Whether a node has reported lately. */
export type NodeConnectivity = Node['connectivity'];

/** What an administrator chose for a node. */
export type NodeStatus = Node['status'];

/** How many nodes are in each state. */
export type NodeCounts = Schemas['NodeCounts'];

/** A token that lets one new node join. */
export type JoinToken = Schemas['JoinToken'];

/** A new token, with the secret that is shown this once. */
export type MintedJoinToken = Schemas['MintedToken'];

/** How the cell's agents report and back off, and the window it must fit. */
export type AgentPacing = Schemas['AgentPacing'];

/** How much agent traffic this cell refuses. */
export type AgentShed = Schemas['AgentShed'];

/** What control refuses from agents, lowest priority first. */
export type ShedLevel = AgentShed['level'];

/** The values an administrator sets in {@link AgentPacing}. */
export type PacingValues = Pick<
  AgentPacing,
  'heartbeat_seconds' | 'jitter_percent' | 'reconnect_base_seconds' | 'reconnect_max_seconds'
>;

/** One task of one node. */
export type NodeTask = Schemas['Task'];

const client = createClient<nodesContract.paths>();
const cellClusters = createClient<cellClustersContract.paths>();

/** Checks that this cell can use managed Kubernetes, including before its first cluster exists. */
export async function checkKubernetesEnrollment(options?: RequestOptions): Promise<true> {
  const page = await cellClusters.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/cell-clusters',
    {
      ...options,
      path: localCell,
    },
  );
  if (!Array.isArray(page.items)) throw new ApiProblem('RESPONSE_INVALID');
  return true;
}

/** Lists joined nodes, newest first. Removed nodes appear only when asked for by status. */
export function listNodes(
  query: {
    limit: number;
    cursor?: string | undefined;
    connectivity?: NodeConnectivity | undefined;
    status?: NodeStatus | undefined;
    namePrefix?: string | undefined;
    /** Text anywhere in the name, in any case. */
    search?: string | undefined;
  },
  options?: RequestOptions,
): Promise<NodePage> {
  const { limit, cursor, connectivity, status, namePrefix, search } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes', {
    ...options,
    path: localCell,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(connectivity ? { connectivity } : {}),
      ...(status ? { status } : {}),
      ...(namePrefix ? { name_prefix: namePrefix } : {}),
      ...(search ? { q: search } : {}),
    },
  });
}

/** Counts the fleet's nodes by state. */
export function getNodeCounts(options?: RequestOptions): Promise<NodeCounts> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/summary', {
    ...options,
    path: localCell,
  });
}

/** Reads one node. */
export function getNode(id: string, options?: RequestOptions): Promise<Node> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}', {
    ...options,
    path: { ...localCell, node_id: id },
  });
}

/** Renames a node or changes its status. `version` refuses a change made on stale data. */
export function updateNode(
  id: string,
  change: { name: string; status: NodeStatus; version: number; reason: string },
  options?: RequestOptions,
): Promise<Node> {
  return client.patch('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}', {
    ...options,
    path: { ...localCell, node_id: id },
    body: change,
  });
}

/** Lists a node's latest tasks, newest first. */
export function listNodeTasks(
  id: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['TaskList']> {
  const { limit, cursor } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/tasks', {
    ...options,
    path: { ...localCell, node_id: id },
    query: { limit, ...(cursor ? { cursor } : {}) },
  });
}

/** Lists usable join tokens by default, or the token history when requested, newest first. */
export function listJoinTokens(
  query: { limit: number; cursor?: string | undefined; includeHistory?: boolean },
  options?: RequestOptions,
): Promise<Schemas['JoinTokenList']> {
  const { limit, cursor, includeHistory } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/node-join-tokens', {
    ...options,
    path: localCell,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(includeHistory ? {} : { status: 'pending' }),
    },
  });
}

/** Mints a token for one new node. Its secret can be read only from this answer. */
export function createJoinToken(
  input: {
    workspaceId: string;
    nodeName: string;
    /** What the node may do once joined, fixed from then on. Read-only when left out. */
    executionProfile?: MintedJoinToken['execution_profile'] | undefined;
  },
  options?: RequestOptions,
): Promise<MintedJoinToken> {
  return client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/node-join-tokens', {
    ...options,
    path: localCell,
    body: {
      workspace_id: input.workspaceId,
      node_name: input.nodeName,
      ...(input.executionProfile ? { execution_profile: input.executionProfile } : {}),
    },
  });
}

/** Stops a token that has not been used from working. */
export function revokeJoinToken(
  id: string,
  reason: string,
  options?: RequestOptions,
): Promise<JoinToken> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/node-join-tokens/{token_id}/revocations',
    { ...options, path: { ...localCell, token_id: id }, body: { reason } },
  );
}

/** Reads how this cell's agents are paced. */
export function getAgentPacing(options?: RequestOptions): Promise<AgentPacing> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/agent-pacing', {
    ...options,
    path: localCell,
  });
}

/** Changes the pacing. `version` refuses a change made on stale values. */
export function updateAgentPacing(
  values: PacingValues,
  version: number,
  reason: string,
  options?: RequestOptions,
): Promise<AgentPacing> {
  return client.put('/api/v1/sites/{site_id}/cells/{cell_id}/admin/agent-pacing', {
    ...options,
    path: localCell,
    body: { ...values, version, reason },
  });
}

/** Reads how much agent traffic this cell refuses. */
export function getAgentShed(options?: RequestOptions): Promise<AgentShed> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/agent-shed', {
    ...options,
    path: localCell,
  });
}

/** Changes the level. `version` refuses a change made on a stale level. */
export function updateAgentShed(
  level: ShedLevel,
  version: number,
  reason: string,
  options?: RequestOptions,
): Promise<AgentShed> {
  return client.put('/api/v1/sites/{site_id}/cells/{cell_id}/admin/agent-shed', {
    ...options,
    path: localCell,
    body: { level, version, reason },
  });
}

/**
 * The slowest heartbeat that keeps a healthy node online with `jitterPercent`: one and a half of
 * the longest interval must fit in the window, as control checks it.
 */
export function slowestHeartbeat(windowSeconds: number, jitterPercent: number): number {
  return Math.floor((200 * windowSeconds) / (3 * (100 + jitterPercent)));
}
