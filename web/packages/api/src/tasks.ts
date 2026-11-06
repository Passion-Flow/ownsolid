// Tasks that run on nodes, for platform administrators (api/openapi/v1/nodes).
import { createClient } from './client.ts';
import type { nodesContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = nodesContract.components['schemas'];

/** One task and where it stands. */
export type TaskSummary = Schemas['TaskSummary'];

/** Where a task is in its life. */
export type TaskState = TaskSummary['state'];

/** What a task does. */
export type TaskOperation = TaskSummary['operation'];

/** One page of tasks, newest first. */
export type TaskPage = Schemas['TaskSummaryList'];

/** One step of a task's latest attempt. */
export type TaskStep = Schemas['TaskStep'];

/** The name of a step. */
export type TaskStepName = TaskStep['step'];

/** A task and the steps of its latest attempt. */
export type TaskDetail = Schemas['TaskDetail'];

/** One line of a task's log. */
export type TaskLogLine = Schemas['TaskLogLine'];

/** Lines of a task's log, and the cursor to read on from. */
export type TaskLogPage = Schemas['TaskLog'];

// Tasks live in a cell. Every installation has the one local cell until ARC-202.
const localCell = { site_id: 'default', cell_id: 'default' };

const client = createClient<nodesContract.paths>();

/** True once a task can change no more. */
export function taskFinished(state: TaskState): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled';
}

/** Lists tasks across every node, newest first. */
export function listTasks(
  query: {
    limit: number;
    cursor?: string | undefined;
    state?: TaskState | undefined;
    /** Only unfinished tasks the signed-in person started. */
    mine?: boolean | undefined;
    /** Text anywhere in the name of the task's node, in any case. */
    search?: string | undefined;
  },
  options?: RequestOptions,
): Promise<TaskPage> {
  const { limit, cursor, state, mine, search } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/tasks', {
    ...options,
    path: localCell,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(state ? { state } : {}),
      ...(mine ? { mine } : {}),
      ...(search ? { q: search } : {}),
    },
  });
}

/** Reads a task and the steps of its latest attempt. */
export function getTask(id: string, options?: RequestOptions): Promise<TaskDetail> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/tasks/{task_id}', {
    ...options,
    path: { ...localCell, task_id: id },
  });
}

/** Reads a task's log from just after `after`, the cursor of the page before. */
export function getTaskLog(
  id: string,
  query: { limit: number; after?: string | undefined },
  options?: RequestOptions,
): Promise<TaskLogPage> {
  const { limit, after } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/tasks/{task_id}/log', {
    ...options,
    path: { ...localCell, task_id: id },
    query: { limit, ...(after ? { after } : {}) },
  });
}

/**
 * Asks a node to read its facts again. `idempotencyKey` stays the same across retries of one
 * request, so a retry cannot start a second task.
 */
export function collectNodeFacts(
  node: string,
  idempotencyKey: string,
  options?: RequestOptions,
): Promise<Schemas['Task']> {
  return client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/tasks', {
    ...options,
    path: { ...localCell, node_id: node },
    body: { operation: 'collect_facts.v1' },
    idempotencyKey,
  });
}
