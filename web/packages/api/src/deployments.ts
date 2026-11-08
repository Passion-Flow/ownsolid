// Models served on GPUs, for platform administrators (api/openapi/v1/deployments).
import { localCell } from './cell.ts';
import { createClient } from './client.ts';
import type { deploymentsContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = deploymentsContract.components['schemas'];

/** A model served on one GPU of one node. */
export type Deployment = Schemas['Deployment'];

/** What the agent last reported about a deployment. */
export type DeploymentObservation = NonNullable<Deployment['observation']>;

/** What a person can ask of a deployment. */
export type DeploymentAction = Schemas['DeploymentAction']['action'];

/** Where a model can be deployed now, judged by the checks a deployment makes. */
export type Placement = Schemas['Placement'];

/** A node that reported its GPUs, as the placement judged it. */
export type PlacementNode = Schemas['PlacementNode'];

/** One GPU as the placement judged it. */
export type PlacementGpu = Schemas['PlacementGPU'];

/** What a node last reported about its GPUs. */
export type InferenceNodeState = Schemas['InferenceNodeState'];

/** A task the control plane queued on a node. */
export type InferenceTask = Schemas['Task'];

const client = createClient<deploymentsContract.paths>();

/** Lists a workspace's deployments that are not removed, newest first. */
export async function listDeployments(
  workspaceId: string,
  filter: { modelId?: string | undefined } = {},
  options?: RequestOptions,
): Promise<Deployment[]> {
  const page = await client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/deployments', {
    ...options,
    path: localCell,
    query: {
      workspace_id: workspaceId,
      retained: true,
      ...(filter.modelId ? { model_id: filter.modelId } : {}),
    },
  });
  return page.items;
}

/** Lists the deployments of a workspace the person belongs to, for the portal. */
export async function listWorkspaceDeployments(
  workspaceId: string,
  options?: RequestOptions,
): Promise<Deployment[]> {
  const page = await client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/deployments',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
    },
  );
  return page.items;
}

/** Reads one deployment. */
export function getDeployment(id: string, options?: RequestOptions): Promise<Deployment> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/deployments/{deployment_id}', {
    ...options,
    path: { ...localCell, deployment_id: id },
  });
}

/** Judges where a model can be deployed now, and recommends a GPU. Nothing is reserved. */
export function getPlacement(modelId: string, options?: RequestOptions): Promise<Placement> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/deployments/placement', {
    ...options,
    path: localCell,
    query: { model_id: modelId },
  });
}

/** Deploys a model on one GPU. The same key names the same deployment, so a retry cannot make two. */
export function createDeployment(
  input: { nodeId: string; modelId: string; gpuUuid: string; reason: string },
  idempotencyKey: string,
): Promise<Deployment> {
  return client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/deployments', {
    path: localCell,
    idempotencyKey,
    body: {
      node_id: input.nodeId,
      model_id: input.modelId,
      gpu_uuid: input.gpuUuid,
      reason: input.reason,
    },
  });
}

/**
 * Asks a deployment to start, stop, be removed, report or verify. `revision` refuses a request made
 * on stale data with `VERSION_CONFLICT`.
 */
export function requestDeploymentAction(
  id: string,
  input: { action: DeploymentAction; revision: number; reason: string },
  idempotencyKey: string,
): Promise<Deployment> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/deployments/{deployment_id}/actions',
    { path: { ...localCell, deployment_id: id }, idempotencyKey, body: input },
  );
}

/** Reads what a node last reported about its GPUs. */
export function getInferenceNode(
  nodeId: string,
  options?: RequestOptions,
): Promise<InferenceNodeState> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/inference', {
    ...options,
    path: { ...localCell, node_id: nodeId },
  });
}

/** Asks a node's agent to read its GPUs again. */
export function observeInferenceNode(
  nodeId: string,
  reason: string,
  idempotencyKey: string,
): Promise<InferenceTask> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/inference/observations',
    { path: { ...localCell, node_id: nodeId }, idempotencyKey, body: { reason } },
  );
}
