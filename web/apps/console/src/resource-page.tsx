// What every resource's own page shares: its header, its tab in the address, its failures and its activity
// (docs/research/resource-detail-pages.md).
import { isProblem, listAuditEvents, type AuditOutcome } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  Card,
  EmptyState,
  ProblemNotice,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Link, useRouter, useRouterState } from '@tanstack/react-router';
import { ArrowLeft, History, SearchX } from 'lucide-react';
import type { ReactNode } from 'react';
import { auditEventSearch } from './audit-link.ts';

const outcomeTones: Record<AuditOutcome, StatusTone> = {
  success: 'good',
  accepted: 'neutral',
  failure: 'critical',
};

/** The chosen tab lives in the address, so a view of a resource can be sent to a colleague. */
export function useChosenTab<T extends string>(tabs: readonly [T, ...T[]]): [T, (tab: T) => void] {
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const chosen = new URLSearchParams(location.searchStr).get('tab');
  const first = tabs[0];
  return [
    tabs.find((tab) => tab === chosen) ?? first,
    (tab) => {
      const search = tab === first ? '' : `?tab=${tab}`;
      void router.navigate({ href: `${location.pathname}${search}`, replace: true });
    },
  ];
}

/** A failed read that says what happened and retries. */
export function ResourceFailure({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return isProblem(error) ? <ProblemNotice problem={error} onRetry={onRetry} /> : null;
}

function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
    >
      <ArrowLeft aria-hidden className="size-4" />
      {label}
    </Link>
  );
}

/** The way back, the name and state, and the actions of a resource's page. */
export function ResourceHeader({
  backTo,
  backLabel,
  title,
  description,
  status,
  actions,
}: {
  backTo: string;
  backLabel: string;
  title: string;
  description?: string | undefined;
  status: ReactNode;
  actions: ReactNode;
}) {
  return (
    <>
      <BackLink to={backTo} label={backLabel} />
      <div className="mb-6 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em] [overflow-wrap:anywhere]">
            {title}
          </h1>
          {description ? <p className="m-0 mb-2 text-text-2">{description}</p> : null}
          {status}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
    </>
  );
}

/** A resource's page while its read has no answer: waiting, gone, or failed. */
export function ResourceUnavailable({
  pending,
  error,
  onRetry,
  backTo,
  backLabel,
  missingTitle,
  missingDetail,
  failedTitle,
}: {
  pending: boolean;
  error: unknown;
  onRetry: () => void;
  backTo: string;
  backLabel: string;
  missingTitle: string;
  missingDetail: string;
  failedTitle: string;
}) {
  if (pending) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-1/2" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  const failed = error !== null && !isProblem(error, 'RESOURCE_NOT_FOUND', 'VALIDATION_FAILED');
  return (
    <>
      <BackLink to={backTo} label={backLabel} />
      <h1 className="m-0 mb-4 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {failed ? failedTitle : missingTitle}
      </h1>
      {failed ? (
        <ResourceFailure error={error} onRetry={onRetry} />
      ) : (
        <Card>
          <EmptyState icon={SearchX} title={missingTitle}>
            {missingDetail}
          </EmptyState>
        </Card>
      )}
    </>
  );
}

/** What the audit trail holds about a resource, or by a person, since it began. */
export function ResourceActivity({
  target,
  actor,
  since,
}: {
  /** The events about this resource. */
  target?: string;
  /** The events this person caused. */
  actor?: string;
  /** When it began: its whole life is shown, not the trail's default window. */
  since: string;
}) {
  const { t, format } = useI18n();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const events = useInfiniteQuery({
    queryKey: ['resource-activity', target, actor, since, limit],
    queryFn: ({ pageParam, signal }) =>
      listAuditEvents(
        // The minute covers the moment it began, whose event may carry a slightly earlier time.
        {
          limit,
          cursor: pageParam ?? undefined,
          targetId: target,
          actorUserId: actor,
          from: new Date(Date.parse(since) - 60_000).toISOString(),
        },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  if (events.isPending) return <Skeleton className="h-11 w-full" />;
  if (events.isError && !events.data) {
    return (
      <ResourceFailure
        error={events.error}
        onRetry={() => {
          void events.refetch();
        }}
      />
    );
  }
  const items = events.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return (
      <Card>
        <EmptyState icon={History} title={t('resource.activity.empty.title')}>
          {t('resource.activity.empty.detail')}
        </EmptyState>
      </Card>
    );
  }
  return (
    <>
      <Card className="overflow-hidden">
        <Table>
          <thead>
            <tr>
              <Th>{t('audit.column.time')}</Th>
              <Th>{t('audit.column.person')}</Th>
              <Th>{t('audit.column.action')}</Th>
              <Th>{t('audit.column.outcome')}</Th>
            </tr>
          </thead>
          <tbody>
            {items.map((event) => (
              <Tr key={event.id}>
                <Td className="font-medium whitespace-nowrap">
                  <Link
                    to="/audit"
                    search={auditEventSearch(event)}
                    className="text-text underline-offset-2 hover:underline"
                  >
                    {format.dateTime(event.occurred_at)}
                  </Link>
                </Td>
                <Td className="text-text-2">
                  {event.actor_email ?? event.actor_user_id ?? t('audit.system')}
                </Td>
                <Td>
                  <code className="font-mono text-[12.5px]">{event.action}</code>
                </Td>
                <Td>
                  <StatusPill tone={outcomeTones[event.outcome]}>
                    {t(`audit.outcome.${event.outcome}`)}
                  </StatusPill>
                </Td>
              </Tr>
            ))}
          </tbody>
        </Table>
      </Card>
      {events.hasNextPage ? (
        <Button
          className="mt-4"
          disabled={events.isFetchingNextPage}
          onClick={() => {
            void events.fetchNextPage();
          }}
        >
          {t('audit.loadMore')}
        </Button>
      ) : null}
    </>
  );
}
