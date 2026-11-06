// The fleet: every node, why it is or is not working, and adding more. Built on the shared list
// pattern (docs/research/console-list-pattern.md) and docs/research/nodes-page.md.
import {
  collectNodeFacts,
  getNode,
  getNodeCounts,
  isProblem,
  listJoinTokens,
  listNodes,
  revokeJoinToken,
  updateNode,
  type JoinToken,
  type Node,
  type NodeConnectivity,
  type NodeStatus,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  SavedViewsMenu,
  runtimeConfigQuery,
  useListView,
  useRowSelection,
  useTypedFilter,
  type ListDefinition,
  type ListView,
  type RowSelection,
} from '@ownsolid/shell';
import {
  BulkAction,
  BulkBar,
  Button,
  Card,
  Checkbox,
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
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Ban, Gauge, Plus, RefreshCw, Server } from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import { AddNodeDialog } from './add-node-dialog.tsx';
import { NodeDrawer } from './node-drawer.tsx';
import { nodeState } from './node-state.ts';

type Filter = 'all' | NodeConnectivity;

const filters: readonly Filter[] = ['all', 'online', 'unknown', 'offline'];
const statuses: readonly NodeStatus[] = ['active', 'disabled', 'removed'];

const statusNames: Record<NodeStatus, TranslationKey> = {
  active: 'nodes.filter.status.active',
  disabled: 'nodes.filter.status.disabled',
  removed: 'nodes.filter.status.removed',
};

const definition: ListDefinition = {
  key: 'nodes',
  filters: {
    connectivity: { kind: 'choice', values: filters.filter((value) => value !== 'all') },
    status: { kind: 'choice', values: statuses },
    name: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'name', label: 'nodes.column.name', locked: true },
    { id: 'state', label: 'nodes.column.state' },
    { id: 'system', label: 'nodes.column.system' },
    { id: 'resources', label: 'nodes.column.resources' },
    { id: 'agent', label: 'nodes.column.agent' },
    { id: 'seen', label: 'nodes.column.seen' },
  ],
};

