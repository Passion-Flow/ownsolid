// The page an administrator lands on: what to do, how the fleet stands, what needs attention and
// what is left to set up. Everything comes from the lists the pages use, and each source loads and
// fails on its own (docs/research/console-overview.md).
import {
  getLicense,
  getNodeCounts,
  isProblem,
  licenseDaysUntil,
  listInvitations,
  listMailDeliveries,
  listNodes,
  listTasks,
  listUsers,
  type ApiProblem,
  type NodeCounts,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { licenseStateLabels, runtimeConfigQuery, useSession } from '@ownsolid/shell';
import {
  Card,
  CardBody,
  CardHeader,
  cn,
  EmptyState,
  Meter,
  ProblemNotice,
  Skeleton,
  StatusPill,
  Tag,
  type StatusTone,
} from '@ownsolid/ui';
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  BadgeCheck,
  Check,
  CircleAlert,
  CircleCheck,
  ListChecks,
  Server,
  TriangleAlert,
  UserPlus,
  type LucideIcon,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { licenseKey, licenseStanding } from './license-standing.ts';
import { operations } from './task-names.ts';

/** How many failed tasks or emails the overview reads; more says "or more". */
const failuresRead = 25;

/**
 * How many offline nodes are named before the rest are linked. A disabled node is out of service on
 * purpose, so only active ones need anyone.
 */
const offlineNamed = 3;

/** How many running tasks the overview reads; more says "or more". */
const runningRead = 100;

const recentMilliseconds = 86_400_000;

/**
 * A license read verifies the signed history and records the time under a lock, so it is not polled
 * like a list.
 */
const licensePollMilliseconds = 60_000;

const titleClasses = 'm-0 text-[13.5px] font-semibold';
const detailClasses = 'm-0 mt-0.5 text-[13px] text-text-2';
const linkClasses = 'text-[13px] font-semibold text-accent-ink hover:underline';

/** The sources of the page, polled as background reads so an open page keeps no session alive. */
function useSources() {
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const refetchInterval = poll;
  const license = useQuery({
    queryKey: licenseKey,
    queryFn: ({ signal }) => getLicense({ signal, background: true }),
    refetchInterval: licensePollMilliseconds,
  });
  const counts = useQuery({
    queryKey: ['nodes', 'counts'],
    queryFn: ({ signal }) => getNodeCounts({ signal, background: true }),
    refetchInterval,
  });
  const offline = useQuery({
    queryKey: ['overview', 'offline-nodes'],
    queryFn: ({ signal }) =>
      listNodes(
        { limit: offlineNamed, connectivity: 'offline', status: 'active' },
        { signal, background: true },
      ),
    refetchInterval,
  });
  const failedTasks = useQuery({
    queryKey: ['overview', 'failed-tasks'],
    queryFn: ({ signal }) =>
      listTasks({ limit: failuresRead, state: 'failed' }, { signal, background: true }),
    refetchInterval,
  });
  const runningTasks = useQuery({
    queryKey: ['overview', 'running-tasks'],
    queryFn: ({ signal }) =>
      listTasks({ limit: runningRead, state: 'running' }, { signal, background: true }),
    refetchInterval,
  });
  const failedMail = useQuery({
    queryKey: ['overview', 'failed-mail'],
    queryFn: ({ signal }) =>
      listMailDeliveries({ limit: failuresRead, state: 'failed' }, { signal, background: true }),
    refetchInterval,
  });
  const users = useQuery({
    queryKey: ['overview', 'users'],
    queryFn: ({ signal }) => listUsers({ limit: 2 }, { signal, background: true }),
  });
  const invitations = useQuery({
    queryKey: ['overview', 'invitations'],
    queryFn: ({ signal }) =>
      listInvitations({ limit: 1, status: 'pending' }, { signal, background: true }),
  });
  return { license, counts, offline, failedTasks, runningTasks, failedMail, users, invitations };
}

type Sources = ReturnType<typeof useSources>;

/** Moments within the day before `read`, the time the list was read. */
function recent(moments: readonly string[], read: number): number {
  return moments.filter((moment) => read - Date.parse(moment) < recentMilliseconds).length;
}

function QuickAction({
  to,
  hash,
  icon: Icon,
  title,
  detail,
}: {
  to: '/nodes' | '/users' | '/license' | '/tasks';
  hash?: string;
  icon: LucideIcon;
  title: string;
  detail: string;
}) {
  return (
    <Link
      to={to}
      {...(hash ? { hash } : {})}
      className="flex items-center gap-3 rounded-card border border-border bg-surface px-4 py-3.5 shadow-1 transition-[border-color,box-shadow] duration-150 hover:border-accent hover:shadow-[0_0_0_3px_var(--accent-soft)]"
    >
      <span className="grid size-9 flex-none place-items-center rounded-[10px] bg-accent-soft text-accent-ink">
        <Icon aria-hidden />
      </span>
      <span className="min-w-0">
        <b className="block text-[14px] text-text">{title}</b>
        <small className="block truncate text-[12.5px] text-text-3">{detail}</small>
      </span>
    </Link>
  );
}

function Tile({
  to,
  label,
  loading,
  value,
  unit,
  children,
}: {
  to: '/nodes' | '/tasks' | '/license';
  label: string;
  loading: boolean;
  value: ReactNode;
  unit?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <Link
      to={to}
      className="block rounded-card border border-border bg-surface px-[18px] py-4 shadow-1 transition-colors duration-150 hover:border-accent"
    >
      <span className="block text-[13px] font-medium text-text-2">{label}</span>
      {loading ? (
        <Skeleton aria-busy className="mt-2 h-[52px] w-full" />
      ) : (
        <>
          <span className="mt-1.5 block text-[28px] leading-[1.1] font-bold tracking-[-0.03em] text-text tabular-nums">
            {value}
            {unit ? (
              <small className="ml-1 text-[14px] font-medium tracking-normal text-text-3">
                {unit}
              </small>
            ) : null}
          </span>
          <span className="mt-2.5 flex min-h-5 items-center gap-2 text-[12.5px] text-text-3">
            {children}
          </span>
        </>
      )}
    </Link>
  );
}

function Tiles({ sources }: { sources: Sources }) {
  const { t, format } = useI18n();
  const { counts, runningTasks, failedTasks, license } = sources;
  const running = runningTasks.data;
  const failed = failedTasks.data;
  const recentFailed = failed
    ? recent(
        failed.items.map((task) => task.completed_at ?? task.updated_at),
        failedTasks.dataUpdatedAt,
      )
    : 0;
  const standing = license.data;
  const left = standing?.current ? licenseDaysUntil(standing.current.expires_at, standing) : 0;
  return (
    <div className="mb-4 grid gap-4 md:grid-cols-3">
      <Tile
        to="/nodes"
        label={t('overview.tile.nodes')}
        loading={counts.isPending}
        value={counts.data ? format.number(counts.data.online) : '-'}
        unit={counts.data ? t('overview.tile.of', { total: format.number(counts.data.total) }) : ''}
      >
        {!counts.data || counts.data.total === 0 ? (
          t('overview.tile.noNodes')
        ) : counts.data.offline > 0 ? (
          <StatusPill tone="critical">
            {t('overview.tile.offline', { count: counts.data.offline })}
          </StatusPill>
        ) : (
          t('overview.tile.allReporting')
        )}
      </Tile>
      <Tile
        to="/tasks"
        label={t('overview.tile.tasks')}
        loading={runningTasks.isPending}
        value={running ? format.number(running.items.length) : '-'}
        unit={running?.next_cursor ? t('overview.orMore') : ''}
      >
        {failed && recentFailed > 0
          ? t(
              failed.next_cursor && recentFailed === failed.items.length
                ? 'overview.attention.tasksMany'
                : 'overview.tile.failed',
              { count: recentFailed },
            )
          : t('overview.tile.noFailed')}
      </Tile>
      <Tile
        to="/license"
        label={t('overview.tile.license')}
        loading={license.isPending}
        value={standing ? t(licenseStateLabels[standing.state]) : '-'}
      >
        {standing?.current
          ? left > 0
            ? t('overview.tile.licenseLeft', {
                date: format.date(standing.current.expires_at),
                count: left,
              })
            : format.date(standing.current.expires_at)
          : t('overview.tile.noLicense')}
      </Tile>
    </div>
  );
}

interface Attention {
  id: string;
  critical: boolean;
  title: string;
  detail: string;
  action: ReactNode;
}

function attentionItems(
  sources: Sources,
  i18n: Pick<ReturnType<typeof useI18n>, 't' | 'format'>,
): Attention[] {
  const { t, format } = i18n;
  const { license, offline, failedTasks, failedMail } = sources;
  const items: Attention[] = [];
  const standing = license.data ? licenseStanding(license.data, i18n) : null;
  const licenseItem = standing
    ? {
        id: 'license',
        critical: standing.tone === 'critical',
        title: standing.title,
        detail: standing.next,
        action: (
          <Link to="/license" className={linkClasses}>
            {t('overview.attention.license')}
          </Link>
        ),
      }
    : null;
  if (licenseItem?.critical) items.push(licenseItem);
  for (const node of offline.data?.items ?? []) {
    items.push({
      id: `node-${node.id}`,
      critical: true,
      title: node.last_heartbeat_at
        ? t('overview.attention.node', {
            name: node.name,
            date: format.dateTime(node.last_heartbeat_at),
          })
        : t('overview.attention.nodeNever', { name: node.name }),
      detail: t('overview.attention.node.next'),
      action: (
        <Link to="/nodes" search={{ open: node.id }} className={linkClasses}>
          {t('overview.attention.node.open')}
        </Link>
      ),
    });
  }
  if (offline.data?.next_cursor) {
    items.push({
      id: 'nodes-more',
      critical: true,
      title: t('overview.attention.nodesMore'),
      detail: t('overview.attention.node.next'),
      action: (
        <Link
          to="/nodes"
          search={{ connectivity: 'offline', status: 'active' }}
          className={linkClasses}
        >
          {t('overview.attention.nodesMore.open')}
        </Link>
      ),
    });
  }
  const tasks = failedTasks.data;
  if (tasks) {
    const moments = tasks.items.map((task) => task.completed_at ?? task.updated_at);
    const count = recent(moments, failedTasks.dataUpdatedAt);
    const [only] = tasks.items;
    if (count > 0) {
      items.push({
        id: 'tasks',
        critical: false,
        title:
          count === tasks.items.length && tasks.next_cursor
            ? t('overview.attention.tasksMany', { count })
            : t('overview.attention.tasks', { count }),
        detail:
          count === 1 && only
            ? t('overview.attention.task.one', {
                name: t(operations[only.operation]),
                node: only.node_name,
              })
            : t('overview.attention.task.next'),
        action: (
          <Link to="/tasks" search={{ state: 'failed' }} className={linkClasses}>
            {t('overview.attention.tasks.open')}
          </Link>
        ),
      });
    }
  }
  const mail = failedMail.data;
  if (mail) {
    const count = recent(
      mail.items.map((item) => item.completed_at ?? item.updated_at),
      failedMail.dataUpdatedAt,
    );
    if (count > 0) {
      items.push({
        id: 'mail',
        critical: false,
        title:
          count === mail.items.length && mail.next_cursor
            ? t('overview.attention.mailMany', { count })
            : t('overview.attention.mail', { count }),
        detail: t('overview.attention.mail.next'),
        action: (
          <Link to="/mail" search={{ state: 'failed' }} className={linkClasses}>
            {t('overview.attention.mail.open')}
          </Link>
        ),
      });
    }
  }
  if (licenseItem && !licenseItem.critical) items.push(licenseItem);
  return items;
}

const checks: readonly { key: keyof Sources; label: TranslationKey }[] = [
  { key: 'license', label: 'overview.check.license' },
  { key: 'offline', label: 'overview.check.nodes' },
  { key: 'failedTasks', label: 'overview.check.tasks' },
  { key: 'runningTasks', label: 'overview.check.tasks' },
  { key: 'failedMail', label: 'overview.check.mail' },
  { key: 'users', label: 'overview.check.setup' },
  { key: 'invitations', label: 'overview.check.setup' },
];

/** The sources with nothing to show because they failed, one entry per kind of failure. */
function failureGroups(sources: Sources) {
  const groups = new Map<
    string,
    { problem: ApiProblem; labels: Set<TranslationKey>; queries: UseQueryResult[] }
  >();
  for (const { key, label } of checks) {
    const query: UseQueryResult = sources[key];
    if (query.data || !isProblem(query.error)) continue;
    const group = groups.get(query.error.code) ?? {
      problem: query.error,
      labels: new Set<TranslationKey>(),
      queries: [],
    };
    group.labels.add(label);
    group.queries.push(query);
    groups.set(query.error.code, group);
  }
  return [...groups.values()];
}

function NeedsAttention({ sources, items }: { sources: Sources; items: readonly Attention[] }) {
  const { t } = useI18n();
  const failed = failureGroups(sources);
  const loading = checks.some(({ key }) => sources[key].isPending);
  return (
    <Card>
      <CardHeader title={t('overview.attention')} subtitle={t('overview.attention.hint')} />
      <CardBody>
        <div className="flex flex-col gap-3">
          {failed.map(({ problem, labels, queries }) => (
            <div key={problem.code}>
              <p className="m-0 mb-1.5 flex flex-wrap items-center gap-1.5 text-[13px] text-text-2">
                {t('overview.unknownFor')}
                {[...labels].map((label) => (
                  <Tag key={label}>{t(label)}</Tag>
                ))}
              </p>
              <ProblemNotice
                problem={problem}
                onRetry={() => {
                  for (const query of queries) void query.refetch();
                }}
              />
            </div>
          ))}
          {items.length > 0 ? (
            <ul className="m-0 list-none divide-y divide-border p-0">
              {items.map((item) => {
                const Icon = item.critical ? CircleAlert : TriangleAlert;
                return (
                  <li key={item.id} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
                    <span
                      className={cn(
                        'grid size-[30px] flex-none place-items-center rounded-[9px] bg-surface-2',
                        item.critical ? 'text-critical' : 'text-text-2',
                      )}
                    >
                      <Icon aria-hidden className="size-4" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className={titleClasses}>{item.title}</p>
                      <p className={detailClasses}>{item.detail}</p>
                      <div className="mt-1.5">{item.action}</div>
                    </div>
                  </li>
                );
              })}
            </ul>
          ) : null}
          {loading ? <Skeleton aria-busy className="h-[64px] w-full" /> : null}
          {!loading && items.length === 0 && failed.length === 0 ? (
            <EmptyState icon={CircleCheck} title={t('overview.attention.clear')}>
              {t('overview.attention.clear.detail')}
            </EmptyState>
          ) : null}
        </div>
      </CardBody>
    </Card>
  );
}

interface Step {
  id: string;
  label: string;
  done: boolean;
  action?: ReactNode;
}

function FinishSetup({ sources }: { sources: Sources }) {
  const { t } = useI18n();
  const { license, counts, users, invitations } = sources;
  if (license.isPending || counts.isPending || users.isPending || invitations.isPending) {
    return <Skeleton aria-busy className="h-[190px] w-full" />;
  }
  if (!license.data || !counts.data || !users.data || !invitations.data) return null;
  const licensed = license.data.current !== null || license.data.pending !== null;
  const steps: Step[] = [
    { id: 'install', label: t('overview.setup.install'), done: true },
    {
      id: 'license',
      label: t('overview.setup.license'),
      done: licensed,
      action: (
        <Link to="/license" className={linkClasses}>
          {t('overview.setup.license.open')}
        </Link>
      ),
    },
    {
      id: 'node',
      label: t('overview.setup.node'),
      done: counts.data.total > 0,
      action: (
        <Link to="/nodes" hash="add" className={linkClasses}>
          {t('overview.setup.node.open')}
        </Link>
      ),
    },
    {
      id: 'team',
      label: t('overview.setup.team'),
      done: users.data.items.length > 1 || invitations.data.items.length > 0,
      action: (
        <Link to="/users" hash="invite" className={linkClasses}>
          {t('overview.setup.team.open')}
        </Link>
      ),
    },
  ];
  const done = steps.filter((step) => step.done).length;
  if (done === steps.length) return null;
  return (
    <Card>
      <CardHeader
        title={t('overview.setup')}
        subtitle={t('overview.setup.progress', { done, total: steps.length })}
      />
      <CardBody>
        <Meter
          accent
          label={t('overview.setup')}
          value={(done / steps.length) * 100}
          className="mb-2.5 block"
        />
        <ul className="m-0 flex list-none flex-col p-0">
          {steps.map((step) => (
            <li key={step.id} className="flex items-center gap-2.5 py-[7px] text-[13.5px]">
              <span
                className={cn(
                  'grid size-[18px] flex-none place-items-center rounded-full border-[1.5px]',
                  step.done ? 'border-good bg-good text-white' : 'border-text-3',
                )}
              >
                {step.done ? <Check aria-hidden className="size-3" /> : null}
              </span>
              <span className={cn('min-w-0 flex-1', step.done && 'text-text-3 line-through')}>
                {step.done ? <span className="sr-only">{t('overview.setup.done')} </span> : null}
                {step.label}
              </span>
              {step.done ? null : step.action}
            </li>
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}

const fleetStates: readonly {
  key: Exclude<keyof NodeCounts, 'total'>;
  tone: StatusTone;
  label: TranslationKey;
  search: Record<string, string>;
}[] = [
  {
    key: 'online',
    tone: 'good',
    label: 'overview.fleet.online',
    search: { connectivity: 'online' },
  },
  {
    key: 'unknown',
    tone: 'warning',
    label: 'overview.fleet.unknown',
    search: { connectivity: 'unknown' },
  },
  {
    key: 'offline',
    tone: 'critical',
    label: 'overview.fleet.offline',
    search: { connectivity: 'offline' },
  },
  {
    key: 'disabled',
    tone: 'neutral',
    label: 'overview.fleet.disabled',
    search: { status: 'disabled' },
  },
];

function FleetHealth({ counts }: { counts: UseQueryResult<NodeCounts> }) {
  const { t, format } = useI18n();
  let body;
  let note = null;
  if (counts.isPending) {
    body = <Skeleton aria-busy className="h-[120px] w-full" />;
  } else if (!counts.data) {
    body = isProblem(counts.error) ? (
      <ProblemNotice
        problem={counts.error}
        onRetry={() => {
          void counts.refetch();
        }}
      />
    ) : null;
  } else if (counts.data.total === 0) {
    body = (
      <EmptyState
        icon={Server}
        title={t('overview.fleet.empty')}
        action={
          <Link to="/nodes" hash="add" className={linkClasses}>
            {t('overview.setup.node.open')}
          </Link>
        }
      >
        {t('overview.fleet.empty.detail')}
      </EmptyState>
    );
  } else {
    const { total } = counts.data;
    body = (
      <ul className="m-0 flex list-none flex-col gap-1 p-0">
        {fleetStates.map(({ key, tone, label, search }) => {
          const value = counts.data[key];
          return (
            <li key={key}>
              <Link
                to="/nodes"
                search={search}
                className="grid grid-cols-[110px_1fr_40px] items-center gap-3 rounded-control px-1 py-1.5 text-[13.5px] transition-colors duration-150 hover:bg-surface-2"
              >
                <StatusPill tone={tone}>{t(label)}</StatusPill>
                <Meter label={t(label)} value={(value / total) * 100} />
                <span className="text-right text-text tabular-nums">{format.number(value)}</span>
              </Link>
            </li>
          );
        })}
      </ul>
    );
    note = <p className="m-0 mt-2 text-[12.5px] text-text-3">{t('overview.fleet.note')}</p>;
  }
  return (
    <Card>
      <CardHeader title={t('overview.fleet')} />
      <CardBody>
        {body}
        {note}
      </CardBody>
    </Card>
  );
}

/** `/`: what to do next and how the installation stands. */
export function OverviewPage() {
  const i18n = useI18n();
  const { t } = i18n;
  const me = useSession();
  const sources = useSources();
  const items = attentionItems(sources, i18n);
  const failed = failureGroups(sources).length > 0;
  const loading = checks.some(({ key }) => sources[key].isPending);
  const name = me ? me.user.display_name || me.user.email : '';
  let subtitle: string;
  if (items.length > 0) subtitle = t('overview.subtitle.attention', { count: items.length });
  else if (failed) subtitle = t('overview.subtitle.unknown');
  else if (loading) subtitle = t('overview.subtitle.loading');
  else subtitle = t('overview.subtitle.clear');
  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('welcome.title', { name })}
      </h1>
      <p className="m-0 mb-6 text-text-2">{subtitle}</p>
      <div className="mb-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <QuickAction
          to="/nodes"
          hash="add"
          icon={Server}
          title={t('overview.quick.node')}
          detail={t('overview.quick.node.detail')}
        />
        <QuickAction
          to="/users"
          hash="invite"
          icon={UserPlus}
          title={t('overview.quick.invite')}
          detail={t('overview.quick.invite.detail')}
        />
        <QuickAction
          to="/license"
          icon={BadgeCheck}
          title={t('overview.quick.license')}
          detail={t('overview.quick.license.detail')}
        />
        <QuickAction
          to="/tasks"
          icon={ListChecks}
          title={t('overview.quick.tasks')}
          detail={t('overview.quick.tasks.detail')}
        />
      </div>
      <Tiles sources={sources} />
      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <NeedsAttention sources={sources} items={items} />
        <div className="flex min-w-0 flex-col gap-4">
          <FinishSetup sources={sources} />
          <FleetHealth counts={sources.counts} />
        </div>
      </div>
    </>
  );
}
