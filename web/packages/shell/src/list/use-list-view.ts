// A list's filters, columns and saved views. The filters, sort and open row live in the
// address, so a link shows the same list to whoever opens it (UX-114); the visible columns are
// the person's own. What is on screen is also remembered in this browser, and a saved view
// keeps it on the server, so it follows the account.
import {
  deleteSavedView,
  isProblem,
  listSavedViews,
  putSavedView,
  type SavedView,
} from '@ownsolid/api';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useRouter, useRouterState } from '@tanstack/react-router';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSession } from '../session.ts';
import {
  emptyListState,
  linksList,
  readListLink,
  readListState,
  sameListState,
  withColumn,
  withFilter,
  type ListDefinition,
  type ListState,
  writeListLink,
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
  /** The row whose drawer is open, from the address. */
  openRow: string | null;
  setOpenRow: (id: string | null) => void;
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
  /** Puts a view back as it was, on top of the revision that replaced it. */
  restore: (current: SavedView, previous: SavedView) => Promise<SavedView>;
  /** Deletes a view and returns a function that restores it. */
  remove: (view: SavedView) => Promise<() => Promise<void>>;
}

/** The state of one list on the page. */
export function useListView(definition: ListDefinition): ListView {
  const me = useSession();
  const client = useQueryClient();
  const storageKey = `ownsolid.list.${definition.key}.${me?.user.id ?? 'visitor'}`;
  const [stored] = useState(() => readStored(storageKey));
  const router = useRouter();
  const location = useRouterState({ select: (current) => current.location });
  const link = useMemo(
    () => readListLink(location.searchStr, definition),
    [location.searchStr, definition],
  );
  const [hiddenColumns, setHiddenColumns] = useState(
    () => readListState(stored.state, definition).hiddenColumns,
  );
  const state: ListState = useMemo(
    () => ({ filters: link.filters, hiddenColumns }),
    [link.filters, hiddenColumns],
  );
  const [activeId, setActiveId] = useState(typeof stored.view === 'string' ? stored.view : null);
  // Every change replaces the entry: Back leaves the page rather than stepping through filters.
  const show = useCallback(
    (filters: ListState['filters'], open: string | null) => {
      const current = router.state.location;
      const search = writeListLink(current.searchStr, definition, filters, open);
      if (search === current.searchStr || (search === '' && current.searchStr === '?')) return;
      void router.navigate({
        href: `${current.pathname}${search}${current.hash ? `#${current.hash}` : ''}`,
        replace: true,
      });
    },
    [router, definition],
  );
  // A link decides what the list shows. Without one, this browser's last screen comes back,
  // and the address shows it, so copying the address shares what is on screen.
  const [restored, setRestored] = useState(false);
  useEffect(() => {
    if (restored) return;
    setRestored(true);
    if (!linksList(location.searchStr, definition)) {
      show(readListState(stored.state, definition).filters, null);
    }
  }, [restored, location.searchStr, definition, show, stored.state]);
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
      show(withFilter(state, name, value).filters, link.open);
    },
    setColumnVisible: (id, visible) => {
      setHiddenColumns(withColumn(state, id, visible).hiddenColumns);
    },
    isColumnVisible: (id) => !state.hiddenColumns.includes(id),
    openRow: link.open,
    setOpenRow: (id) => {
      show(state.filters, id);
    },
    clearFilters: () => {
      show({}, link.open);
    },
    views,
    viewsAvailable: query.isSuccess,
    activeView,
    changed: activeView
      ? !sameListState(readListState(activeView.state, definition), state)
      : false,
    openView: (view) => {
      const next = view ? readListState(view.state, definition) : emptyListState;
      setActiveId(view?.id ?? null);
      setHiddenColumns(next.hiddenColumns);
      show(next.filters, null);
    },
    revert: () => {
      if (!activeView) return;
      const saved = readListState(activeView.state, definition);
      setHiddenColumns(saved.hiddenColumns);
      show(saved.filters, link.open);
    },
    saveAs: async (id, name) => {
      const saved = await put(id, name, state, 0);
      setActiveId(saved.id);
      return saved;
    },
    saveChanges: async () =>
      activeView ? put(activeView.id, activeView.name, state, activeView.revision) : null,
    rename: (view, name) => put(view.id, name, view.state, view.revision),
    restore: (current, previous) =>
      put(current.id, previous.name, previous.state, current.revision),
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
