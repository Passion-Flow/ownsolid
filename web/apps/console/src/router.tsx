import {
  authenticatedRoute,
  createAccountRoute,
  createApplication,
  createFrameRoute,
  publicRoutes,
  rootRoute,
} from '@ownsolid/shell';
import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { navigation, shortcuts } from './navigation.ts';
import { searchSources } from './search-sources.ts';
import { operations, taskPath } from './task-names.ts';

const frameRoute = createFrameRoute({
  navigation,
  shortcuts,
  search: searchSources,
  licensePath: '/license',
  tasks: { taskPath, taskName: (task) => operations[task.operation] },
});

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: lazyRouteComponent(() => import('./overview-page.tsx'), 'OverviewPage'),
});

const nodesRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/nodes',
  component: lazyRouteComponent(() => import('./nodes-page.tsx'), 'NodesPage'),
});

const pacingRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/nodes/pacing',
  component: lazyRouteComponent(() => import('./agent-pacing-page.tsx'), 'AgentPacingPage'),
});

const nodeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/nodes/$nodeId',
  component: lazyRouteComponent(() => import('./node-page.tsx'), 'NodePage'),
});

const userRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/users/$userId',
  component: lazyRouteComponent(() => import('./user-page.tsx'), 'UserPage'),
});

const connectorRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/sso/$connectorId',
  component: lazyRouteComponent(() => import('./connector-page.tsx'), 'ConnectorPage'),
});

const usersRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/users',
  component: lazyRouteComponent(() => import('./users-page.tsx'), 'UsersPage'),
});

const workspacesRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/users/workspaces',
  component: lazyRouteComponent(() => import('./workspaces-page.tsx'), 'WorkspacesPage'),
});

const workspaceRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/users/workspaces/$workspaceId',
  component: lazyRouteComponent(() => import('./workspace-page.tsx'), 'WorkspacePage'),
});

const ssoRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/sso',
  component: lazyRouteComponent(() => import('./sso-page.tsx'), 'SsoPage'),
});

const auditRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/audit',
  component: lazyRouteComponent(() => import('./audit-page.tsx'), 'AuditPage'),
});

const licenseRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/license',
  component: lazyRouteComponent(() => import('./license-page.tsx'), 'LicensePage'),
});

const mailRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/mail',
  component: lazyRouteComponent(() => import('./mail-deliveries-page.tsx'), 'MailDeliveriesPage'),
});

const tasksRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/tasks',
  component: lazyRouteComponent(() => import('./tasks-page.tsx'), 'TasksPage'),
});

const taskRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/tasks/$taskId',
  component: lazyRouteComponent(() => import('./task-page.tsx'), 'TaskPage'),
});

const settingsRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/settings',
  component: lazyRouteComponent(() => import('./settings-page.tsx'), 'SettingsPage'),
});

const accountRoute = createAccountRoute(frameRoute);

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([
    frameRoute.addChildren([
      homeRoute,
      accountRoute,
      nodesRoute,
      pacingRoute,
      nodeRoute,
      userRoute,
      connectorRoute,
      usersRoute,
      workspacesRoute,
      workspaceRoute,
      ssoRoute,
      auditRoute,
      licenseRoute,
      mailRoute,
      tasksRoute,
      taskRoute,
      settingsRoute,
    ]),
  ]),
]);

/** The console application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'admin');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
