// Nodes and the tokens that join them, for platform administrators (api/openapi/v1/nodes).
import { createClient } from './client.ts';
import type { nodesContract } from './gen/index.ts';
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

/** One task of one node. */
export type NodeTask = Schemas['Task'];

// Nodes live in a cell. Every installation has the one local cell until ARC-202.
const localCell = { site_id: 'default', cell_id: 'default' };

const client = createClient<nodesContract.paths>();

/** Lists joined nodes, newest first. Removed nodes appear only when asked for by status. */
export function listNodes(
  query: {
    limit: number;
    cursor?: string | undefined;
    connectivity?: NodeConnectivity | undefined;
    status?: NodeStatus | undefined;
    namePrefix?: string | undefined;
  },
  options?: RequestOptions,
): Promise<NodePage> {
  const { limit, cursor, connectivity, status, namePrefix } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes', {
    ...options,
    path: localCell,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(connectivity ? { connectivity } : {}),
      ...(status ? { status } : {}),
      ...(namePrefix ? { name_prefix: namePrefix } : {}),
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
  query: { limit: number },
  options?: RequestOptions,
): Promise<Schemas['TaskList']> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/tasks', {
    ...options,
    path: { ...localCell, node_id: id },
    query,
  });
}

/** Lists join tokens that can still be used, newest first. */
export function listJoinTokens(
  query: { limit: number },
  options?: RequestOptions,
): Promise<Schemas['JoinTokenList']> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/node-join-tokens', {
    ...options,
    path: localCell,
    query: { ...query, status: 'pending' },
  });
}

/** Mints a token for one new node. Its secret can be read only from this answer. */
export function createJoinToken(
  input: { workspaceId: string; nodeName: string },
  options?: RequestOptions,
): Promise<MintedJoinToken> {
  return client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/node-join-tokens', {
    ...options,
    path: localCell,
    body: { workspace_id: input.workspaceId, node_name: input.nodeName },
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
