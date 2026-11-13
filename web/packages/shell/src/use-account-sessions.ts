import {
  isProblem,
  listBrowserSessions,
  revokeBrowserSession,
  type BrowserSession,
  type Me,
} from '@ownsolid/api';
import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';

/** A refusal invalidates every visible row, including an already selected target. */
export function sessionAccessRefused(error: unknown): boolean {
  return (
    isProblem(error) &&
    (error.status === 401 ||
      error.status === 403 ||
      isProblem(error, 'AUTH_REQUIRED', 'AUTH_SESSION_EXPIRED', 'ACCESS_DENIED'))
  );
}

function isActive(owner: { active: boolean }): boolean {
  return owner.active;
}

function requiresFreshRead(error: unknown): boolean {
  return (
    !isProblem(error) ||
    error.status === 0 ||
    isProblem(
      error,
      'STORAGE_COMMIT_OUTCOME_UNKNOWN',
      'RESPONSE_INVALID',
      'CSRF_INVALID',
      'RESOURCE_NOT_FOUND',
    )
  );
}

/**
 * One mounted account view owns one random, memory-only read identity. No workspace,
 * account version, persisted cache or global clear stands in for a browser session.
 * The keyed Account section is replaced after password rotation and authentication.
 */
export function useAccountSessions(me: Me) {
  const client = useQueryClient();
  const [view] = useState(() => crypto.randomUUID());
  const key = ['own-browser-sessions', me.audience, me.user.id, view];
  const [target, setTarget] = useState<BrowserSession | null>(null);
  const [mustRead, setMustRead] = useState(false);
  const [ended, setEnded] = useState(false);
  const [retryAt, setRetryAt] = useState(0);
  const [accessError, setAccessError] = useState<unknown>(null);
  const [, tick] = useState(0);
  const life = useRef({ active: false, controller: new AbortController() });
  const writeBusy = useRef(false);
  const options = infiniteQueryOptions({
    queryKey: key,
    queryFn: async ({ pageParam, signal }) => {
      try {
        return await listBrowserSessions(pageParam, { signal, background: pageParam === null });
      } catch (error) {
        if (!signal.aborted && sessionAccessRefused(error)) setAccessError(error);
        throw error;
      }
    },
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    enabled: (query) => query.state.status !== 'error',
    retry: false,
    retryOnMount: false,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: false,
    staleTime: 'static',
    gcTime: 0,
    networkMode: 'always',
  });
  const query = useInfiniteQuery(options);

  useEffect(() => {
    const current = { active: true, controller: new AbortController() };
    life.current = current;
    return () => {
      current.active = false;
      current.controller.abort();
    };
  }, []);

  // A timer only enables an explicit action; it never retries a network request.
  useEffect(() => {
    const remaining = retryAt - Date.now();
    if (remaining <= 0) return;
    const timer = setTimeout(
      () => {
        tick((n) => n + 1);
      },
      Math.min(remaining, 2_147_483_647),
    );
    return () => {
      clearTimeout(timer);
    };
  }, [retryAt]);

  const waitFor = (error: unknown) => {
    setRetryAt(Date.now() + (isProblem(error) ? (error.retryAfterSeconds ?? 0) : 0) * 1_000);
  };

  const end = useMutation({
    retry: false,
    networkMode: 'always',
    gcTime: 0,
    mutationFn: async ({ row, owner }: { row: BrowserSession; owner: typeof life.current }) => {
      try {
        await revokeBrowserSession(row.id, { signal: owner.controller.signal });
        return owner;
      } catch (error) {
        // An old request must not sign out a newly mounted view through MutationCache.
        if (!isActive(owner)) return null;
        throw error;
      }
    },
    onSuccess: (owner) => {
      if (!owner?.active) return;
      setTarget(null);
      setEnded(true);
      setMustRead(true);
    },
    onError: (error) => {
      waitFor(error);
      if (sessionAccessRefused(error)) setAccessError(error);
      if (sessionAccessRefused(error) || requiresFreshRead(error)) setTarget(null);
      setMustRead(requiresFreshRead(error));
    },
    onSettled: () => {
      writeBusy.current = false;
    },
  });

  const denied =
    sessionAccessRefused(accessError) ||
    sessionAccessRefused(query.error) ||
    sessionAccessRefused(end.error);
  const notReady =
    isProblem(query.error, 'SERVICE_NOT_READY') || isProblem(end.error, 'SERVICE_NOT_READY');
  const waiting = Date.now() < retryAt;
  const busy = query.isFetching || end.isPending;
  const blocked = denied || notReady || mustRead || busy || waiting || query.isError;

  const readFirst = async () => {
    const owner = life.current;
    if (
      !isActive(owner) ||
      writeBusy.current ||
      client.getQueryState(key)?.fetchStatus !== 'idle' ||
      waiting
    )
      return;
    try {
      await client.infiniteQuery({
        ...options,
        queryFn: ({ signal }) => listBrowserSessions(null, { signal }),
        pages: 1,
        staleTime: 0,
      });
      if (!isActive(owner)) return;
      setAccessError(null);
      end.reset();
      setTarget(null);
      setMustRead(false);
      setRetryAt(0);
    } catch (error) {
      if (isActive(owner)) {
        waitFor(error);
        if (sessionAccessRefused(error)) setAccessError(error);
      }
    }
  };

  const confirm = () => {
    if (!target || target.current || blocked || writeBusy.current) return;
    writeBusy.current = true;
    end.mutate({ row: target, owner: life.current });
  };

  // A confirmed end requires a new read, never an optimistic removal. This effect
  // does not run for uncertain outcomes: those wait for the person's Read again.
  const afterEnd = useRef(false);
  useEffect(() => {
    if (!end.isSuccess || !end.data?.active || afterEnd.current) return;
    afterEnd.current = true;
    void readFirst();
  });

  const loadMore = () => {
    if (blocked || !query.hasNextPage || client.getQueryState(key)?.fetchStatus !== 'idle') return;
    void query.fetchNextPage({ cancelRefetch: false });
  };
  const readAgain = () => {
    if (writeBusy.current || busy || waiting) return;
    if (
      query.isFetchNextPageError &&
      !denied &&
      !mustRead &&
      !isProblem(query.error, 'INVALID_CURSOR')
    ) {
      void query.fetchNextPage({ cancelRefetch: false });
    } else {
      void readFirst();
    }
  };
  return {
    rows: denied || notReady ? [] : (query.data?.pages.flatMap((page) => page.items) ?? []),
    target: denied || notReady ? null : target,
    readError: query.error,
    endError: end.error,
    pending: query.isPending,
    partial: query.data !== undefined,
    busy,
    blocked,
    denied,
    notReady,
    waiting,
    mustRead,
    ended,
    ending: end.isPending,
    hasMore: query.hasNextPage,
    readAgain,
    loadMore,
    confirm,
    select: (row: BrowserSession) => {
      if (row.current || blocked) return;
      afterEnd.current = false;
      setEnded(false);
      end.reset();
      setTarget(row);
    },
    close: () => {
      if (!writeBusy.current) {
        setTarget(null);
        end.reset();
      }
    },
  };
}
