// The gateway's names for deployments and the keys that call them (api/openapi/v1/distribution).
import { localCell } from './cell.ts';
import { createClient } from './client.ts';
import type { distributionContract } from './gen/index.ts';
import type { Audience } from './identity.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = distributionContract.components['schemas'];

/** The name a gateway serves a deployment under. */
export type ModelAlias = Schemas['ModelAlias'];

/** A key that calls one alias. */
export type GatewayKey = Schemas['DistributionKey'];

const client = createClient<distributionContract.paths>();

/**
 * The paths each entrance reads keys through. The server takes who is asking from the session, so
 * a person's portal session gets only their own keys however the request is written.
 */
const keyPaths = {
  admin: {
    aliases:
      '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/aliases',
    keys: '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys',
    rotate:
      '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys/{resource_id}/rotate',
    revoke:
      '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/keys/{resource_id}/revoke',
    requests:
      '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/requests',
  },
  portal: {
    aliases:
      '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/distribution/aliases',
    keys: '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/distribution/keys',
    rotate:
      '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/distribution/keys/{resource_id}/rotate',
    revoke:
      '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/distribution/keys/{resource_id}/revoke',
    requests:
      '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/distribution/requests',
  },
} as const;

/** One page of a workspace's aliases, newest first. */
export function listModelAliases(
  audience: Audience,
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['ModelAliasPage']> {
  return client.get(keyPaths[audience].aliases, {
    ...options,
    path: { ...localCell, workspace_id: workspaceId },
    query: { limit: query.limit, ...(query.cursor ? { cursor: query.cursor } : {}) },
  });
}

/** One page of a workspace's gateway keys, newest first. */
export function listGatewayKeys(
  audience: Audience,
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['DistributionKeyPage']> {
  return client.get(keyPaths[audience].keys, {
    ...options,
    path: { ...localCell, workspace_id: workspaceId },
    query: { limit: query.limit, ...(query.cursor ? { cursor: query.cursor } : {}) },
  });
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
  audience: Audience,
  workspaceId: string,
  input: { aliasId: string; label: string; ttlSeconds: number; reason: string },
  idempotencyKey: string,
): Promise<MintedGatewayKey> {
  return client.post(keyPaths[audience].keys, {
    path: { ...localCell, workspace_id: workspaceId },
    idempotencyKey,
    body: {
      alias_id: input.aliasId,
      label: input.label,
      ttl_seconds: input.ttlSeconds,
      reason: input.reason,
    },
  });
}

/**
 * Replaces a key's secret: the successor keeps everything else, and the old key stops after
 * `overlapSeconds`, or at once when it is 0.
 */
export function rotateGatewayKey(
  audience: Audience,
  workspaceId: string,
  keyId: string,
  input: { overlapSeconds: number; reason: string },
  idempotencyKey: string,
): Promise<MintedGatewayKey> {
  return client.post(keyPaths[audience].rotate, {
    path: { ...localCell, workspace_id: workspaceId, resource_id: keyId },
    idempotencyKey,
    body: { overlap_seconds: input.overlapSeconds, reason: input.reason },
  });
}

/** Stops a key at once. Revoking a revoked key changes nothing. */
export function revokeGatewayKey(
  audience: Audience,
  workspaceId: string,
  keyId: string,
  reason: string,
): Promise<GatewayKey> {
  return client.post(keyPaths[audience].revoke, {
    path: { ...localCell, workspace_id: workspaceId, resource_id: keyId },
    body: { reason },
  });
}

/** One page of the calls made with one key, newest first: the key's call log. */
export function listGatewayKeyCalls(
  audience: Audience,
  workspaceId: string,
  keyId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['GatewayRequestPage']> {
  return client.get(keyPaths[audience].requests, {
    ...options,
    path: { ...localCell, workspace_id: workspaceId },
    query: {
      api_key_id: keyId,
      limit: query.limit,
      ...(query.cursor ? { cursor: query.cursor } : {}),
    },
  });
}

/** The gateway process registered for one node of a workspace. */
export type GatewayInstance = Schemas['GatewayInstance'];

/** A gateway instance just registered, with its credential when this answer is the first. */
export type ProvisionedGateway = Schemas['ProvisionedGateway'];

/** One page of a workspace's gateway instances, newest first. */
export function listGatewayInstances(
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<Schemas['GatewayInstancePage']> {
  return client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/gateways',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
      query: { limit: query.limit, ...(query.cursor ? { cursor: query.cursor } : {}) },
    },
  );
}

/**
 * Registers the gateway that serves one node. When the node's agent runs gateways, it starts this
 * one with a credential generated on the node, and `clientOrigin` is left out: the node declares
 * where callers reach it. Otherwise callers reach it at `clientOrigin`, and the credential the
 * gateway signs in with comes back once; the same `idempotencyKey` returns the instance without it.
 */
export function provisionGateway(
  workspaceId: string,
  input: { nodeId: string; name: string; clientOrigin: string | null; reason: string },
  idempotencyKey: string,
): Promise<ProvisionedGateway> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/gateways',
    {
      path: { ...localCell, workspace_id: workspaceId },
      idempotencyKey,
      body: {
        node_id: input.nodeId,
        name: input.name,
        ...(input.clientOrigin === null ? {} : { client_origin: input.clientOrigin }),
        reason: input.reason,
      },
    },
  );
}

/** Stops a gateway instance at once: its credential and every name it serves stop working. */
export function revokeGatewayInstance(
  workspaceId: string,
  gatewayId: string,
  reason: string,
): Promise<GatewayInstance> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/gateways/{resource_id}/revoke',
    { path: { ...localCell, workspace_id: workspaceId, resource_id: gatewayId }, body: { reason } },
  );
}

/**
 * Publishes one revision of a deployment under `name` on the gateway of its node, so keys can call
 * it. The same `idempotencyKey` returns the same name instead of a second one.
 */
export function publishModelAlias(
  workspaceId: string,
  input: {
    gatewayId: string;
    name: string;
    deploymentId: string;
    deploymentRevision: number;
    reason: string;
  },
  idempotencyKey: string,
): Promise<ModelAlias> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/aliases',
    {
      path: { ...localCell, workspace_id: workspaceId },
      idempotencyKey,
      body: {
        gateway_id: input.gatewayId,
        name: input.name,
        deployment_id: input.deploymentId,
        deployment_revision: input.deploymentRevision,
        reason: input.reason,
      },
    },
  );
}

/**
 * Stops publishing a name: every key that calls it stops working. `expectedRevision` is the
 * revision the person saw, so a change made meanwhile is refused instead of overwritten.
 */
export function disableModelAlias(
  workspaceId: string,
  aliasId: string,
  input: { expectedRevision: number; reason: string },
): Promise<ModelAlias> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/distribution/workspaces/{workspace_id}/aliases/{resource_id}/disable',
    {
      path: { ...localCell, workspace_id: workspaceId, resource_id: aliasId },
      body: { expected_revision: input.expectedRevision, reason: input.reason },
    },
  );
}
