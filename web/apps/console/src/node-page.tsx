// One node on its own page: what it is, what it has run, and what people did to it
// (docs/research/resource-detail-pages.md). The drawer over the list stays the quick look.
import { getNode, isProblem, listNodeTasks, type Node } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import { Button, Card, EmptyState, Skeleton, StatusPill, Tabs } from '@ownsolid/ui';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ListChecks } from 'lucide-react';
import { useNodeActions } from './node-actions.tsx';
import { NodeFacts, NodeTaskRows, QuietNotice } from './node-details.tsx';
import { nodeState } from './node-state.ts';
import {
  ResourceActivity,
  ResourceFailure,
  ResourceHeader,
  ResourceUnavailable,
  useChosenTab,
} from './resource-page.tsx';

const route = getRouteApi('/authenticated/frame/nodes/$nodeId');

const tabs = ['overview', 'tasks', 'activity'] as const;

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
      <ResourceFailure
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

function Loaded({ node }: { node: Node }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [tab, setTab] = useChosenTab(tabs);
  const actions = useNodeActions(node, {
    onChanged: () => undefined,
    onRemoved: () => {
      void navigate({ to: '/nodes' });
    },
  });
  const state = nodeState(node);
  return (
    <>
      <ResourceHeader
        backTo="/nodes"
        backLabel={t('nodes.page.back')}
        title={node.name}
        status={<StatusPill tone={state.tone}>{t(state.label)}</StatusPill>}
        actions={actions.buttons}
      />
      {actions.notices}
      {actions.collected ? (
        <p role="status" className="mt-0 mb-4 text-text-2">
          {t('nodes.collect.started')}
        </p>
      ) : null}
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`resource.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'overview' ? (
          <Card className="p-[18px]">
            <QuietNotice node={node} />
            <NodeFacts node={node} />
          </Card>
        ) : null}
        {tab === 'tasks' ? <TasksTab node={node} /> : null}
        {tab === 'activity' ? <ResourceActivity target={node.id} /> : null}
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
  return (
    <ResourceUnavailable
      pending={node.isPending}
      error={node.error}
      onRetry={() => {
        void node.refetch();
      }}
      backTo="/nodes"
      backLabel={t('nodes.page.back')}
      missingTitle={t('nodes.page.missing.title')}
      missingDetail={t('nodes.page.missing.detail')}
      failedTitle={t('nodes.detail.missing')}
    />
  );
}
