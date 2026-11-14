// Tasks across every node: what is running, what finished and how, and a way to start one. It
// is built on the shared list pattern (docs/research/console-list-pattern.md).
import {
  collectNodeFacts,
  isProblem,
  listNodes,
  listTasks,
  type TaskState,
  type TaskSummary,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
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
  Dialog,
  EmptyState,
  ProblemNotice,
  SearchField,
  Segmented,
  SelectField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useRouter } from '@tanstack/react-router';
import { ListChecks, Play } from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import { operations, states, taskPath } from './task-names.ts';

const filters = ['all', 'running', 'pending', 'failed', 'succeeded'] as const;

type Filter = (typeof filters)[number];

const definition: ListDefinition = {
  key: 'tasks',
  filters: {
    state: { kind: 'choice', values: filters.filter((value) => value !== 'all') },
    node: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'node', label: 'tasks.column.node' },
    { id: 'state', label: 'tasks.column.state' },
    { id: 'started', label: 'tasks.column.started' },
    { id: 'finished', label: 'tasks.column.finished' },
  ],
};

function Rows({ items, list }: { items: readonly TaskSummary[]; list: ListView }) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-hidden">
      <Table>
        <thead>
          <tr>
            <Th>{t('tasks.column.task')}</Th>
            {shows('node') ? <Th>{t('tasks.column.node')}</Th> : null}
            {shows('state') ? <Th>{t('tasks.column.state')}</Th> : null}
            {shows('started') ? <Th>{t('tasks.column.started')}</Th> : null}
            {shows('finished') ? <Th>{t('tasks.column.finished')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((task) => {
            const state = states[task.state];
            return (
              <Tr key={task.id}>
                <Td className="font-medium">
                  <Link
                    to={taskPath(task.id)}
                    className="text-text underline-offset-2 hover:underline"
                  >
                    {t(operations[task.operation])}
                  </Link>
                </Td>
                {shows('node') ? <Td className="text-text-2">{task.node_name}</Td> : null}
                {shows('state') ? (
                  <Td>
                    <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                  </Td>
                ) : null}
                {shows('started') ? (
                  <Td className="text-text-2">{format.dateTime(task.created_at)}</Td>
                ) : null}
                {shows('finished') ? (
                  <Td className="text-text-2">
                    {task.completed_at ? format.dateTime(task.completed_at) : null}
                  </Td>
                ) : null}
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

function StartTask({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  const router = useRouter();
  const queryClient = useQueryClient();
  const lookup = useQuery(runtimeConfigQuery()).data?.ui.lookupPageSize ?? 100;
  const nodes = useQuery({
    queryKey: ['nodes', 'choices', lookup],
    queryFn: ({ signal }) => listNodes({ limit: lookup }, { signal }),
    enabled: open,
  });
  const choices = useMemo(
    () => nodes.data?.items.filter((node) => node.status === 'active') ?? [],
    [nodes.data],
  );
  const [chosen, setChosen] = useState('');
  const node = chosen || (choices[0]?.id ?? '');
  // One key per request, so a retry after a lost answer cannot start a second task.
  const key = useRef<string | null>(null);
  const start = useMutation({
    mutationFn: () => {
      key.current ??= crypto.randomUUID();
      return collectNodeFacts(node, key.current);
    },
    onSuccess: (task) => {
      key.current = null;
      onClose();
      void queryClient.invalidateQueries({ queryKey: ['tasks'] });
      void router.navigate({ href: taskPath(task.id) });
    },
  });
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t('tasks.start.title')}
      description={t('tasks.start.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button
            variant="primary"
            disabled={!node || start.isPending}
            onClick={() => {
              start.mutate();
            }}
          >
            {t('tasks.start.submit')}
          </Button>
        </>
      }
    >
      {nodes.isPending ? <Skeleton className="h-11 w-full" /> : null}
      {nodes.error && isProblem(nodes.error) ? (
        <ProblemNotice
          problem={nodes.error}
          onRetry={() => {
            void nodes.refetch();
          }}
        />
      ) : null}
      {nodes.data && choices.length === 0 ? (
        <p className="m-0">{t('tasks.start.noNodes')}</p>
      ) : null}
      {choices.length > 0 ? (
        <SelectField
          label={t('tasks.start.node')}
          hint={nodes.data?.next_cursor ? t('tasks.start.firstNodes') : undefined}
          value={node}
          options={choices.map((item) => ({ value: item.id, label: item.name }))}
          onChange={(event) => {
            setChosen(event.target.value);
          }}
        />
      ) : null}
      {start.error && isProblem(start.error) ? (
        <ProblemNotice
          className="mt-3"
          problem={start.error}
          onRetry={() => {
            start.mutate();
          }}
        />
      ) : null}
    </Dialog>
  );
}

/** Lists tasks across every node, newest first. */
export function TasksPage() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const list = useListView(definition);
  const state = list.state.filters.state as TaskState | undefined;
  const node = list.state.filters.node;
  const filtered = state !== undefined || node !== undefined;
  const [typed, setTyped] = useTypedFilter(list, 'node', config?.searchDebounceMilliseconds ?? 0);
  const [starting, setStarting] = useState(false);
  const tasks = useInfiniteQuery({
    queryKey: ['tasks', 'list', state, node],
    queryFn: ({ pageParam, signal }) =>
      listTasks({ limit, cursor: pageParam ?? undefined, state, search: node }, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = useMemo(() => tasks.data?.pages.flatMap((page) => page.items) ?? [], [tasks.data]);

  let body;
  if (tasks.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (tasks.error) {
    body = isProblem(tasks.error) ? (
      <ProblemNotice
        problem={tasks.error}
        onRetry={() => {
          void tasks.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={ListChecks}
          title={filtered ? t('tasks.emptyFilter.title') : t('tasks.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('tasks.emptyFilter.clear')}</Button>
            ) : undefined
          }
        >
          {filtered ? t('tasks.emptyFilter.detail') : t('tasks.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        <Rows items={items} list={list} />
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

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('tasks.title')}
          </h1>
          <p className="m-0 text-text-2">{t('tasks.subtitle')}</p>
        </div>
        <Button
          variant="primary"
          onClick={() => {
            setStarting(true);
          }}
        >
          <Play aria-hidden />
          {t('tasks.start')}
        </Button>
      </div>
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('tasks.search')}
          placeholder={t('tasks.search')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <Segmented
          label={t('tasks.filter')}
          value={(state as Filter | undefined) ?? 'all'}
          options={filters.map((value) => ({ value, label: t(`tasks.filter.${value}`) }))}
          onChange={(value) => {
            list.setFilter('state', value === 'all' ? '' : value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      <StartTask
        open={starting}
        onClose={() => {
          setStarting(false);
        }}
      />
    </>
  );
}
