// A list's filters, columns and saved views. What is on screen is remembered per person in
// this browser; a saved view keeps it on the server, so it follows the account.
import {
  deleteSavedView,
  isProblem,
  listSavedViews,
  putSavedView,
  type SavedView,
} from '@ownsolid/api';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSession } from '../session.ts';
import {
  emptyListState,
  readListState,
  sameListState,
  withColumn,
  withFilter,
  type ListDefinition,
  type ListState,
} from './list-state.ts';

function readStored(key: string): { state?: unknown; view?: unknown } {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as { state?: unknown; view?: unknown }) : {};
  } catch {
    // Storage can be disabled by browser policy; the list then starts empty.
    return {};
  }
}

/** What {@link useListView} returns. */
export interface ListView {
  definition: ListDefinition;
  state: ListState;
  setFilter: (name: string, value: string) => void;
  setColumnVisible: (id: string, visible: boolean) => void;
  isColumnVisible: (id: string) => boolean;
  clearFilters: () => void;
  views: readonly SavedView[];
  /** False when this installation cannot store views, or they could not load. */
  viewsAvailable: boolean;
  activeView: SavedView | null;
  /** True when the screen differs from the view that is open. */
  changed: boolean;
  openView: (view: SavedView | null) => void;
  revert: () => void;
  /** Saves the screen as a new view. `id` stays the same across retries. */
  saveAs: (id: string, name: string) => Promise<SavedView>;
  /** Saves the screen over the open view. */
  saveChanges: () => Promise<SavedView | null>;
  rename: (view: SavedView, name: string) => Promise<SavedView>;
  /** Deletes a view and returns a function that restores it. */
  remove: (view: SavedView) => Promise<() => Promise<void>>;
}

/** The state of one list on the page. */
export function useListView(definition: ListDefinition): ListView {
  const me = useSession();
  const client = useQueryClient();
  const storageKey = `ownsolid.list.${definition.key}.${me?.user.id ?? 'visitor'}`;
  const [stored] = useState(() => readStored(storageKey));
  const [state, setState] = useState(() => readListState(stored.state, definition));
  const [activeId, setActiveId] = useState(typeof stored.view === 'string' ? stored.view : null);
  const queryKey = useMemo(() => ['saved-views', definition.key] as const, [definition.key]);
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => listSavedViews(definition.key, { signal }),
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  const views = useMemo(() => query.data ?? [], [query.data]);
  const activeView = views.find((view) => view.id === activeId) ?? null;

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify({ state, view: activeId }));
    } catch {
      // The list still works for this page when storage is unavailable.
    }
  }, [storageKey, state, activeId]);

  const remember = useCallback(
    (saved: SavedView) => {
      client.setQueryData<SavedView[]>(queryKey, (current = []) =>
        [...current.filter((view) => view.id !== saved.id), saved].sort((a, b) =>
          a.name.localeCompare(b.name),
        ),
      );
      return saved;
    },
    [client, queryKey],
  );
  // Another browser changed something: show what is there now.
  const refreshOnConflict = useCallback(
    async (error: unknown) => {
      if (isProblem(error, 'PREFERENCE_REVISION_CONFLICT', 'RESOURCE_NOT_FOUND')) {
        await client.invalidateQueries({ queryKey });
      }
    },
    [client, queryKey],
  );
  const put = useCallback(
    async (id: string, name: string, viewState: unknown, revision: number) => {
      try {
        return remember(
          await putSavedView(id, {
            list: definition.key,
            name,
            state: viewState as Record<string, never>,
            expected_revision: revision,
          }),
        );
      } catch (error) {
        await refreshOnConflict(error);
        throw error;
      }
    },
    [definition.key, remember, refreshOnConflict],
  );

  return {
    definition,
    state,
    setFilter: (name, value) => {
      setState((current) => withFilter(current, name, value));
    },
    setColumnVisible: (id, visible) => {
      setState((current) => withColumn(current, id, visible));
    },
    isColumnVisible: (id) => !state.hiddenColumns.includes(id),
    clearFilters: () => {
      setState((current) => ({ ...current, filters: {} }));
    },
    views,
    viewsAvailable: query.isSuccess,
    activeView,
    changed: activeView
      ? !sameListState(readListState(activeView.state, definition), state)
      : false,
    openView: (view) => {
      setActiveId(view?.id ?? null);
      setState(view ? readListState(view.state, definition) : emptyListState);
    },
    revert: () => {
      if (activeView) setState(readListState(activeView.state, definition));
    },
    saveAs: async (id, name) => {
      const saved = await put(id, name, state, 0);
      setActiveId(saved.id);
      return saved;
    },
    saveChanges: async () =>
      activeView ? put(activeView.id, activeView.name, state, activeView.revision) : null,
    rename: (view, name) => put(view.id, name, view.state, view.revision),
    remove: async (view) => {
      try {
        await deleteSavedView(view.id, view.revision);
      } catch (error) {
        // Already gone, perhaps from a retry: what was asked for is true.
        if (!isProblem(error, 'RESOURCE_NOT_FOUND')) {
          await refreshOnConflict(error);
          throw error;
        }
      }
      client.setQueryData<SavedView[]>(queryKey, (current = []) =>
        current.filter((item) => item.id !== view.id),
      );
      if (activeId === view.id) setActiveId(null);
      return async () => {
        const restored = await put(view.id, view.name, view.state, 0);
        if (activeId === view.id) setActiveId(restored.id);
      };
    },
  };
}
