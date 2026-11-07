// The gateway's names for deployments and the keys that call them (api/openapi/v1/distribution).
import { localCell } from './cell.ts';
import { createClient } from './client.ts';
import type { distributionContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = distributionContract.components['schemas'];

/** The name a gateway serves a deployment under. */
export type ModelAlias = Schemas['ModelAlias'];

/** A key that calls one alias. */
export type GatewayKey = Schemas['DistributionKey'];

const client = createClient<distributionContract.paths>();

/** One page of a workspace's aliases, newest first. */
export function listModelAliases(
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['ModelAliasPage']> {
  return client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/aliases',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
      query: { limit: query.limit, ...(query.cursor ? { cursor: query.cursor } : {}) },
    },
  );
}

/** One page of a workspace's gateway keys, newest first. */
export function listGatewayKeys(
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['DistributionKeyPage']> {
  return client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
      query: { limit: query.limit, ...(query.cursor ? { cursor: query.cursor } : {}) },
    },
  );
}
