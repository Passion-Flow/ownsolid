import {
  WelcomePage,
  authenticatedRoute,
  createAccountRoute,
  createApplication,
  createFrameRoute,
  publicRoutes,
  rootRoute,
} from '@ownsolid/shell';
import { createRoute } from '@tanstack/react-router';
import { navigation } from './navigation.ts';

const frameRoute = createFrameRoute({ navigation });

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: WelcomePage,
});

const accountRoute = createAccountRoute(frameRoute);

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([frameRoute.addChildren([homeRoute, accountRoute])]),
]);

/** The portal application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'portal');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
