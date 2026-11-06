// How each search source asks its list. Loaded on the first search, so the first page stays small.
import {
  getTask,
  isProblem,
  listAllWorkspaces,
  listNodes,
  listTasks,
  listUsers,
} from '@ownsolid/api';
import type { SearchSource } from '@ownsolid/shell';
import { nodeState } from './node-state.ts';
import { operations, states, taskPath } from './task-names.ts';

type Find = SearchSource['find'];

// Enough to recognise the one meant; "See all" opens the rest.
const hitsPerSource = 5;

const taskId = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;

/** A link to a list, filtered by what was typed. */
function filtered(path: string, filter: string, term: string): string {
  return `${path}?${new URLSearchParams({ [filter]: term }).toString()}`;
}

/** The link to a list with one row's drawer open. */
function opened(path: string, id: string): string {
  return `${path}?${new URLSearchParams({ open: id }).toString()}`;
}

/** Nodes with the text in their name. */
export const findNodes: Find = async (term, { signal, t }) => {
  const page = await listNodes({ limit: hitsPerSource, search: term }, { signal });
  return {
    hits: page.items.map((node) => ({
      id: node.id,
      label: node.name,
      detail: t(nodeState(node).label),
      href: opened('/nodes', node.id),
    })),
    more: page.next_cursor ? filtered('/nodes', 'name', term) : undefined,
  };
};

/** People with the text in their address or display name. */
export const findPeople: Find = async (term, { signal }) => {
  const page = await listUsers({ limit: hitsPerSource, search: term }, { signal });
  return {
    hits: page.items.map((user) => ({
      id: user.id,
      label: user.display_name,
      detail: user.email,
      href: opened('/users', user.id),
    })),
    more: page.next_cursor ? filtered('/users', 'email', term) : undefined,
  };
};

/** Tasks of nodes with the text in their name, or the one task a pasted ID names. */
export const findTasks: Find = async (term, { signal, t }) => {
  if (taskId.test(term)) {
    try {
      const { task } = await getTask(term.toLowerCase(), { signal });
      return {
        hits: [
          {
            id: task.id,
            label: t(operations[task.operation]),
            detail: task.node_name,
            href: taskPath(task.id),
          },
        ],
      };
    } catch (error) {
      if (isProblem(error, 'RESOURCE_NOT_FOUND')) return { hits: [] };
      throw error;
    }
  }
  const page = await listTasks({ limit: hitsPerSource, search: term }, { signal });
  return {
    hits: page.items.map((task) => ({
      id: task.id,
      label: t(operations[task.operation]),
      detail: `${task.node_name} \u00b7 ${t(states[task.state].label)}`,
      href: taskPath(task.id),
    })),
    more: page.next_cursor ? filtered('/tasks', 'node', term) : undefined,
  };
};

/** Workspaces with the text in their name. */
export const findWorkspaces: Find = async (term, { signal }) => {
  const page = await listAllWorkspaces({ limit: hitsPerSource, search: term }, { signal });
  return {
    hits: page.items.map((workspace) => ({
      id: workspace.id,
      label: workspace.name,
      href: `/users/workspaces/${workspace.id}`,
    })),
    more: page.next_cursor ? filtered('/users/workspaces', 'name', term) : undefined,
  };
};
