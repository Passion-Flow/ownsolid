// Scripted tasks for browser flows.
import type { TaskLogLine, TaskStep, TaskSummary } from '@ownsolid/api';
import { node } from './nodes.ts';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-06T08:00:00Z';

/** A task with what the page reads about it. */
export interface ScriptedTask {
  task: TaskSummary;
  steps: TaskStep[];
  log: TaskLogLine[];
}

/** One log line of attempt 1 unless a flow says otherwise. */
export function logLine(sequence: number, message: string, attempt = 1): TaskLogLine {
  return { attempt, sequence, at: stamp, level: 'info', message };
}

/** A task the agent is working on. */
export const runningTask: ScriptedTask = {
  task: {
    id: '6f1c7a1e-0000-4000-8000-000000000040',
    workspace_id: node.workspace_id,
    node_id: node.id,
    node_name: node.name,
    operation: 'collect_facts.v1',
    state: 'running',
    attempt: 1,
    error_code: null,
    created_at: stamp,
    updated_at: stamp,
    completed_at: null,
  },
  steps: [
    {
      step: 'read_system',
      state: 'succeeded',
      started_at: stamp,
      finished_at: '2026-11-06T08:00:03Z',
    },
    {
      step: 'read_memory',
      state: 'running',
      started_at: '2026-11-06T08:00:03Z',
      finished_at: null,
    },
  ],
  log: [logLine(2, 'Operating system: ubuntu 24.04')],
};

/** The same task once it has finished. */
export const finishedTask: ScriptedTask = {
  task: {
    ...runningTask.task,
    state: 'succeeded',
    completed_at: '2026-11-06T08:00:09Z',
  },
  steps: [
    {
      step: 'read_system',
      state: 'succeeded',
      started_at: stamp,
      finished_at: '2026-11-06T08:00:03Z',
    },
    {
      step: 'read_memory',
      state: 'succeeded',
      started_at: '2026-11-06T08:00:03Z',
      finished_at: '2026-11-06T08:00:09Z',
    },
  ],
  log: [logLine(2, 'Operating system: ubuntu 24.04'), logLine(5, 'Memory: 33554432000 bytes')],
};

/** A task that stopped on a step. */
export const failedTask: ScriptedTask = {
  task: {
    ...runningTask.task,
    id: '6f1c7a1e-0000-4000-8000-000000000041',
    state: 'failed',
    error_code: 'DISCOVERY_FAILED',
    completed_at: '2026-11-06T08:00:05Z',
  },
  steps: [
    {
      step: 'read_system',
      state: 'failed',
      started_at: stamp,
      finished_at: '2026-11-06T08:00:05Z',
    },
  ],
  log: [
    {
      attempt: 1,
      sequence: 2,
      at: stamp,
      level: 'error',
      message: "Reading this node's facts failed: AGENT_OS_UNSUPPORTED",
    },
  ],
};

/** What a flow changes to script tasks. */
export interface TaskScript {
  /** Tasks, newest first. */
  tasks: ScriptedTask[];
  /** Fails task reads with this while set. */
  tasksProblem: { status: number; code: string } | null;
  /** Answers the next start with this failure instead of starting a task. */
  nextTaskStart: { status: number; code: string } | null;
  /** Idempotency-Key of each start, in order. */
  taskStartKeys: string[];
  /** Whether each task read was marked as background, in order. */
  taskPolls: boolean[];
}

/** The state of a mock with nothing scripted. */
export function emptyTaskScript(): TaskScript {
  return {
    tasks: [],
    tasksProblem: null,
    nextTaskStart: null,
    taskStartKeys: [],
    taskPolls: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

function finished(task: TaskSummary): boolean {
  return task.state === 'succeeded' || task.state === 'failed' || task.state === 'cancelled';
}

function position(line: TaskLogLine): number {
  return line.attempt * 100_000 + line.sequence;
}

/** Serves the task routes of the admin entrance from `state`. */
export async function routeTasks(
  page: Page,
  state: TaskScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const base = '**/api/v1/sites/*/cells/*/admin';
  const read = (route: Route) => {
    state.taskPolls.push(route.request().headers()['x-ownsolid-background'] === 'true');
    return state.tasksProblem;
  };
  await page.route(`${base}/tasks?*`, (route) => {
    const failure = read(route);
    if (failure) return problem(route, failure);
    const query = new URL(route.request().url()).searchParams;
    const wanted = query.get('state');
    const search = query.get('q')?.toLowerCase();
    const items = state.tasks
      .map((item) => item.task)
      .filter((task) => (query.get('mine') === 'true' ? !finished(task) : true))
      .filter((task) => !wanted || task.state === wanted)
      .filter((task) => !search || task.node_name.toLowerCase().includes(search));
    return data(route, { items, next_cursor: null });
  });
  await page.route(`${base}/tasks/*`, (route) => {
    const failure = read(route);
    if (failure) return problem(route, failure);
    const id = new URL(route.request().url()).pathname.split('/').at(-1);
    const found = state.tasks.find((item) => item.task.id === id);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    return data(route, { task: found.task, steps: found.steps });
  });
  await page.route(`${base}/tasks/*/log?*`, (route) => {
    const failure = read(route);
    if (failure) return problem(route, failure);
    const url = new URL(route.request().url());
    const id = url.pathname.split('/').at(-2);
    const found = state.tasks.find((item) => item.task.id === id);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    const after = url.searchParams.get('after') ?? '0.0';
    const [attempt = 0, sequence = 0] = after.split('.').map(Number);
    const limit = Number(url.searchParams.get('limit'));
    const items = found.log
      .filter((line) => position(line) > attempt * 100_000 + sequence)
      .slice(0, limit);
    const last = items.at(-1);
    return data(route, {
      items,
      cursor: last ? `${String(last.attempt)}.${String(last.sequence)}` : after,
    });
  });
  await page.route(`${base}/nodes/*/tasks?*`, (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-2);
    return data(route, {
      items: state.tasks.filter((item) => item.task.node_id === id).map((item) => item.task),
      next_cursor: null,
    });
  });
  await page.route(`${base}/nodes/*/tasks`, (route) => {
    const request = route.request();
    if (request.headers()['x-csrf-token'] !== 'csrf-token') {
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    }
    state.taskStartKeys.push(request.headers()['idempotency-key'] ?? '');
    if (state.nextTaskStart) {
      const scripted = state.nextTaskStart;
      state.nextTaskStart = null;
      return problem(route, scripted);
    }
    const id = `6f1c7a1e-0000-4000-8000-${String(state.taskStartKeys.length).padStart(12, '0')}`;
    const started: ScriptedTask = {
      task: {
        ...runningTask.task,
        id,
        state: 'pending',
        attempt: 0,
        node_id: new URL(request.url()).pathname.split('/').at(-2) ?? '',
      },
      steps: [],
      log: [],
    };
    state.tasks.unshift(started);
    return data(route, started.task, 202);
  });
}
