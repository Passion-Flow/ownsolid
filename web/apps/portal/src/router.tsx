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

const frameRoute = createFrameRoute({ navigation, shortcuts });

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: lazyRouteComponent(() => import('./home-page.tsx'), 'HomePage'),
});

const keysRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/keys',
  component: lazyRouteComponent(() => import('./keys-page.tsx'), 'KeysPage'),
});

const accountRoute = createAccountRoute(frameRoute);

const supportRoute = createSupportRoute(frameRoute);

const helpRoutes = createHelpRoutes(frameRoute);

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([
    frameRoute.addChildren([homeRoute, keysRoute, accountRoute, supportRoute, ...helpRoutes]),
  ]),
]);

/** The portal application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'portal');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
