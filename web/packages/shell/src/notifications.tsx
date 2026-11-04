// The bell and the notification drawer. The unread count refreshes on the installation's
// poll interval; the list loads when the drawer opens.
import {
  getNotificationSummary,
  isProblem,
  listNotifications,
  markNotificationRead,
  type RunNotification,
  type RunOutcome,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  Button,
  Drawer,
  EmptyState,
  ProblemNotice,
  Skeleton,
  StatusPill,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell } from 'lucide-react';
import { useState } from 'react';
import { runtimeConfigQuery } from './routes.tsx';

const summaryKey = ['notifications', 'summary'] as const;
const listKey = ['notifications', 'list'] as const;

const outcomes: Record<RunOutcome, { tone: StatusTone; label: TranslationKey }> = {
  succeeded: { tone: 'good', label: 'notifications.outcome.succeeded' },
  failed: { tone: 'critical', label: 'notifications.outcome.failed' },
  cancelled: { tone: 'neutral', label: 'notifications.outcome.cancelled' },
  interrupted: { tone: 'warning', label: 'notifications.outcome.interrupted' },
};

function Row({ notification }: { notification: RunNotification }) {
  const { t, locale } = useI18n();
  const queryClient = useQueryClient();
  const read = useMutation({
    mutationFn: () => markNotificationRead(notification.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });
  const outcome = outcomes[notification.outcome];
  const time = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(notification.occurred_at),
  );
  return (
    <li className="flex items-start gap-3 border-b border-border py-3 last:border-b-0">
      <div className="min-w-0 flex-1">
        <StatusPill tone={outcome.tone}>{t(outcome.label)}</StatusPill>
        {notification.error_code ? (
          <p className="m-0 mt-1 font-mono text-[12px] text-text-2">{notification.error_code}</p>
        ) : null}
        <p className="m-0 mt-0.5 text-[12.5px] text-text-3">{time}</p>
        {read.error && isProblem(read.error) ? (
          <ProblemNotice
            className="mt-2"
            problem={read.error}
            onRetry={() => {
              read.mutate();
            }}
          />
        ) : null}
      </div>
      {notification.read_at ? null : (
        <Button
          size="sm"
          disabled={read.isPending}
          onClick={() => {
            read.mutate();
          }}
        >
          {t('notifications.markRead')}
        </Button>
      )}
    </li>
  );
}

function NotificationList() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data;
  const limit = config?.ui.pageSize ?? 20;
  const list = useInfiniteQuery({
    queryKey: listKey,
    queryFn: ({ pageParam, signal }) => listNotifications(limit, pageParam, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });

  if (list.isPending) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
      </div>
    );
  }
  if (list.error) {
    return isProblem(list.error) ? (
      <ProblemNotice
        problem={list.error}
        onRetry={() => {
          void list.refetch();
        }}
      />
    ) : null;
  }
  const items = list.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return (
      <EmptyState icon={Bell} title={t('notifications.empty.title')}>
        {t('notifications.empty.detail')}
      </EmptyState>
    );
  }
  return (
    <>
      <ul className="m-0 list-none p-0">
        {items.map((item) => (
          <Row key={item.id} notification={item} />
        ))}
      </ul>
      {list.hasNextPage ? (
        <Button
          className="mt-3 w-full"
          disabled={list.isFetchingNextPage}
          onClick={() => {
            void list.fetchNextPage();
          }}
        >
          {t('notifications.loadMore')}
        </Button>
      ) : null}
    </>
  );
}

/** The bell button with its unread dot, and the drawer it opens. */
export function NotificationBell() {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const config = useQuery(runtimeConfigQuery()).data;
  const summary = useQuery({
    queryKey: summaryKey,
    queryFn: ({ signal }) => getNotificationSummary({ signal }),
    refetchInterval: config?.ui.pollMilliseconds ?? false,
  });
  if (summary.data && !summary.data.available) return null;
  const unread = summary.data?.unread_count ?? 0;
  const label = t('notifications.bell', { count: unread });
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="relative"
        aria-label={label}
        title={label}
        onClick={() => {
          setOpen(true);
        }}
      >
        <Bell aria-hidden />
        {unread > 0 ? (
          <span
            aria-hidden
            className="absolute top-2 right-[9px] size-[7px] rounded-full border-2 border-bg bg-critical"
          />
        ) : null}
      </Button>
      <Drawer
        open={open}
        onClose={() => {
          setOpen(false);
        }}
        title={t('notifications.title')}
        description={
          summary.data
            ? t('notifications.retention', { count: summary.data.retention_days })
            : undefined
        }
      >
        <NotificationList />
      </Drawer>
    </>
  );
}
