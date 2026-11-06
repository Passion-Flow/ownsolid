// Email delivery: what the platform sent, what failed and why, and a resend for failures. It is
// the first page built on the shared list pattern (docs/research/console-list-pattern.md).
import {
  isProblem,
  listMailDeliveries,
  resendMailDelivery,
  type MailDelivery,
  type MailPurpose,
  type MailState,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  SavedViewsMenu,
  runtimeConfigQuery,
  useListView,
  useRowSelection,
  type ListDefinition,
  type ListView,
  type RowSelection,
} from '@ownsolid/shell';
import {
  BulkBar,
  Button,
  Card,
  Checkbox,
  CopyText,
  Drawer,
  EmptyState,
  FilterSelect,
  ProblemNotice,
  SearchField,
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
import { ArrowDown, ArrowUp, Mail, RotateCw } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';

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

const purposeNames = Object.keys(purposes) as MailPurpose[];

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

const definition: ListDefinition = {
  key: 'mail-deliveries',
  filters: {
    state: { kind: 'choice', values: filters.filter((value) => value !== 'all') },
    purpose: { kind: 'choice', values: purposeNames },
    recipient: { kind: 'text', maximum: 254 },
    // Newest first unless the link says otherwise.
    order: { kind: 'choice', values: ['oldest'] },
  },
  columns: [
    { id: 'recipient', label: 'mail.column.recipient', locked: true },
    { id: 'purpose', label: 'mail.column.purpose' },
    { id: 'state', label: 'mail.column.state' },
    { id: 'reason', label: 'mail.column.reason' },
    { id: 'attempts', label: 'mail.column.attempts' },
    { id: 'created', label: 'mail.column.created' },
  ],
};

function Reason({ delivery }: { delivery: MailDelivery }) {
  const { t, format } = useI18n();
  if (delivery.state === 'queued' && delivery.next_attempt_at && delivery.attempt_count > 0) {
    return <>{t('mail.retryAt', { time: format.time(delivery.next_attempt_at) })}</>;
  }
  if (!delivery.error_category) return <span className="text-text-3">-</span>;
  return <>{t(reasons[delivery.error_category] ?? 'mail.reason.other')}</>;
}

/** Resends, with one key per email until it succeeds, so a retry cannot send it twice. */
function useResend() {
  const queryClient = useQueryClient();
  const keys = useRef(new Map<string, string>());
  return useMemo(
    () => ({
      send: async (id: string) => {
        const key = keys.current.get(id) ?? crypto.randomUUID();
        keys.current.set(id, key);
        const sent = await resendMailDelivery(id, key);
        keys.current.delete(id);
        return sent;
      },
      refresh: () => queryClient.invalidateQueries({ queryKey: listKey }),
    }),
    [queryClient],
  );
}

function ResendAll({ selection }: { selection: RowSelection }) {
  const { t } = useI18n();
  const toast = useToast();
  const resend = useResend();
  const run = useMutation({
    mutationFn: async (ids: readonly string[]) => {
      const results = await Promise.allSettled(ids.map((id) => resend.send(id)));
      return ids.filter((_, index) => results[index]?.status === 'rejected');
    },
    onSuccess: async (failed, ids) => {
      selection.select(failed);
      await resend.refresh();
      if (failed.length === 0) {
        toast({ message: t('mail.bulk.resent', { count: ids.length }) });
        return;
      }
      toast({
        message: t('mail.bulk.partial', {
          sent: ids.length - failed.length,
          failed: failed.length,
        }),
        retry: () => {
          run.mutate(failed);
        },
      });
    },
  });
  return (
    <Button
      size="sm"
      variant="ghost"
      disabled={run.isPending}
      className="border-[color-mix(in_srgb,var(--bg)_30%,transparent)] text-bg hover:bg-[color-mix(in_srgb,var(--bg)_14%,transparent)] hover:text-bg"
      onClick={() => {
        run.mutate([...selection.selected]);
      }}
    >
      <RotateCw aria-hidden />
      {t('mail.bulk.resend')}
    </Button>
  );
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-text-3">{label}</dt>
      <dd className="m-0 min-w-0 break-words">{children}</dd>
    </>
  );
}

