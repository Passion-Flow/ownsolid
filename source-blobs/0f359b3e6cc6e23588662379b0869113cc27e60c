// Scripted deployments for browser flows. Actions follow the server's rules: a stale revision is
// refused, and the same key names the same deployment, so a flow proves what the page really sends.
import type { Deployment, Placement, PlacementNode } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';
import { readyModel } from './models.ts';
import { node } from './nodes.ts';

const stamp = '2026-11-06T08:00:00Z';
const digest = 'a'.repeat(64);

/** The GPU the scripted placement recommends. */
export const freeGpu = {
  uuid: 'GPU-6f1c7a1e-0000-4000-8000-000000000081',
  name: 'NVIDIA L40S',
  memory_total_mib: 46068,
  memory_free_mib: 45000,
  refusal: null,
};

/** A second GPU that can take a model, with less free. */
export const smallerGpu = {
  uuid: 'GPU-6f1c7a1e-0000-4000-8000-000000000082',
  name: 'NVIDIA RTX 4000',
  memory_total_mib: 20475,
  memory_free_mib: 20000,
  refusal: null,
};

/** A node that can take a model. */
export const placementNode: PlacementNode = {
  node_id: node.id,
  name: node.name,
  observed_at: stamp,
  refusal: null,
  gpus: [freeGpu, smallerGpu],
};

/** A placement with a recommendation. */
export const openPlacement: Placement = {
  model_id: readyModel.id,
  workspace_id: readyModel.workspace_id,
  candidates: [placementNode],
  recommended: { node_id: node.id, gpu_uuid: freeGpu.uuid },
};

/** A placement whose only node last reported too long ago. */
export const stalePlacement: Placement = {
  ...openPlacement,
  candidates: [{ ...placementNode, refusal: 'INFERENCE_PREFLIGHT_REQUIRED' }],
  recommended: null,
};

/** A deployment running and answering. */
export const runningDeployment: Deployment = {
  id: '6f1c7a1e-0000-4000-8000-000000000091',
  workspace_id: readyModel.workspace_id,
  node_id: node.id,
  model_id: readyModel.id,
  model_kind: 'approved_base',
  catalog_digest: digest,
  serving_recipe_sha256: null,
  gpu_uuid: freeGpu.uuid,
  desired_state: 'running',
  observed_state: 'running',
  revision: 2,
  observation: {
    deployment_id: '6f1c7a1e-0000-4000-8000-000000000091',
    model_id: readyModel.id,
    catalog_digest: digest,
    gpu_uuid: freeGpu.uuid,
    revision: 2,
    managed: true,
    state: 'running',
    ready: true,
    verification: {
      model: 'projectone-qwen3-0.6b',
      prompt_sha256: digest,
      response_sha256: digest,
      input_tokens: 10,
      output_tokens: 4,
      observed_at: stamp,
    },
  },
  observed_at: stamp,
  last_task_id: '6f1c7a1e-0000-4000-8000-000000000092',
  task_state: 'succeeded',
  task_error_code: null,
  task_deadline_at: null,
  resource_state: 'held',
  stale: false,
  created_at: stamp,
  removed_at: null,
} as unknown as Deployment;

/** A deployment whose load failed on the node. */
export const failedDeployment: Deployment = {
  ...runningDeployment,
  id: '6f1c7a1e-0000-4000-8000-000000000093',
  observed_state: 'failed',
  observation: null,
  observed_at: null,
  task_state: 'failed',
  task_error_code: 'INFERENCE_GPU_OOM',
};

/** A deployment a stopped agent no longer reports on. */
export const staleDeployment: Deployment = {
  ...runningDeployment,
  id: '6f1c7a1e-0000-4000-8000-000000000094',
  stale: true,
};

/** A deployment stopped on purpose. */
export const stoppedDeployment: Deployment = {
  ...runningDeployment,
  id: '6f1c7a1e-0000-4000-8000-000000000095',
  desired_state: 'stopped',
  observed_state: 'stopped',
  observation: null,
  resource_state: 'released',
};

interface ScriptedFailure {
  status: number;
  code: string;
}

/** The deployments a flow starts from, and what it makes go wrong. */
export interface DeploymentScript {
  deployments: Deployment[];
  placement: Placement;
  /** What the placement becomes once a node has read its GPUs. */
  placementAfterRead: Placement | null;
  deploymentsProblem: ScriptedFailure | null;
  /** Failures of the next creations, in order; `lost` creates and drops the answer. */
  createFailures: (ScriptedFailure | 'lost')[];
  nextAction: ScriptedFailure | null;
  /** The keys creations arrived with, and the actions requested. */
  createKeys: string[];
  actions: { action: string; revision: number; reason: string }[];
}