/** Tokens minted but not used yet, each with a way to stop it working. */
function PendingJoins() {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const tokens = useQuery({
    queryKey: ['nodes', 'join-tokens'],
    queryFn: ({ signal }) =>
      listJoinTokens({ limit: config?.lookupPageSize ?? 100 }, { signal, background: true }),
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  const revoke = useMutation({
    mutationFn: (token: JoinToken) => revokeJoinToken(token.id, t('nodes.pending.reason')),
    onSuccess: (token) => {
      toast({ message: t('nodes.pending.revoked', { name: token.node_name }) });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['nodes', 'join-tokens'] }),
  });
  const items = tokens.data?.items ?? [];
  if (items.length === 0 && !revoke.error) return null;
  return (
    <section aria-labelledby="pending-joins" className="mb-3.5">
      {revoke.error && isProblem(revoke.error) ? (
        <ProblemNotice className="mb-3" problem={revoke.error} />
      ) : null}
      {items.length > 0 ? (
        <Card className="px-4 py-3">
          <h2 id="pending-joins" className="m-0 mb-1.5 text-[13px] font-semibold text-text-2">
            {t('nodes.pending.title')}
          </h2>
          <ul className="m-0 list-none p-0">
            {items.map((token) => (
              <li
                key={token.id}
                className="flex flex-wrap items-center justify-between gap-3 border-b border-border py-2 last:border-b-0"
              >
                <span>
                  <span className="font-medium">{token.node_name}</span>
                  <span className="ml-2 text-[12.5px] text-text-3">
                    {t('nodes.pending.expires', { time: format.dateTime(token.expires_at) })}
                  </span>
                </span>
                <Button
                  size="sm"
                  disabled={revoke.isPending}
                  onClick={() => {
                    revoke.mutate(token);
                  }}
                >
                  {t('nodes.pending.revoke')}
                  <span className="sr-only">{token.node_name}</span>
                </Button>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
    </section>
  );
}

/** Applies one action to each selected node; a refusal keeps its row selected for a retry. */
function useBulk<T extends Node>(
  selection: RowSelection,
  run: (node: T) => Promise<unknown>,
  messages: { done: (count: number) => string; partial: (done: number, failed: number) => string },
  undo?: (nodes: readonly T[]) => Promise<unknown>,
) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['nodes'] });
  const bulk = useMutation({
    mutationFn: async (nodes: readonly T[]) => {
      const results = await Promise.allSettled(nodes.map(run));
      return {
        done: nodes.filter((_, index) => results[index]?.status === 'fulfilled'),
        failed: nodes.filter((_, index) => results[index]?.status === 'rejected'),
      };
    },
    onSuccess: async ({ done, failed }) => {
      selection.select(failed.map((node) => node.id));
      await refresh();
      if (failed.length === 0) {
        toast({
          message: messages.done(done.length),
          ...(undo ? { undo: () => undo(done).finally(refresh) } : {}),
        });
        return;
      }
      toast({
        message: messages.partial(done.length, failed.length),
        retry: () => {
          bulk.mutate(failed);
        },
      });
    },
  });
  return bulk;
}

function BulkActions({ items, selection }: { items: readonly Node[]; selection: RowSelection }) {
  const { t } = useI18n();
  const chosen = items.filter((node) => selection.isSelected(node.id));
  const working = chosen.filter((node) => node.status === 'active');
  // One key per node until it succeeds, so a retry cannot start a second task.
  const keys = useRef(new Map<string, string>());
  const collect = useBulk(
    selection,
    async (node: Node) => {
      const key = keys.current.get(node.id) ?? crypto.randomUUID();
      keys.current.set(node.id, key);
      await collectNodeFacts(node.id, key);
      keys.current.delete(node.id);
    },
    {
      done: (count) => t('nodes.bulk.collected', { count }),
      partial: (done, failed) => t('nodes.bulk.collectPartial', { done, failed }),
    },
  );
  const disable = useBulk(
    selection,
    (node: Node) =>
      updateNode(node.id, {
        name: node.name,
        status: 'disabled',
        version: node.version,
        reason: t('nodes.reason.disable'),
      }),
    {
      done: (count) => t('nodes.bulk.disabled', { count }),
      partial: (done, failed) => t('nodes.bulk.disablePartial', { done, failed }),
    },
    async (nodes) => {
      // Each node's version moved by one when it was disabled.
      await Promise.all(
        nodes.map((node) =>
          updateNode(node.id, {
            name: node.name,
            status: 'active',
            version: node.version + 1,
            reason: t('nodes.reason.undo'),
          }),
        ),
      );
    },
  );
  return (
    <>
      <BulkAction
        disabled={collect.isPending || working.length === 0}
        onClick={() => {
          collect.mutate(working);
        }}
      >
        <RefreshCw aria-hidden />
        {t('nodes.collect.open')}
      </BulkAction>
      <BulkAction
        disabled={disable.isPending || working.length === 0}
        onClick={() => {
          disable.mutate(working);
        }}
      >
        <Ban aria-hidden />
        {t('nodes.disable.open')}
      </BulkAction>
    </>
  );
}

function Rows({
  items,
  list,
  selection,
  selectable,
}: {
  items: readonly Node[];
  list: ListView;
  selection: RowSelection;
  selectable: readonly string[];
}) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('nodes.title')}</caption>
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
            <Th>{t('nodes.column.name')}</Th>
            {shows('state') ? <Th>{t('nodes.column.state')}</Th> : null}
            {shows('system') ? <Th>{t('nodes.column.system')}</Th> : null}
            {shows('resources') ? <Th>{t('nodes.column.resources')}</Th> : null}
            {shows('agent') ? <Th>{t('nodes.column.agent')}</Th> : null}
            {shows('seen') ? <Th>{t('nodes.column.seen')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((node) => {
            const state = nodeState(node);
            const selected = selection.isSelected(node.id);
            return (
              <Tr key={node.id} data-selected={selected ? '' : undefined}>
                <Td className="w-11 pr-0">
                  {node.status === 'removed' ? null : (
                    <Checkbox
                      aria-label={t('list.selectRow', { name: node.name })}
                      checked={selected}
                      onChange={(event) => {
                        selection.toggle(node.id, event.target.checked);
                      }}
                    />
                  )}
                </Td>
                <Td>
                  <button
                    type="button"
                    aria-label={t('nodes.detail.open', { name: node.name })}
                    className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                    onClick={() => {
                      list.setOpenRow(node.id);
                    }}
                  >
                    {node.name}
                  </button>
                  <div className="font-mono text-[12px] text-text-2">{node.facts.hostname}</div>
                </Td>
                {shows('state') ? (
                  <Td>
                    <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                  </Td>
                ) : null}
                {shows('system') ? (
                  <Td>
                    {`${node.facts.os_id} ${node.facts.os_version}`}
                    <div className="text-[12px] text-text-2">{node.facts.architecture}</div>
                  </Td>
                ) : null}
                {shows('resources') ? (
                  <Td>
                    {t('nodes.cpus', { count: node.facts.logical_cpus })}
                    <div className="text-[12px] text-text-2">
                      {format.bytes(node.facts.memory_total_bytes)}
                    </div>
                  </Td>
                ) : null}
                {shows('agent') ? <Td>{node.facts.agent_version}</Td> : null}
                {shows('seen') ? (
                  <Td className="text-text-2">{format.dateTime(node.last_heartbeat_at)}</Td>
                ) : null}
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

/** Lists the fleet's nodes, newest first, with their state and how to add more. */
export function NodesPage() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const poll = config?.pollMilliseconds ?? false;
  const list = useListView(definition);
  const applied = list.state.filters;
  const connectivity = applied.connectivity as NodeConnectivity | undefined;
  const status = applied.status as NodeStatus | undefined;
  const name = applied.name;
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const [adding, setAdding] = useState(false);

  const nodes = useInfiniteQuery({
    queryKey: ['nodes', 'list', connectivity, status, name],
    queryFn: ({ pageParam, signal }) =>
      listNodes(
        { limit, cursor: pageParam ?? undefined, connectivity, status, namePrefix: name },
        { signal, background: true },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: poll,
  });
  const counts = useQuery({
    queryKey: ['nodes', 'counts'],
    queryFn: ({ signal }) => getNodeCounts({ signal, background: true }),
    refetchInterval: poll,
  });
  const items = useMemo(() => nodes.data?.pages.flatMap((page) => page.items) ?? [], [nodes.data]);
  const selectable = useMemo(
    () => items.filter((node) => node.status !== 'removed').map((node) => node.id),
    [items],
  );
  const selection = useRowSelection(selectable);

  // A link can name a node beyond the pages on screen, so it is read on its own.
  const opened = items.find((node) => node.id === list.openRow);
  const detail = useQuery({
    queryKey: ['nodes', 'detail', list.openRow],
    queryFn: ({ signal }) => getNode(list.openRow ?? '', { signal }),
    enabled: list.openRow !== null && !opened,
    retry: false,
  });
  const openNode = opened ?? (list.openRow !== null ? detail.data : undefined) ?? null;

  const filtered = Object.keys(applied).length > 0;
  const count = (filter: Filter): number | undefined => {
    if (!counts.data) return undefined;
    return filter === 'all' ? counts.data.total : counts.data[filter];
  };

  let body;
  if (nodes.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (!nodes.data) {
    body = isProblem(nodes.error) ? (
      <ProblemNotice
        problem={nodes.error}
        onRetry={() => {
          void nodes.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={Server}
          title={filtered ? t('nodes.emptyFilter.title') : t('nodes.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('nodes.emptyFilter.clear')}</Button>
            ) : (
              <Button
                variant="primary"
                onClick={() => {
                  setAdding(true);
                }}
              >
                <Plus aria-hidden />
                {t('nodes.add.open')}
              </Button>
            )
          }
        >
          {filtered ? t('nodes.emptyFilter.detail') : t('nodes.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        {/* A failed poll keeps what is on screen and says so. */}
        {isProblem(nodes.error) ? (
          <ProblemNotice
            className="mb-3.5"
            problem={nodes.error}
            onRetry={() => {
              void nodes.refetch();
            }}
          />
        ) : null}
        <Rows items={items} list={list} selection={selection} selectable={selectable} />
        {nodes.hasNextPage ? (
          <Button
            className="mt-4"
            disabled={nodes.isFetchingNextPage}
            onClick={() => {
              void nodes.fetchNextPage();
            }}
          >
            {t('nodes.loadMore')}
          </Button>
        ) : null}
        <BulkBar count={selection.selected.size} onClear={selection.clear}>
          <BulkActions items={items} selection={selection} />
        </BulkBar>
      </>
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('nodes.title')}
          </h1>
          <p className="m-0 text-text-2">{t('nodes.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link
            to="/nodes/pacing"
            className="inline-flex h-9 items-center gap-[7px] rounded-field border border-border-strong bg-surface px-3.5 font-[550] text-text hover:bg-surface-2"
          >
            <Gauge aria-hidden className="size-4" />
            {t('nodes.pacing.open')}
          </Link>
          <Button
            variant="primary"
            onClick={() => {
              setAdding(true);
            }}
          >
            <Plus aria-hidden />
            {t('nodes.add.open')}
          </Button>
        </div>
      </div>
      <PendingJoins />
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('nodes.search')}
          placeholder={t('nodes.search.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <Segmented
          label={t('nodes.filter')}
          value={connectivity ?? 'all'}
          options={filters.map((value) => {
            const number = count(value);
            return {
              value,
              label:
                number === undefined
                  ? t(`nodes.filter.${value}`)
                  : t(`nodes.filter.${value}.count`, { count: number }),
            };
          })}
          onChange={(value) => {
            list.setFilter('connectivity', value === 'all' ? '' : value);
          }}
        />
        <FilterSelect
          label={t('nodes.filter.status')}
          value={status ?? ''}
          options={[
            { value: '', label: t('nodes.filter.status.all') },
            ...statuses.map((value) => ({ value, label: t(statusNames[value]) })),
          ]}
          onChange={(event) => {
            list.setFilter('status', event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      <NodeDrawer
        node={openNode}
        problem={list.openRow !== null && !opened ? detail.error : null}
        onClose={() => {
          list.setOpenRow(null);
        }}
        onRetry={() => {
          void detail.refetch();
        }}
      />
      {adding ? (
        <AddNodeDialog
          onClose={() => {
            setAdding(false);
          }}
          onOpenNode={(node) => {
            setAdding(false);
            list.setOpenRow(node.id);
          }}
        />
      ) : null}
    </>
  );
}