function DeliveryDrawer({
  delivery,
  onClose,
}: {
  delivery: MailDelivery | null;
  onClose: () => void;
}) {
  const { t, format } = useI18n();
  const toast = useToast();
  const resend = useResend();
  const send = useMutation({
    mutationFn: (id: string) => resend.send(id),
    onSuccess: async () => {
      toast({ message: t('mail.resent') });
      await resend.refresh();
      onClose();
    },
  });
  const state = delivery ? states[delivery.state] : null;
  return (
    <Drawer
      open={delivery !== null}
      onClose={onClose}
      title={delivery?.recipient_email}
      footer={
        delivery?.state === 'failed' ? (
          <Button
            variant="primary"
            disabled={send.isPending}
            onClick={() => {
              send.mutate(delivery.id);
            }}
          >
            <RotateCw aria-hidden />
            {t('mail.resend')}
          </Button>
        ) : undefined
      }
    >
      {send.error && isProblem(send.error) ? (
        <ProblemNotice
          className="mb-4"
          problem={send.error}
          onRetry={() => {
            if (delivery) send.mutate(delivery.id);
          }}
        />
      ) : null}
      {delivery && state ? (
        <dl className="m-0 grid grid-cols-[130px_1fr] gap-y-2.5 text-[13.5px]">
          <Detail label={t('mail.column.state')}>
            <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
          </Detail>
          <Detail label={t('mail.column.purpose')}>{t(purposes[delivery.purpose])}</Detail>
          <Detail label={t('mail.column.reason')}>
            <Reason delivery={delivery} />
          </Detail>
          <Detail label={t('mail.column.attempts')}>{format.number(delivery.attempt_count)}</Detail>
          <Detail label={t('mail.detail.language')}>{delivery.locale}</Detail>
          <Detail label={t('mail.detail.created')}>{format.dateTime(delivery.created_at)}</Detail>
          {delivery.next_attempt_at ? (
            <Detail label={t('mail.detail.nextAttempt')}>
              {format.dateTime(delivery.next_attempt_at)}
            </Detail>
          ) : null}
          {delivery.completed_at ? (
            <Detail label={t('mail.detail.completed')}>
              {format.dateTime(delivery.completed_at)}
            </Detail>
          ) : null}
          <Detail label={t('mail.detail.id')}>
            <CopyText text={delivery.id} />
          </Detail>
        </dl>
      ) : null}
    </Drawer>
  );
}

interface RowsProps {
  items: readonly MailDelivery[];
  list: ListView;
  selection: RowSelection;
  selectable: readonly string[];
  onOpen: (id: string) => void;
}

