// What every resource's own page shares: its header, its tab in the address, its failures and its activity
// (docs/research/resource-detail-pages.md).
import { isProblem } from '@ownsolid/api';
import { Card, EmptyState, ProblemNotice, Skeleton, cn } from '@ownsolid/ui';
import { Link, useRouter, useRouterState } from '@tanstack/react-router';
import { ArrowLeft, SearchX } from 'lucide-react';
import type { ReactNode } from 'react';
export { ResourceActivity } from './resource-activity.tsx';

/** The chosen tab lives in the address, so a view of a resource can be sent to a colleague. */
export function useChosenTab<T extends string>(tabs: readonly [T, ...T[]]): [T, (tab: T) => void] {
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const chosen = new URLSearchParams(location.searchStr).get('tab');
  const first = tabs[0];
  return [
    tabs.find((tab) => tab === chosen) ?? first,
    (tab) => {
      const params = new URLSearchParams(location.searchStr);
      if (tab === first) params.delete('tab');
      else params.set('tab', tab);
      const search = params.size ? `?${params.toString()}` : '';
      void router.navigate({ href: `${location.pathname}${search}`, replace: true });
    },
  ];
}

/** A failed read that says what happened and retries. */
export function ResourceFailure({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return isProblem(error) ? <ProblemNotice problem={error} onRetry={onRetry} /> : null;
}

function BackLink({
  to,
  search,
  label,
}: {
  to: string;
  search?: Record<string, string> | undefined;
  label: string;
}) {
  return (
    <Link
      to={to}
      {...(search ? { search } : {})}
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
  backSearch,
  backLabel,
  title,
  description,
  status,
  actions,
  className,
}: {
  backTo: string;
  /** The way back's filters or tab, when they are worth returning to. */
  backSearch?: Record<string, string> | undefined;
  backLabel: string;
  title: string;
  description?: string | undefined;
  status: ReactNode;
  actions: ReactNode;
  /** Layout adjustments for the name, status and action row. */
  className?: string;
}) {
  return (
    <>
      <BackLink to={backTo} search={backSearch} label={backLabel} />
      <div className={cn('mb-6 flex flex-wrap items-start gap-3', className)}>
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
