import {
  AppFrame,
  WelcomePage,
  authenticatedRoute,
  createApplication,
  rootRoute,
  signInRoute,
} from '@ownsolid/shell';
import { Outlet, createRoute } from '@tanstack/react-router';
import { navigation } from './navigation.ts';

const frameRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  id: 'frame',
  component: () => (
    <AppFrame navigation={navigation} showLicense>
      <Outlet />
    </AppFrame>
  ),
});

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: WelcomePage,
});

const routeTree = rootRoute.addChildren([
  signInRoute,
  authenticatedRoute.addChildren([frameRoute.addChildren([homeRoute])]),
]);

/** The console application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'admin');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