/** A script with nothing deployed. */
export function emptyDeploymentScript(): DeploymentScript {
  return {
    deployments: [],
    placement: { ...openPlacement },
    placementAfterRead: null,
    deploymentsProblem: null,
    createFailures: [],
    nextAction: null,
    createKeys: [],
    actions: [],
  };
}

type Reply = (route: Route, value: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: ScriptedFailure) => Promise<void>;

function idOf(route: Route, fromEnd = 1): string {
  return new URL(route.request().url()).pathname.split('/').at(-fromEnd) ?? '';
}

const missing = { status: 404, code: 'RESOURCE_NOT_FOUND' };

function apply(found: Deployment, action: string): Deployment {
  const next = { ...found, revision: found.revision + 1 };
  if (action === 'stop') return { ...next, desired_state: 'stopped', task_state: 'pending' };
  if (action === 'remove') return { ...next, desired_state: 'removed', task_state: 'pending' };
  if (action === 'deploy') return { ...next, desired_state: 'running', task_state: 'pending' };
  return next;
}

/** Routes the deployments contract onto `state`. */
export async function routeDeployments(
  page: Page,
  state: DeploymentScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const base = '**/api/v1/sites/*/cells/*/admin';
  await page.route(/\/workspaces\/[0-9a-f-]{36}\/deployments$/, (route) =>
    state.deploymentsProblem
      ? problem(route, state.deploymentsProblem)
      : data(route, { items: state.deployments }),
  );
  await page.route(`${base}/deployments?*`, (route) => {
    if (state.deploymentsProblem) return problem(route, state.deploymentsProblem);
    const model = new URL(route.request().url()).searchParams.get('model_id');
    const items = state.deployments.filter((item) => !model || item.model_id === model);
    return data(route, { items });
  });
  await page.route(`${base}/deployments`, (route) => {
    const request = route.request();
    const key = request.headers()['idempotency-key'] ?? '';
    state.createKeys.push(key);
    const failure = state.createFailures.shift();
    if (failure && failure !== 'lost') return problem(route, failure);
    const body = request.postDataJSON() as {
      node_id: string;
      model_id: string;
      gpu_uuid: string;
      reason: string;
    };
    if (!key || body.reason === '') {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const id = `6f1c7a1e-0000-4000-8000-${key.replaceAll('-', '').slice(0, 12)}`;
    let created = state.deployments.find((item) => item.id === id);
    if (!created) {
      created = {
        ...runningDeployment,
        id,
        node_id: body.node_id,
        model_id: body.model_id,
        gpu_uuid: body.gpu_uuid,
        observed_state: 'unknown',
        observation: null,
        observed_at: null,
        revision: 1,
        task_state: 'pending',
        task_deadline_at: stamp,
        stale: true,
      };
      state.deployments.unshift(created);
    }
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, created, 202);
  });
  await page.route(`${base}/deployments/placement?*`, (route) => data(route, state.placement));
  await page.route(/\/admin\/deployments\/[0-9a-f-]{36}$/, (route) => {
    const found = state.deployments.find((item) => item.id === idOf(route));
    return found ? data(route, found) : problem(route, missing);
  });
  await page.route(/\/admin\/deployments\/[0-9a-f-]{36}\/actions$/, (route) => {
    if (state.nextAction) {
      const scripted = state.nextAction;
      state.nextAction = null;
      return problem(route, scripted);
    }
    const index = state.deployments.findIndex((item) => item.id === idOf(route, 2));
    const found = state.deployments[index];
    if (!found) return problem(route, missing);
    const body = route.request().postDataJSON() as {
      action: string;
      revision: number;
      reason: string;
    };
    if (!route.request().headers()['idempotency-key']) {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    if (body.revision !== found.revision) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    state.actions.push(body);
    const updated = apply(found, body.action);
    state.deployments[index] = updated;
    return data(route, updated, 202);
  });
  await page.route(/\/admin\/nodes\/[0-9a-f-]{36}\/inference\/observations$/, (route) => {
    if (state.placementAfterRead) state.placement = state.placementAfterRead;
    return data(route, { id: '6f1c7a1e-0000-4000-8000-000000000098' }, 202);
  });
}
