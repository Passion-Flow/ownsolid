// What the command menu finds: each source asks the list its page shows, so "See all" opens the
// same rows (docs/research/global-search.md). Models and applications join with their pages.
import type { SearchSource } from '@ownsolid/shell';
import { Building2, ListChecks, Server, Users } from 'lucide-react';

const finders = () => import('./search-finders.ts');

/** The console's sources. */
export const searchSources: readonly SearchSource[] = [
  {
    key: 'nodes',
    icon: Server,
    kind: 'command.kind.node',
    seeAll: 'command.seeAll.nodes',
    failed: 'command.failed.nodes',
    find: async (term, context) => (await finders()).findNodes(term, context),
  },
  {
    key: 'people',
    icon: Users,
    kind: 'command.kind.person',
    seeAll: 'command.seeAll.people',
    failed: 'command.failed.people',
    find: async (term, context) => (await finders()).findPeople(term, context),
  },
  {
    key: 'tasks',
    icon: ListChecks,
    kind: 'command.kind.task',
    seeAll: 'command.seeAll.tasks',
    failed: 'command.failed.tasks',
    find: async (term, context) => (await finders()).findTasks(term, context),
  },
  {
    key: 'workspaces',
    icon: Building2,
    kind: 'command.kind.workspace',
    seeAll: 'command.seeAll.workspaces',
    failed: 'command.failed.workspaces',
    find: async (term, context) => (await finders()).findWorkspaces(term, context),
  },
];
