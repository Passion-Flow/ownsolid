// Creates and starts an application: router, query client and providers.
import type { Audience } from '@ownsolid/api';
import { I18nProvider } from '@ownsolid/i18n';
import { PreferencesProvider, ToastProvider } from '@ownsolid/ui';
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import {
  RouterProvider,
  createRouter,
  type AnyRoute,
  type AnyRouter,
} from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { signInPath } from './redirect.ts';
import { createQueryClient } from './session.ts';
import { PendingPage } from './status-pages.tsx';

/**
 * Creates the router and query client for a route tree built on `rootRoute`. When a
 * request finds the session over, the user is sent to sign-in and returns to the
 * same page afterwards.
 */
export function createApplication<TRouteTree extends AnyRoute>(
  routeTree: TRouteTree,
  audience: Audience,
) {
  const queryClient = createQueryClient({
    onSignedOut: () => {
      const { pathname, href } = router.state.location;
      if (pathname === signInPath) return;
      void router.navigate({ to: signInPath, search: { redirect: href }, replace: true });
    },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient, audience },
    defaultPendingComponent: PendingPage,
    defaultPendingMs: 300,
    defaultPreload: 'intent',
    scrollRestoration: true,
  });
  return { router, queryClient };
}

/** Options for {@link startApplication}. */
export interface StartOptions {
  router: AnyRouter;
  queryClient: QueryClient;
  /** The element the application renders into. */
  root: HTMLElement;
}

/** Renders an application created with {@link createApplication}. */
export function startApplication({ router, queryClient, root }: StartOptions): void {
  createRoot(root).render(
    <StrictMode>
      <PreferencesProvider>
        <I18nProvider>
          <ToastProvider>
            <QueryClientProvider client={queryClient}>
              <RouterProvider router={router} />
            </QueryClientProvider>
          </ToastProvider>
        </I18nProvider>
      </PreferencesProvider>
    </StrictMode>,
  );
}
