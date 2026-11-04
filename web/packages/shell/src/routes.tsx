// Routes every application shares: runtime configuration, sign-in and the
// authenticated layout. Applications add their pages under authenticatedRoute.
import { loadRuntimeConfig, type Audience, type RuntimeConfig } from '@ownsolid/api';
import type { QueryClient } from '@tanstack/react-query';
import { queryOptions } from '@tanstack/react-query';
import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  redirect,
  useRouter,
} from '@tanstack/react-router';
import type { ReactNode } from 'react';
import { ForgotPasswordPage, ResetPasswordPage } from './password-pages.tsx';
import { safeRedirect, signInPath } from './redirect.ts';
import { RegisterPage } from './register-page.tsx';
import { sessionQuery } from './session.ts';
import { SignInPage } from './sign-in-page.tsx';
import { SsoResultPage, parseSsoResult } from './sso.tsx';
import { NotFoundPage, RouteErrorPage } from './status-pages.tsx';

/** Router context every application provides. */
export interface AppContext {
  queryClient: QueryClient;
  /** Which entrance this application is. */
  audience: Audience;
}

/** The runtime configuration as a query; it is loaded once per page load. */
export function runtimeConfigQuery() {
  return queryOptions({
    queryKey: ['runtime-config'],
    queryFn: ({ signal }): Promise<RuntimeConfig> => loadRuntimeConfig(signal),
    staleTime: 'static',
  });
}

/** The root route. It loads the runtime configuration before anything else. */
export const rootRoute = createRootRouteWithContext<AppContext>()({
  beforeLoad: async ({ context }) => ({
    config: await context.queryClient.query(runtimeConfigQuery()),
  }),
  component: Outlet,
  notFoundComponent: NotFoundPage,
  errorComponent: RouteErrorPage,
});

interface SignInSearch {
  redirect?: string;
}

/** `/sign-in`. A signed-in visitor goes straight to the requested page. */
const signInRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: signInPath,
  validateSearch: (search: Record<string, unknown>): SignInSearch =>
    typeof search.redirect === 'string' ? { redirect: search.redirect } : {},
  beforeLoad: async ({ context, search }) => {
    const me = await context.queryClient.query(sessionQuery());
    if (me) redirect({ href: safeRedirect(search.redirect), replace: true, throw: true });
  },
  component: SignInRoute,
});

/**
 * The router context of the running application. Shared packages cannot see the
 * application's registered router type, so the context is typed here once.
 */
export function useAppContext(): AppContext {
  return useRouter().options.context as AppContext;
}

function SignInRoute(): ReactNode {
  const router = useRouter();
  const search: SignInSearch = signInRoute.useSearch();
  const target = search.redirect;
  const { audience } = useAppContext();
  return (
    <SignInPage
      audience={audience}
      onSignedIn={() => {
        void router.navigate({ href: safeRedirect(target), replace: true });
      }}
    />
  );
}

const forgotPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/forgot-password',
  component: ForgotPasswordPage,
});

// The next three paths are fixed by the control plane: it puts them in account emails
// (platform/mail) and redirects to them after single sign-on (modules/sso/http).
const resetPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/reset-password',
  component: ResetPasswordPage,
});

const registerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/register',
  // Invitations belong to the portal; the admin origin refuses them.
  component: function RegisterRoute() {
    return useAppContext().audience === 'portal' ? <RegisterPage /> : <NotFoundPage />;
  },
});

const signInResultRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login/sso-result',
  validateSearch: parseSsoResult,
  beforeLoad: ({ search }) => {
    // The session cookie is set; the start page loads the identity.
    if (!search.error) redirect({ to: '/', replace: true, throw: true });
  },
  component: function SignInResultRoute() {
    return <SsoResultPage search={signInResultRoute.useSearch()} />;
  },
});

const linkResultRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account/sso-result',
  validateSearch: parseSsoResult,
  component: function LinkResultRoute() {
    return <SsoResultPage search={linkResultRoute.useSearch()} />;
  },
});

/** Every page reachable before sign-in. */
export const publicRoutes = [
  signInRoute,
  forgotPasswordRoute,
  resetPasswordRoute,
  registerRoute,
  signInResultRoute,
  linkResultRoute,
] as const;

/** Layout for every page that needs a session; it sends visitors to sign-in first. */
export const authenticatedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'authenticated',
  beforeLoad: async ({ context, location }) => {
    const me = await context.queryClient.query(sessionQuery());
    if (!me)
      redirect({ to: signInPath, search: { redirect: location.href }, replace: true, throw: true });
    return { me };
  },
  component: Outlet,
});
