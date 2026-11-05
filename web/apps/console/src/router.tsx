import {
  WelcomePage,
  authenticatedRoute,
  createApplication,
  createFrameRoute,
  publicRoutes,
  rootRoute,
} from '@ownsolid/shell';
import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { navigation } from './navigation.ts';

const frameRoute = createFrameRoute({ navigation, showLicense: true });

const homeRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/',
  component: WelcomePage,
});

const mailRoute = createRoute({
  getParentRoute: () => frameRoute,
  path: '/mail',
  component: lazyRouteComponent(() => import('./mail-deliveries-page.tsx'), 'MailDeliveriesPage'),
});

const routeTree = rootRoute.addChildren([
  ...publicRoutes,
  authenticatedRoute.addChildren([frameRoute.addChildren([homeRoute, mailRoute])]),
]);

/** The console application: its router and query client. */
export const { router, queryClient } = createApplication(routeTree, 'admin');

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
