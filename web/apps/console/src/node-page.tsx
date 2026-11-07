// One node on its own page: what it is, what it has run, and what people did to it
// (docs/research/resource-detail-pages.md). The drawer over the list stays the quick look.
import {
  getNode,
  isProblem,
  listAuditEvents,
  listNodeTasks,
  type AuditOutcome,
  type Node,
} from '@ownsolid/api';
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
  Tabs,
  Td,
  Th,
  Tr,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Link, getRouteApi, useNavigate, useRouter, useRouterState } from '@tanstack/react-router';
import { ArrowLeft, History, ListChecks, Server } from 'lucide-react';
import { auditEventSearch } from './audit-link.ts';
import { useNodeActions } from './node-actions.tsx';
import { NodeFacts, NodeTaskRows, QuietNotice } from './node-details.tsx';
import { nodeState } from './node-state.ts';

const route = getRouteApi('/authenticated/frame/nodes/$nodeId');

const tabs = ['overview', 'tasks', 'activity'] as const;
type Tab = (typeof tabs)[number];

const outcomeTones: Record<AuditOutcome, StatusTone> = {
  success: 'good',
  accepted: 'neutral',
  failure: 'critical',
};

function isTab(value: string | null): value is Tab {
  return tabs.some((tab) => tab === value);
}

/** The chosen tab lives in the address, so a view of a node can be sent to a colleague. */
function useChosenTab(): [Tab, (tab: Tab) => void] {
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const chosen = new URLSearchParams(location.searchStr).get('tab');
  return [
    isTab(chosen) ? chosen : 'overview',
    (tab) => {
      const search = tab === 'overview' ? '' : `?tab=${tab}`;
      void router.navigate({ href: `${location.pathname}${search}`, replace: true });
    },
  ];
}

function Failure({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return isProblem(error) ? <ProblemNotice problem={error} onRetry={onRetry} /> : null;
}

function TasksTab({ node }: { node: Node }) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const tasks = useInfiniteQuery({
    queryKey: ['nodes', node.id, 'tasks', 'all', limit],
    queryFn: ({ pageParam, signal }) =>
      listNodeTasks(
        node.id,
        { limit, cursor: pageParam ?? undefined },
        { signal, background: true },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  if (tasks.isPending) return <Skeleton className="h-11 w-full" />;
  if (tasks.isError && !tasks.data) {
    return (
      <Failure
        error={tasks.error}
        onRetry={() => {
          void tasks.refetch();
        }}
      />
    );
  }
  const items = tasks.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return (
      <Card>
        <EmptyState icon={ListChecks} title={t('nodes.page.tasks.empty.title')}>
          {t('nodes.page.tasks.empty.detail')}
        </EmptyState>
      </Card>
    );
  }
  return (
    <>
      <Card className="px-[18px] py-1">
        <NodeTaskRows tasks={items} />
      </Card>
      {tasks.hasNextPage ? (
        <Button
          className="mt-4"
          disabled={tasks.isFetchingNextPage}
          onClick={() => {
            void tasks.fetchNextPage();
          }}
        >
          {t('tasks.loadMore')}
        </Button>
      ) : null}
    </>
  );
}

function ActivityTab({ node }: { node: Node }) {
  const { t, format } = useI18n();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const events = useInfiniteQuery({
    queryKey: ['nodes', node.id, 'activity', limit],
    queryFn: ({ pageParam, signal }) =>
      listAuditEvents(
        // Its whole life, not the audit trail's default window. The minute covers the moment it joined,
        // whose event may carry a slightly earlier time than the row.
        {
          limit,
          cursor: pageParam ?? undefined,
          targetId: node.id,
          from: new Date(Date.parse(node.created_at) - 60_000).toISOString(),
        },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  if (events.isPending) return <Skeleton className="h-11 w-full" />;
  if (events.isError && !events.data) {
    return (
      <Failure
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
        <EmptyState icon={History} title={t('nodes.page.activity.empty.title')}>
          {t('nodes.page.activity.empty.detail')}
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

function BackLink() {
  const { t } = useI18n();
  return (
    <Link
      to="/nodes"
      className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
    >
      <ArrowLeft aria-hidden className="size-4" />
      {t('nodes.page.back')}
    </Link>
  );
}

function Loaded({ node }: { node: Node }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [tab, setTab] = useChosenTab();
  const actions = useNodeActions(node, {
    onChanged: () => undefined,
    onRemoved: () => {
      void navigate({ to: '/nodes' });
    },
  });
  const state = nodeState(node);
  return (
    <>
      <BackLink />
      <div className="mb-6 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em] [overflow-wrap:anywhere]">
            {node.name}
          </h1>
          <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
        </div>
        {actions.buttons ? (
          <div className="flex flex-wrap items-center gap-2">{actions.buttons}</div>
        ) : null}
      </div>
      {actions.notices}
      {actions.collected ? (
        <p role="status" className="mt-0 mb-4 text-text-2">
          {t('nodes.collect.started')}
        </p>
      ) : null}
      <Tabs
        label={t('nodes.page.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`nodes.page.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'overview' ? (
          <Card className="p-[18px]">
            <QuietNotice node={node} />
            <NodeFacts node={node} />
          </Card>
        ) : null}
        {tab === 'tasks' ? <TasksTab node={node} /> : null}
        {tab === 'activity' ? <ActivityTab node={node} /> : null}
      </Tabs>
      {actions.dialogs}
    </>
  );
}

/** `/nodes/$nodeId`: one node, reachable by a link that names it. */
export function NodePage() {
  const { t } = useI18n();
  const { nodeId } = route.useParams();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const node = useQuery({
    queryKey: ['nodes', 'detail', nodeId],
    queryFn: ({ signal }) => getNode(nodeId, { signal, background: true }),
    refetchInterval: poll,
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  if (node.data) return <Loaded node={node.data} />;
  if (node.isPending) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-1/2" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (isProblem(node.error, 'RESOURCE_NOT_FOUND', 'VALIDATION_FAILED')) {
    return (
      <>
        <BackLink />
        <h1 className="m-0 mb-4 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
          {t('nodes.page.missing.title')}
        </h1>
        <Card>
          <EmptyState icon={Server} title={t('nodes.page.missing.title')}>
            {t('nodes.page.missing.detail')}
          </EmptyState>
        </Card>
      </>
    );
  }
  return (
    <>
      <BackLink />
      <h1 className="m-0 mb-4 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('nodes.detail.missing')}
      </h1>
      <Failure
        error={node.error}
        onRetry={() => {
          void node.refetch();
        }}
      />
    </>
  );
}
