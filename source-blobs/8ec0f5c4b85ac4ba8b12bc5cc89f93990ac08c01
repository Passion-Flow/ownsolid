import {
  authenticatedRoute,
  createAccountRoute,
  createApplication,
  createFrameRoute,
  createHelpRoutes,
  createSupportRoute,
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

const libraryRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/library',
  component: lazyRouteComponent(() => import('./model-library-page.tsx'), 'ModelLibraryPage'),
});

const modelRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/library/$modelId',
  component: lazyRouteComponent(() => import('./model-page.tsx'), 'ModelPage'),
});

const modelImportRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/library/import',
  component: lazyRouteComponent(() => import('./model-import-page.tsx'), 'ModelImportPage'),
});

const modelImportStatusRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/library/imports/$importId',
  component: lazyRouteComponent(() => import('./model-import-page.tsx'), 'ModelImportStatusPage'),
});

const servicesRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/services',
  component: lazyRouteComponent(() => import('./services-page.tsx'), 'ServicesPage'),
});

const deployRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/services/new',
  component: lazyRouteComponent(() => import('./deploy-page.tsx'), 'DeployPage'),
});

const deploymentRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/services/$deploymentId',
  component: lazyRouteComponent(() => import('./deployment-page.tsx'), 'DeploymentPage'),
});

const gatewayRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/gateway',
  component: lazyRouteComponent(() => import('./gateway-page.tsx'), 'GatewayPage'),
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

const supportRoute = createSupportRoute(frameRoute);

const helpRoutes = createHelpRoutes(frameRoute);

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([
    frameRoute.addChildren([
      homeRoute,
      accountRoute,
      supportRoute,
      ...helpRoutes,
      nodesRoute,
      pacingRoute,
      nodeRoute,
      libraryRoute,
      modelRoute,
      modelImportRoute,
      modelImportStatusRoute,
      servicesRoute,
      deployRoute,
      deploymentRoute,
      gatewayRoute,
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
