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

/** A key issued or rotated, with its secret when this answer is the first. */
export type MintedGatewayKey = Schemas['MintedDistributionKey'];

/** One call the gateway admitted, with its outcome once reported. */
export type GatewayCall = Schemas['GatewayRequest'];

/** The longest a rotated key keeps working beside the key that replaced it, in seconds. */
export const maximumRotationOverlapSeconds = 7 * 24 * 60 * 60;

/**
 * Issues a key for one alias. The secret comes back once; the same `idempotencyKey` returns the
 * same key without it, so a lost answer cannot make two keys.
 */
export function issueGatewayKey(
  workspaceId: string,
  input: { aliasId: string; label: string; ttlSeconds: number; reason: string },
  idempotencyKey: string,
): Promise<MintedGatewayKey> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys',
    {
      path: { ...localCell, workspace_id: workspaceId },
      idempotencyKey,
      body: {
        alias_id: input.aliasId,
        label: input.label,
        ttl_seconds: input.ttlSeconds,
        reason: input.reason,
      },
    },
  );
}

/**
 * Replaces a key's secret: the successor keeps everything else, and the old key stops after
 * `overlapSeconds`, or at once when it is 0.
 */
export function rotateGatewayKey(
  workspaceId: string,
  keyId: string,
  input: { overlapSeconds: number; reason: string },
  idempotencyKey: string,
): Promise<MintedGatewayKey> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys/{resource_id}/rotate',
    {
      path: { ...localCell, workspace_id: workspaceId, resource_id: keyId },
      idempotencyKey,
      body: { overlap_seconds: input.overlapSeconds, reason: input.reason },
    },
  );
}

/** Stops a key at once. Revoking a revoked key changes nothing. */
export function revokeGatewayKey(
  workspaceId: string,
  keyId: string,
  reason: string,
): Promise<GatewayKey> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys/{resource_id}/revoke',
    { path: { ...localCell, workspace_id: workspaceId, resource_id: keyId }, body: { reason } },
  );
}

/** One page of the calls made with one key, newest first: the key's call log. */
export function listGatewayKeyCalls(
  workspaceId: string,
  keyId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['GatewayRequestPage']> {
  return client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/requests',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
      query: {
        api_key_id: keyId,
        limit: query.limit,
        ...(query.cursor ? { cursor: query.cursor } : {}),
      },
    },
  );
}
