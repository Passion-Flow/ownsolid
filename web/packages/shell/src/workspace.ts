// The workspace this browser works in. It only selects what the interface shows: the
// server checks access to a workspace on every request.
import { listWorkspaces, type Audience, type WorkspaceSummary } from '@ownsolid/api';
import { queryOptions, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useState } from 'react';
import { runtimeConfigQuery } from './routes.tsx';
import { sessionKey, useSession } from './session.ts';

const storageKey = (userId: string) => `ownsolid.workspace.${userId}`;

function storedWorkspace(userId: string): string | null {
  try {
    return localStorage.getItem(storageKey(userId));
  } catch {
    return null;
  }
}

/** The workspaces the session may use. */
export function workspacesQuery(audience: Audience, limit: number) {
  return queryOptions({
    queryKey: ['workspaces', audience, limit],
    queryFn: ({ signal }) => listWorkspaces(audience, limit, { signal }),
    staleTime: 60_000,
  });
}

/** What {@link useWorkspaces} returns. */
export interface Workspaces {
  /** The selected workspace's ID and name, or null when the user has none. */
  active: { id: string; name: string } | null;
  items: readonly WorkspaceSummary[];
  /** True when more workspaces exist than the list shows. */
  truncated: boolean;
  isPending: boolean;
  error: unknown;
  retry: () => void;
  select: (workspace: WorkspaceSummary) => void;
}

/** The selectable workspaces and the current choice, defaulting to the session's. */
export function useWorkspaces(): Workspaces {
  const me = useSession();
  const queryClient = useQueryClient();
  const config = useQuery(runtimeConfigQuery()).data;
  const limit = config?.ui.lookupPageSize ?? 100;
  const audience = me?.audience ?? 'portal';
  const query = useQuery({ ...workspacesQuery(audience, limit), enabled: me !== null });
  const userId = me?.user.id ?? '';
  const [chosen, setChosen] = useState(() => storedWorkspace(userId));

  const select = useCallback(
    (workspace: WorkspaceSummary) => {
      try {
        localStorage.setItem(storageKey(userId), workspace.id);
      } catch {
        // The choice still applies to this page when storage is unavailable.
      }
      setChosen(workspace.id);
      // Everything on screen may belong to the previous workspace.
      void queryClient.invalidateQueries({
        predicate: (entry) => entry.queryKey[0] !== sessionKey[0],
      });
    },
    [queryClient, userId],
  );

  const items = query.data?.items ?? [];
  // A stored choice the user lost access to falls back to the session's workspace.
  const active = items.find((item) => item.id === chosen) ?? me?.workspace ?? null;
  return {
    active: active && { id: active.id, name: active.name },
    items,
    truncated: Boolean(query.data?.next_cursor),
    isPending: query.isPending,
    error: query.error,
    retry: () => {
      void query.refetch();
    },
    select,
  };
}
