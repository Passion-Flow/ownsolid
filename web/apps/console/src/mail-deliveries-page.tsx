// Email delivery: what the platform sent, what failed and why, and a resend for failures.
import {
  isProblem,
  listMailDeliveries,
  resendMailDelivery,
  type MailDelivery,
  type MailPurpose,
  type MailState,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  Card,
  EmptyState,
  ProblemNotice,
  Segmented,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
  useToast,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Mail, RotateCw } from 'lucide-react';
import { useRef, useState } from 'react';

type Filter = 'all' | 'failed' | 'queued' | 'sent';

const filters: readonly Filter[] = ['all', 'failed', 'queued', 'sent'];

const states: Record<MailState, { tone: StatusTone; label: TranslationKey }> = {
  queued: { tone: 'neutral', label: 'mail.state.queued' },
  sending: { tone: 'neutral', label: 'mail.state.sending' },
  sent: { tone: 'good', label: 'mail.state.sent' },
  failed: { tone: 'critical', label: 'mail.state.failed' },
  cancelled: { tone: 'neutral', label: 'mail.state.cancelled' },
};

const purposes: Record<MailPurpose, TranslationKey> = {
  invitation: 'mail.purpose.invitation',
  password_reset: 'mail.purpose.password_reset',
  password_changed: 'mail.purpose.password_changed',
};

// The outbox's error categories. An unknown category shows as a generic reason.
const reasons: Record<string, TranslationKey> = {
  smtp_unavailable: 'mail.reason.smtp_unavailable',
  smtp_auth_rejected: 'mail.reason.smtp_auth_rejected',
  smtp_tls_rejected: 'mail.reason.smtp_tls',
  smtp_tls_required: 'mail.reason.smtp_tls',
  smtp_recipient_rejected: 'mail.reason.smtp_recipient_rejected',
  smtp_unknown_outcome: 'mail.reason.smtp_unknown_outcome',
  token_obsolete: 'mail.reason.token_obsolete',
};

const listKey = ['mail-deliveries'] as const;

function Reason({ delivery }: { delivery: MailDelivery }) {
  const { t, locale } = useI18n();
  if (delivery.state === 'queued' && delivery.next_attempt_at && delivery.attempt_count > 0) {
    const at = new Intl.DateTimeFormat(locale, { timeStyle: 'short' }).format(
      new Date(delivery.next_attempt_at),
    );
    return <>{t('mail.retryAt', { time: at })}</>;
  }
  if (!delivery.error_category) return <span className="text-text-3">-</span>;
  return <>{t(reasons[delivery.error_category] ?? 'mail.reason.other')}</>;
}

function Rows({ items }: { items: readonly MailDelivery[] }) {
  const { t, locale } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  // One key per email until its resend succeeds, so retrying a failed request cannot
  // send the email twice.
  const keys = useRef(new Map<string, string>());
  const resend = useMutation({
    mutationFn: (id: string) => {
      const key = keys.current.get(id) ?? crypto.randomUUID();
      keys.current.set(id, key);
      return resendMailDelivery(id, key);
    },
    onSuccess: (_, id) => {
      keys.current.delete(id);
      toast({ message: t('mail.resent') });
      return queryClient.invalidateQueries({ queryKey: listKey });
    },
  });
  const format = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' });
  return (
    <>
      {resend.error && isProblem(resend.error) ? (
        <ProblemNotice
          className="mb-4"
          problem={resend.error}
          onRetry={() => {
            resend.mutate(resend.variables);
          }}
        />
      ) : null}
      <Card className="overflow-x-auto">
        <Table>
          <caption className="sr-only">{t('mail.title')}</caption>
          <thead>
            <tr>
              <Th>{t('mail.column.recipient')}</Th>
              <Th>{t('mail.column.purpose')}</Th>
              <Th>{t('mail.column.state')}</Th>
              <Th>{t('mail.column.reason')}</Th>
              <Th className="text-right">{t('mail.column.attempts')}</Th>
              <Th>{t('mail.column.created')}</Th>
              <Th>
                <span className="sr-only">{t('mail.column.actions')}</span>
              </Th>
            </tr>
          </thead>
          <tbody>
            {items.map((delivery) => {
              const state = states[delivery.state];
              const pending = resend.isPending && resend.variables === delivery.id;
              return (
                <Tr key={delivery.id}>
                  <Td className="font-medium">{delivery.recipient_email}</Td>
                  <Td>{t(purposes[delivery.purpose])}</Td>
                  <Td>
                    <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                  </Td>
                  <Td className="whitespace-normal text-text-2">
                    <Reason delivery={delivery} />
                  </Td>
                  <Td className="text-right">{delivery.attempt_count}</Td>
                  <Td className="text-text-2">{format.format(new Date(delivery.created_at))}</Td>
                  <Td className="text-right">
                    {delivery.state === 'failed' ? (
                      <Button
                        size="sm"
                        disabled={pending}
                        onClick={() => {
                          resend.mutate(delivery.id);
                        }}
                      >
                        <RotateCw aria-hidden />
                        {t('mail.resend')}
                        <span className="sr-only">
                          {t('mail.resendTo', { email: delivery.recipient_email })}
                        </span>
                      </Button>
                    ) : null}
                  </Td>
                </Tr>
              );
            })}
          </tbody>
        </Table>
      </Card>
    </>
  );
}

/** Lists the platform's emails, newest first, with a resend for each one that failed. */
export function MailDeliveriesPage() {
  const { t } = useI18n();
  const [filter, setFilter] = useState<Filter>('all');
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const state = filter === 'all' ? undefined : filter;
  const list = useInfiniteQuery({
    queryKey: [...listKey, state],
    queryFn: ({ pageParam, signal }) =>
      listMailDeliveries({ limit, cursor: pageParam ?? undefined, state }, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });

  let body;
  if (list.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (list.error) {
    body = isProblem(list.error) ? (
      <ProblemNotice
        problem={list.error}
        onRetry={() => {
          void list.refetch();
        }}
      />
    ) : null;
  } else {
    const items = list.data.pages.flatMap((page) => page.items);
    body =
      items.length === 0 ? (
        <Card>
          <EmptyState
            icon={Mail}
            title={filter === 'all' ? t('mail.empty.title') : t('mail.emptyFilter.title')}
          >
            {filter === 'all' ? t('mail.empty.detail') : t('mail.emptyFilter.detail')}
          </EmptyState>
        </Card>
      ) : (
        <>
          <Rows items={items} />
          {list.hasNextPage ? (
            <Button
              className="mt-4"
              disabled={list.isFetchingNextPage}
              onClick={() => {
                void list.fetchNextPage();
              }}
            >
              {t('mail.loadMore')}
            </Button>
          ) : null}
        </>
      );
  }

  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('mail.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('mail.subtitle')}</p>
      <Segmented
        className="mb-4"
        label={t('mail.filter')}
        value={filter}
        options={filters.map((value) => ({ value, label: t(`mail.filter.${value}`) }))}
        onChange={setFilter}
      />
      {body}
    </>
  );
}
