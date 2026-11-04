import {
  WelcomePage,
  authenticatedRoute,
  createApplication,
  rootRoute,
  signInRoute,
} from '@ownsolid/shell';
import { createRoute } from '@tanstack/react-router';

const homeRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: '/',
  component: WelcomePage,
});

const routeTree = rootRoute.addChildren([signInRoute, authenticatedRoute.addChildren([homeRoute])]);

/** The console application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'admin');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