function Rows({ items, list, selection, selectable, onOpen }: RowsProps) {
  const { t, format } = useI18n();
  const toast = useToast();
  const resend = useResend();
  const send = useMutation({
    mutationFn: (id: string) => resend.send(id),
    onSuccess: () => {
      toast({ message: t('mail.resent') });
      return resend.refresh();
    },
  });
  const shows = list.isColumnVisible;
  return (
    <>
      {send.error && isProblem(send.error) ? (
        <ProblemNotice
          className="mb-4"
          problem={send.error}
          onRetry={() => {
            send.mutate(send.variables);
          }}
        />
      ) : null}
      <Card className="overflow-x-auto">
        <Table>
          <caption className="sr-only">{t('mail.title')}</caption>
          <thead>
            <tr>
              <Th className="w-11 pr-0">
                {selectable.length > 0 ? (
                  <Checkbox
                    aria-label={t('list.selectAll')}
                    checked={selection.allSelected}
                    indeterminate={selection.someSelected}
                    onChange={selection.toggleAll}
                  />
                ) : null}
              </Th>
              <Th>{t('mail.column.recipient')}</Th>
              {shows('purpose') ? <Th>{t('mail.column.purpose')}</Th> : null}
              {shows('state') ? <Th>{t('mail.column.state')}</Th> : null}
              {shows('reason') ? <Th>{t('mail.column.reason')}</Th> : null}
              {shows('attempts') ? (
                <Th className="text-right">{t('mail.column.attempts')}</Th>
              ) : null}
              {shows('created') ? (
                <Th aria-sort={list.state.filters.order === 'oldest' ? 'ascending' : 'descending'}>
                  <button
                    type="button"
                    className="inline-flex cursor-pointer items-center gap-1 rounded-control border-0 bg-transparent p-0 font-semibold text-text-3 hover:text-text"
                    onClick={() => {
                      list.setFilter(
                        'order',
                        list.state.filters.order === 'oldest' ? '' : 'oldest',
                      );
                    }}
                  >
                    {t('mail.column.created')}
                    {list.state.filters.order === 'oldest' ? (
                      <ArrowUp aria-hidden className="size-3.5" />
                    ) : (
                      <ArrowDown aria-hidden className="size-3.5" />
                    )}
                    <span className="sr-only">
                      {list.state.filters.order === 'oldest'
                        ? t('list.sort.toNewest')
                        : t('list.sort.toOldest')}
                    </span>
                  </button>
                </Th>
              ) : null}
              <Th>
                <span className="sr-only">{t('mail.column.actions')}</span>
              </Th>
            </tr>
          </thead>
          <tbody>
            {items.map((delivery) => {
              const state = states[delivery.state];
              const pending = send.isPending && send.variables === delivery.id;
              const canSend = delivery.state === 'failed';
              const selected = selection.isSelected(delivery.id);
              return (
                <Tr key={delivery.id} data-selected={selected ? '' : undefined}>
                  <Td className="w-11 pr-0">
                    {canSend ? (
                      <Checkbox
                        aria-label={t('list.selectRow', { name: delivery.recipient_email })}
                        checked={selected}
                        onChange={(event) => {
                          selection.toggle(delivery.id, event.target.checked);
                        }}
                      />
                    ) : null}
                  </Td>
                  <Td className="font-medium">
                    <button
                      type="button"
                      aria-label={t('mail.detail.open', { email: delivery.recipient_email })}
                      className="cursor-pointer rounded-control border-0 bg-transparent p-0 font-medium text-text hover:underline"
                      onClick={() => {
                        onOpen(delivery.id);
                      }}
                    >
                      {delivery.recipient_email}
                    </button>
                  </Td>
                  {shows('purpose') ? <Td>{t(purposes[delivery.purpose])}</Td> : null}
                  {shows('state') ? (
                    <Td>
                      <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                    </Td>
                  ) : null}
                  {shows('reason') ? (
                    <Td className="min-w-[260px] whitespace-normal text-text-2">
                      <Reason delivery={delivery} />
                    </Td>
                  ) : null}
                  {shows('attempts') ? (
                    <Td className="text-right">{format.number(delivery.attempt_count)}</Td>
                  ) : null}
                  {shows('created') ? (
                    <Td className="text-text-2">{format.dateTime(delivery.created_at)}</Td>
                  ) : null}
                  <Td className="text-right">
                    {canSend ? (
                      <Button
                        size="sm"
                        disabled={pending}
                        onClick={() => {
                          send.mutate(delivery.id);
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
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const debounce = config?.searchDebounceMilliseconds ?? 0;
  const list = useListView(definition);
  const applied = list.state.filters;
  const state = applied.state as MailState | undefined;
  const purpose = applied.purpose as MailPurpose | undefined;
  const recipient = applied.recipient;
  const order = applied.order as 'oldest' | undefined;

  // The field follows typing; the list follows the field after a pause.
  const [typed, setTyped] = useState(recipient ?? '');
  const [appliedRecipient, setAppliedRecipient] = useState(recipient);
  if (recipient !== appliedRecipient) {
    setAppliedRecipient(recipient);
    setTyped(recipient ?? '');
  }
  const { setFilter } = list;
  useEffect(() => {
    if (typed.trim() === (recipient ?? '')) return undefined;
    const timer = setTimeout(() => {
      setFilter('recipient', typed.trim());
    }, debounce);
    return () => {
      clearTimeout(timer);
    };
    // setFilter changes with the state it writes; the pause depends only on the text.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [typed, recipient, debounce]);

  const deliveries = useInfiniteQuery({
    queryKey: [...listKey, state, purpose, recipient, order],
    queryFn: ({ pageParam, signal }) =>
      listMailDeliveries(
        {
          limit,
          cursor: pageParam ?? undefined,
          state,
          purpose,
          recipientPrefix: recipient,
          order,
        },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = useMemo(
    () => deliveries.data?.pages.flatMap((page) => page.items) ?? [],
    [deliveries.data],
  );
  const selectable = useMemo(
    () => items.filter((item) => item.state === 'failed').map((item) => item.id),
    [items],
  );
  const selection = useRowSelection(selectable);
  const open = items.find((item) => item.id === list.openRow) ?? null;
  const filtered = Object.keys(applied).some((name) => name !== 'order');

  let body;
  if (deliveries.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (deliveries.error) {
    body = isProblem(deliveries.error) ? (
      <ProblemNotice
        problem={deliveries.error}
        onRetry={() => {
          void deliveries.refetch();
        }}
      />
    ) : null;
  } else {
    body =
      items.length === 0 ? (
        <Card>
          <EmptyState
            icon={Mail}
            title={filtered ? t('mail.emptyFilter.title') : t('mail.empty.title')}
            action={
              filtered ? (
                <Button onClick={list.clearFilters}>{t('mail.emptyFilter.clear')}</Button>
              ) : undefined
            }
          >
            {filtered ? t('mail.emptyFilter.detail') : t('mail.empty.detail')}
          </EmptyState>
        </Card>
      ) : (
        <>
          <Rows
            items={items}
            list={list}
            selection={selection}
            selectable={selectable}
            onOpen={list.setOpenRow}
          />
          {deliveries.hasNextPage ? (
            <Button
              className="mt-4"
              disabled={deliveries.isFetchingNextPage}
              onClick={() => {
                void deliveries.fetchNextPage();
              }}
            >
              {t('mail.loadMore')}
            </Button>
          ) : null}
          <BulkBar count={selection.selected.size} onClear={selection.clear}>
            <ResendAll selection={selection} />
          </BulkBar>
        </>
      );
  }

  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('mail.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('mail.subtitle')}</p>
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('mail.search')}
          placeholder={t('mail.search.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <Segmented
          label={t('mail.filter')}
          value={(state as Filter | undefined) ?? 'all'}
          options={filters.map((value) => ({ value, label: t(`mail.filter.${value}`) }))}
          onChange={(value) => {
            list.setFilter('state', value === 'all' ? '' : value);
          }}
        />
        <FilterSelect
          label={t('mail.filter.purpose')}
          value={purpose ?? ''}
          options={[
            { value: '', label: t('mail.filter.purpose.all') },
            ...purposeNames.map((value) => ({ value, label: t(purposes[value]) })),
          ]}
          onChange={(event) => {
            list.setFilter('purpose', event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      <DeliveryDrawer
        delivery={open}
        onClose={() => {
          list.setOpenRow(null);
        }}
      />
    </>
  );
}
