import {
  authenticatedRoute,
  createAccountRoute,
  createApplication,
  createFrameRoute,
  createSupportRoute,
  publicRoutes,
  rootRoute,
} from '@ownsolid/shell';
import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { navigation } from './navigation.ts';

const frameRoute = createFrameRoute({ navigation });

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: lazyRouteComponent(() => import('./home-page.tsx'), 'HomePage'),
});

const accountRoute = createAccountRoute(frameRoute);

const supportRoute = createSupportRoute(frameRoute);

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([frameRoute.addChildren([homeRoute, accountRoute, supportRoute])]),
]);

/** The portal application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'portal');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
