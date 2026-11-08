// The signed-in identity as shared query state, and what happens when it ends.
import { getMe, isProblem, type Audience, type Me } from '@ownsolid/api';
import {
  MutationCache,
  QueryCache,
  QueryClient,
  queryOptions,
  useQuery,
} from '@tanstack/react-query';

/** Query key of the current session. */
export const sessionKey = ['session'] as const;

/** Codes meaning the session is missing or over; the user has to sign in again. */
export const signedOutCodes = ['AUTH_REQUIRED', 'AUTH_SESSION_EXPIRED'] as const;

/** True when an error means the session is missing or over. */
export function isSignedOut(error: unknown): boolean {
  return isProblem(error, ...signedOutCodes);
}

/**
 * The current session: the identity, or `null` when nobody is signed in. Other
 * failures, such as an unreachable server, stay errors so the page can offer a retry.
 */
export function sessionQuery() {
  return queryOptions({
    queryKey: sessionKey,
    queryFn: async ({ signal }): Promise<Me | null> => {
      try {
        return await getMe({ signal });
      } catch (error) {
        if (isSignedOut(error)) return null;
        throw error;
      }
    },
    staleTime: 60_000,
  });
}

/**
 * The signed-in identity for pages under the authenticated route, which loads it
 * before they render. Returns `null` in the moment between sign-out and leaving the page.
 */
/** Which entrance this session is in, or the portal's before the session is known. */
export function useAudience(): Audience {
  return useSession()?.audience ?? 'portal';
}

export function useSession(): Me | null {
  return useQuery(sessionQuery()).data ?? null;
}

/** Options for {@link createQueryClient}. */
export interface QueryClientOptions {
  /** Called when any request finds the session over, to send the user to sign-in. */
  onSignedOut: () => void;
}

/**
 * Creates the application's query client. Any query or mutation that fails because
 * the session ended clears cached data and calls `onSignedOut`. Failed reads retry
 * twice only when the failure may pass on its own.
 */
export function createQueryClient({ onSignedOut }: QueryClientOptions): QueryClient {
  let client: QueryClient | undefined = undefined;
  const handle = (error: unknown) => {
    if (!isSignedOut(error) || !client) return;
    client.setQueryData(sessionKey, null);
    onSignedOut();
  };
  client = new QueryClient({
    queryCache: new QueryCache({ onError: handle }),
    mutationCache: new MutationCache({ onError: handle }),
    defaultOptions: {
      queries: {
        retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
        refetchOnWindowFocus: true,
      },
      mutations: { retry: false },
    },
  });
  return client;
}
