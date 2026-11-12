// The audit trail: what happened, who did it and how it ended, from a shareable link. Action names
// stay the codes an auditor quotes (docs/research/audit-trail-page.md).
import { isProblem, listAuditEvents, type AuditEvent, type AuditOutcome } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  runtimeConfigQuery,
  SavedViewsMenu,
  useListView,
  useTypedFilter,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  CopyText,
  Detail,
  DetailList,
  Drawer,
  EmptyState,
  FilterSelect,
  Notice,
  ProblemNotice,
  SearchField,
  Skeleton,
  StatusPill,
  Table,
  Tag,
  Td,
  TextField,
  Th,
  Tr,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ScrollText, X } from 'lucide-react';
import { useMemo } from 'react';
import { auditEventSearch, customRange } from './audit-link.ts';
import { ActionName, TargetName, useAuditNames } from './audit-names.tsx';

const categories = [
  'account',
  'audit',
  'auth',
  'dataset',
  'deployment',
  'gateway',
  'inference',
  'installation',
  'invitation',
  'license',
  'mail',
  'middleware',
  'model',
  'node',
  'notification',
  'object',
  'operator',
  'plugin',
  'preference',
  'sso',
  'system',
  'tenant',
  'training',
  'user',
  'workflow',
  'workspace',
] as const;

const outcomes: readonly AuditOutcome[] = ['success', 'failure', 'accepted'];

const outcomeTones: Record<AuditOutcome, StatusTone> = {
  success: 'good',
  failure: 'critical',
  accepted: 'neutral',
};

/** Choices of time, in hours. The longest is the default audit_max_days. */
const ranges: Record<string, number> = { '24h': 24, '7d': 168, '31d': 744 };
const defaultRange = '7d';
const longestWindowMilliseconds = 744 * 3_600_000;

/** How far around an event its own link looks. */

const identifier = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const definition: ListDefinition = {
  key: 'audit',
  filters: {
    category: { kind: 'choice', values: categories },
    outcome: { kind: 'choice', values: outcomes },
    actor: { kind: 'text', maximum: 36 },
    request: { kind: 'text', maximum: 36 },
    range: { kind: 'choice', values: [...Object.keys(ranges), customRange] },
    from: { kind: 'text', maximum: 40 },
    to: { kind: 'text', maximum: 40 },
  },
  columns: [
    { id: 'time', label: 'audit.column.time', locked: true },
    { id: 'person', label: 'audit.column.person' },
    { id: 'action', label: 'audit.column.action' },
    { id: 'target', label: 'audit.column.target' },
    { id: 'outcome', label: 'audit.column.outcome' },
  ],
};

/** What is wrong with a custom window, or null when it can be read. */
function windowProblem(from: string, to: string): TranslationKey | null {
  const start = Date.parse(from);
  const end = Date.parse(to);
  if (Number.isNaN(start) || Number.isNaN(end)) return 'audit.window.incomplete';
  if (start >= end) return 'audit.window.reversed';
  if (end - start > longestWindowMilliseconds) return 'audit.window.tooLong';
  return null;
}

