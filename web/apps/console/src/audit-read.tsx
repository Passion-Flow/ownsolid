import { isProblem, type AuditPage } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { useSession } from '@ownsolid/shell';
import { Button, Notice, ProblemNotice } from '@ownsolid/ui';
import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useQueryClient,
  type QueryKey,
} from '@tanstack/react-query';

interface AuditReadOptions {
  queryKey: QueryKey;
  read: (cursor: string | null, signal: AbortSignal) => Promise<AuditPage>;
  enabled?: boolean;
}

function deniedRead(error: unknown): boolean {
  return (
    isProblem(error) &&
    (error.status === 401 ||
      error.status === 403 ||
      error.status === 404 ||
      isProblem(
        error,
        'ACCESS_DENIED',
        'RESOURCE_NOT_FOUND',
        'AUTH_REQUIRED',
        'AUTH_SESSION_EXPIRED',
      ))
  );
}

/** Audit access is recorded: another attempt must be the person's explicit choice. */
export function useAuditRead({ queryKey, read, enabled = true }: AuditReadOptions) {
  const client = useQueryClient();
  const session = useSession();
  const key = [
    ...queryKey,
    {
      reader: session?.user.id,
      audience: session?.audience,
      tenant: session?.workspace?.tenant_id,
      workspace: session?.workspace?.id,
    },
  ];
  const canRead = enabled && session !== null;
  const options = infiniteQueryOptions({
    queryKey: key,
    queryFn: async ({ pageParam, signal }) => {
      try {
        return await read(pageParam, signal);
      } catch (error) {
        if (deniedRead(error)) {
          // Keep the active refusal cached so remounting cannot retry it. Discard
          // older inactive views so changing a filter cannot reveal their events.
          client.removeQueries({
            predicate: (query) =>
              query.getObserversCount() === 0 &&
              (query.queryKey[0] === 'audit' || query.queryKey[0] === 'resource-activity'),
          });
        }
        throw error;
      }
    },
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    // Static data blocks invalidation refetches only after a page exists. A failed
    // initial read must also become manual-only; imperative recovery still works.
    enabled: (query) => canRead && query.state.status !== 'error',
    retry: false,
    retryOnMount: false,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: false,
    staleTime: 'static',
    // An offline read must fail now, not be queued for an implicit reconnect attempt.
    networkMode: 'always',
  });
  const events = useInfiniteQuery(options);
  const denied = deniedRead(events.error);
  const readAgain = () => {
    if (!canRead || denied || client.getQueryState(key)?.fetchStatus !== 'idle') return;
    if (events.isFetchNextPageError && !isProblem(events.error, 'INVALID_CURSOR')) {
      void events.fetchNextPage({ cancelRefetch: false });
    } else {
      // A normal infinite refetch re-reads every cached page. Restart only one page and
      // let the cache replace the old pages after success, never before a failed read.
      void client.infiniteQuery({ ...options, pages: 1, staleTime: 0 }).catch(() => undefined);
    }
  };
  return { ...events, data: denied ? undefined : events.data, readAgain };
}

/** An audit-specific recovery that preserves the server's Problem and correlation ID. */
export function AuditReadFailure({
  error,
  partial = false,
  busy,
  onRetry,
}: {
  error: unknown;
  partial?: boolean;
  busy: boolean;
  onRetry: () => void;
}) {
  const { t } = useI18n();
  const problem = isProblem(error) ? error : null;
  const uncertain =
    !problem ||
    problem.status === 0 ||
    isProblem(problem, 'STORAGE_COMMIT_OUTCOME_UNKNOWN', 'RESPONSE_INVALID');
  const noRecovery = deniedRead(problem) || isProblem(problem, 'VALIDATION_FAILED');
  const label = t(
    busy
      ? 'audit.read.pending'
      : isProblem(problem, 'INVALID_CURSOR')
        ? 'audit.read.restart'
        : 'audit.read.again',
  );
  return (
    <div className="mb-4" aria-busy={busy}>
      {problem ? (
        <ProblemNotice
          problem={problem}
          title={uncertain ? t('audit.read.unknown.title') : undefined}
          detail={uncertain ? t('audit.read.unknown.detail') : undefined}
          recovery={noRecovery ? undefined : { label, onClick: onRetry, disabled: busy }}
        />
      ) : (
        <Notice
          tone="critical"
          title={t('audit.read.unknown.title')}
          actions={
            <Button disabled={busy} onClick={onRetry}>
              {label}
            </Button>
          }
        >
          {t('audit.read.unknown.detail')}
        </Notice>
      )}
      {partial ? (
        <p className="mb-0 mt-2 text-[13px] text-text-2">{t('audit.read.partial')}</p>
      ) : null}
    </div>
  );
}