/** A moment as a `datetime-local` field holds it, in the browser's time zone. */
function localField(moment: string): string {
  const parsed = new Date(moment);
  if (Number.isNaN(parsed.getTime())) return '';
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${String(parsed.getFullYear())}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}T${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`;
}

/** What a `datetime-local` field holds, as the API reads it. */
function fromLocalField(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toISOString();
}

/** The address of a list narrowed to one event, which keeps working as time passes. */
function eventLink(event: AuditEvent): string {
  const search = new URLSearchParams(auditEventSearch(event));
  return `${window.location.origin}/audit?${search.toString()}`;
}

const person = (event: AuditEvent, system: string): string =>
  event.actor_email ?? event.actor_user_id ?? system;

function Outcome({ outcome }: { outcome: AuditOutcome }) {
  const { t } = useI18n();
  return <StatusPill tone={outcomeTones[outcome]}>{t(`audit.outcome.${outcome}`)}</StatusPill>;
}

function Rows({ items, list }: { items: readonly AuditEvent[]; list: ListView }) {
  const { t, format } = useI18n();
  const names = useAuditNames();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-hidden">
      <Table>
        <thead>
          <tr>
            <Th>{t('audit.column.time')}</Th>
            {shows('person') ? <Th>{t('audit.column.person')}</Th> : null}
            {shows('action') ? <Th>{t('audit.column.action')}</Th> : null}
            {shows('target') ? <Th>{t('audit.column.target')}</Th> : null}
            {shows('outcome') ? <Th>{t('audit.column.outcome')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((event) => (
            <Tr key={event.id}>
              <Td className="font-medium whitespace-nowrap">
                <button
                  type="button"
                  aria-label={t('audit.detail.open', {
                    action: names.actionText(event.action),
                    time: format.dateTime(event.occurred_at),
                  })}
                  className="cursor-pointer border-0 bg-transparent p-0 text-left text-text underline-offset-2 hover:underline"
                  onClick={() => {
                    list.setOpenRow(event.id);
                  }}
                >
                  {format.dateTime(event.occurred_at)}
                </button>
              </Td>
              {shows('person') ? (
                <Td className="text-text-2">{person(event, t('audit.system'))}</Td>
              ) : null}
              {shows('action') ? (
                <Td>
                  <ActionName action={event.action} />
                </Td>
              ) : null}
              {shows('target') ? (
                <Td className="text-text-2">
                  {event.subject_email ??
                    (event.target_type ? <TargetName type={event.target_type} /> : null)}
                </Td>
              ) : null}
              {shows('outcome') ? (
                <Td>
                  <Outcome outcome={event.outcome} />
                </Td>
              ) : null}
            </Tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function EventDrawer({
  event,
  missing,
  list,
}: {
  event: AuditEvent | null;
  missing: boolean;
  list: ListView;
}) {
  const { t, format } = useI18n();
  const names = useAuditNames();
  const close = () => {
    list.setOpenRow(null);
  };
  const follow = (name: 'actor' | 'request', value: string) => {
    close();
    list.setFilter(name, value);
  };
  return (
    <Drawer
      open={event !== null || missing}
      onClose={close}
      title={event ? names.actionText(event.action) : t('audit.detail.missing')}
      footer={
        event ? (
          <>
            {event.actor_user_id ? (
              <Button
                onClick={() => {
                  if (event.actor_user_id) follow('actor', event.actor_user_id);
                }}
              >
                {t('audit.detail.person')}
              </Button>
            ) : null}
            <Button
              onClick={() => {
                follow('request', event.request_id);
              }}
            >
              {t('audit.detail.request')}
            </Button>
          </>
        ) : null
      }
    >
      {event ? (
        <DetailList>
          <Detail label={t('audit.column.time')}>
            {format.dateTime(event.occurred_at)}
            <CopyText className="mt-1 flex" text={event.occurred_at} />
          </Detail>
          <Detail label={t('audit.column.outcome')}>
            <Outcome outcome={event.outcome} />
          </Detail>
          <Detail label={t('audit.column.person')}>
            {event.actor_email ?? t(event.actor_user_id ? 'audit.unknownPerson' : 'audit.system')}
            {event.actor_user_id ? (
              <CopyText className="mt-1 flex" text={event.actor_user_id} />
            ) : null}
          </Detail>
          <Detail label={t('audit.detail.entrance')}>
            {t(`audit.entrance.${event.audience}`)}
          </Detail>
          {event.subject_user_id ? (
            <Detail label={t('audit.detail.subject')}>
              {event.subject_email ?? t('audit.unknownPerson')}
              <CopyText className="mt-1 flex" text={event.subject_user_id} />
            </Detail>
          ) : null}
          {event.target_type ? (
            <Detail label={t('audit.column.target')}>
              <TargetName type={event.target_type} />
              {event.target_id ? <CopyText className="mt-1 flex" text={event.target_id} /> : null}
            </Detail>
          ) : null}
          {event.reason ? <Detail label={t('audit.detail.reason')}>{event.reason}</Detail> : null}
          <Detail label={t('audit.detail.actionId')}>
            <CopyText text={event.action} />
          </Detail>
          <Detail label={t('audit.detail.requestId')}>
            <CopyText text={event.request_id} />
          </Detail>
          <Detail label={t('audit.detail.link')}>
            <CopyText proportional text={eventLink(event)} />
          </Detail>
        </DetailList>
      ) : (
        <p className="m-0 text-text-2">{t('audit.detail.missing.next')}</p>
      )}
    </Drawer>
  );
}

/** `/audit`: every recorded action, filtered on the server. */
export function AuditPage() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const list = useListView(definition);
  const { category, outcome, actor, request, from, to } = list.state.filters;
  const range = list.state.filters.range ?? defaultRange;
  const custom = range === customRange;
  const [typed, setTyped] = useTypedFilter(
    list,
    'request',
    config?.searchDebounceMilliseconds ?? 0,
  );
  const badRequest = request !== undefined && !identifier.test(request);
  const badActor = actor !== undefined && !identifier.test(actor);
  const badWindow = custom ? windowProblem(from ?? '', to ?? '') : null;

  const events = useInfiniteQuery({
    queryKey: ['audit', category, outcome, actor, request, range, from, to],
    queryFn: ({ pageParam, signal }) => {
      // The clock is read here, once per first page; the cursor carries the window after that.
      const end = new Date();
      const hours = ranges[range] ?? 0;
      return listAuditEvents(
        {
          limit,
          cursor: pageParam ?? undefined,
          category,
          outcome: outcome as AuditOutcome | undefined,
          actorUserId: actor,
          requestId: request,
          from: custom ? from : new Date(end.getTime() - hours * 3_600_000).toISOString(),
          to: custom ? to : end.toISOString(),
        },
        { signal },
      );
    },
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    enabled: !badRequest && !badActor && badWindow === null,
  });
  const items = useMemo(
    () => events.data?.pages.flatMap((page) => page.items) ?? [],
    [events.data],
  );
  const opened = list.openRow ? (items.find((item) => item.id === list.openRow) ?? null) : null;
  const missing = list.openRow !== null && !opened && events.isSuccess && !events.hasNextPage;
  const actorName = items.find((item) => item.actor_user_id === actor)?.actor_email ?? actor ?? '';
  const filtered =
    category !== undefined ||
    outcome !== undefined ||
    actor !== undefined ||
    request !== undefined ||
    range !== defaultRange;

  let body;
  if (badRequest || badActor) {
    body = <Notice tone="critical" title={t('audit.request.invalid')} />;
  } else if (badWindow) {
    body = <Notice title={t(badWindow)} />;
  } else if (events.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (events.error && items.length === 0) {
    body = isProblem(events.error) ? (
      <ProblemNotice
        problem={events.error}
        onRetry={() => {
          void events.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={ScrollText}
          title={t(filtered ? 'audit.emptyFilter.title' : 'audit.empty.title')}
          action={
            filtered ? <Button onClick={list.clearFilters}>{t('audit.clear')}</Button> : undefined
          }
        >
          {t(filtered ? 'audit.emptyFilter.detail' : 'audit.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        <Rows items={items} list={list} />
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

  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('audit.title')}
      </h1>
      <p className="m-0 mb-6 max-w-[760px] text-text-2">{t('audit.subtitle')}</p>
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('audit.request')}
          placeholder={t('audit.request.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value.trim());
          }}
        />
        <FilterSelect
          label={t('audit.filter.category')}
          value={category ?? ''}
          options={[
            { value: '', label: t('audit.filter.category.all') },
            ...categories.map((value) => ({ value, label: t(`audit.category.${value}`) })),
          ]}
          onChange={(event) => {
            list.setFilter('category', event.target.value);
          }}
        />
        <FilterSelect
          label={t('audit.filter.outcome')}
          value={outcome ?? ''}
          options={[
            { value: '', label: t('audit.filter.outcome.all') },
            ...outcomes.map((value) => ({ value, label: t(`audit.outcome.${value}`) })),
          ]}
          onChange={(event) => {
            list.setFilter('outcome', event.target.value);
          }}
        />
        <FilterSelect
          label={t('audit.filter.range')}
          value={range}
          options={[
            ...Object.keys(ranges).map((value) => ({
              value,
              label: t(`audit.range.${value}` as TranslationKey),
            })),
            { value: customRange, label: t('audit.range.custom') },
          ]}
          onChange={(event) => {
            const next = event.target.value;
            list.setFilter('range', next === defaultRange ? '' : next);
            if (next !== customRange) {
              list.setFilter('from', '');
              list.setFilter('to', '');
            }
          }}
        />
        {actor !== undefined ? (
          <span className="inline-flex items-center gap-1">
            <Tag>{t('audit.filter.person', { name: actorName })}</Tag>
            <Button
              size="sm"
              variant="ghost"
              aria-label={t('audit.filter.person.clear')}
              onClick={() => {
                list.setFilter('actor', '');
              }}
            >
              <X aria-hidden />
            </Button>
          </span>
        ) : null}
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {custom ? (
        <div className="mb-3.5 grid max-w-[560px] gap-3 sm:grid-cols-2">
          <TextField
            label={t('audit.window.from')}
            name="from"
            type="datetime-local"
            value={localField(from ?? '')}
            onChange={(event) => {
              list.setFilter('from', fromLocalField(event.target.value));
            }}
          />
          <TextField
            label={t('audit.window.to')}
            name="to"
            type="datetime-local"
            value={localField(to ?? '')}
            onChange={(event) => {
              list.setFilter('to', fromLocalField(event.target.value));
            }}
          />
        </div>
      ) : null}
      {body}
      <EventDrawer event={opened} missing={missing} list={list} />
    </>
  );
}
