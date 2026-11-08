// The keys that call a workspace's models through the gateway: who holds each, what it may call and
// when it stops, filtered by the tiles above them, with each key's calls in its drawer
// (docs/research/gateway-keys.md).
import {
  getUser,
  isProblem,
  listGatewayKeyCalls,
  listGatewayKeys,
  type GatewayCall,
  type GatewayKey,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  runtimeConfigQuery,
  SavedViewsMenu,
  useHashAction,
  useListView,
  useTypedFilter,
  useWorkspaces,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  Detail,
  DetailList,
  Drawer,
  EmptyState,
  FilterTiles,
  Notice,
  ProblemNotice,
  SearchField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
} from '@ownsolid/ui';
import { useInfiniteQuery, useQueries, useQuery } from '@tanstack/react-query';
import { KeyRound, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { everyPage, pageSize } from './every-page.ts';
import { IssueKeyDialog, RevokeKeyDialog, RotateKeyDialog } from './gateway-key-dialogs.tsx';
import {
  canRevoke,
  canRotate,
  gatewayKeys,
  inTile,
  keyStateLabel,
  keyTiles,
  keyTone,
  readyTiles,
  type KeyTile,
} from './gateway-key-state.ts';
import { usersKey } from './user-surface.tsx';

const definition: ListDefinition = {
  key: 'gateway-keys',
  filters: {
    tile: { kind: 'choice', values: readyTiles.filter((tile) => tile !== 'all') },
    name: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'name', label: 'gateway.column.name', locked: true },
    { id: 'prefix', label: 'gateway.column.prefix' },
    { id: 'scope', label: 'gateway.column.scope' },
    { id: 'owner', label: 'gateway.column.owner' },
    { id: 'lastUsed', label: 'gateway.column.lastUsed' },
    { id: 'expiry', label: 'gateway.column.expiry' },
    { id: 'state', label: 'gateway.column.state', locked: true },
  ],
};

type Dialog = { kind: 'rotate' | 'revoke'; key: GatewayKey } | null;

/** The first block of an identifier, for a person whose name could not be read. */
function shortId(id: string): string {
  return id.slice(0, 8);
}

/** Names the people who own keys. A workspace's keys belong to few people, read one by one. */
function useOwnerNames(keys: readonly GatewayKey[]): (id: string) => string {
  const ids = useMemo(() => [...new Set(keys.map((key) => key.created_by))], [keys]);
  const people = useQueries({
    queries: ids.map((id) => ({
      queryKey: [...usersKey, 'detail', id],
      queryFn: ({ signal }: { signal: AbortSignal }) => getUser(id, { signal }),
    })),
  });
  const names = new Map(
    people.flatMap((query) =>
      query.data ? [[query.data.id, query.data.display_name || query.data.email] as const] : [],
    ),
  );
  return (id) => names.get(id) ?? shortId(id);
}

function Waiting() {
  return (
    <div className="flex flex-col gap-3" aria-busy>
      <Skeleton className="h-11 w-full" />
      <Skeleton className="h-11 w-full" />
    </div>
  );
}

function KeyState({ gatewayKey }: { gatewayKey: GatewayKey }) {
  const { t } = useI18n();
  return <StatusPill tone={keyTone(gatewayKey)}>{t(keyStateLabel(gatewayKey))}</StatusPill>;
}

function Rows({
  items,
  list,
  owner,
}: {
  items: readonly GatewayKey[];
  list: ListView;
  owner: (id: string) => string;
}) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('gateway.title')}</caption>
        <thead>
          <tr>
            <Th>{t('gateway.column.name')}</Th>
            {shows('prefix') ? <Th>{t('gateway.column.prefix')}</Th> : null}
            {shows('scope') ? <Th>{t('gateway.column.scope')}</Th> : null}
            {shows('owner') ? <Th>{t('gateway.column.owner')}</Th> : null}
            {shows('lastUsed') ? <Th>{t('gateway.column.lastUsed')}</Th> : null}
            {shows('expiry') ? <Th>{t('gateway.column.expiry')}</Th> : null}
            <Th>{t('gateway.column.state')}</Th>
          </tr>
        </thead>
        <tbody>
          {items.map((key) => (
            <Tr key={key.id}>
              <Td>
                <button
                  type="button"
                  aria-label={t('gateway.detail.open', { name: key.label })}
                  className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                  onClick={() => {
                    list.setOpenRow(key.id);
                  }}
                >
                  {key.label}
                </button>
              </Td>
              {shows('prefix') ? (
                <Td className="font-mono text-[12.5px] whitespace-nowrap">{key.prefix}</Td>
              ) : null}
              {shows('scope') ? (
                <Td className="font-mono text-[12.5px]">{key.alias_name}</Td>
              ) : null}
              {shows('owner') ? <Td>{owner(key.created_by)}</Td> : null}
              {shows('lastUsed') ? (
                <Td className="whitespace-nowrap">
                  {key.last_used_at ? format.dateTime(key.last_used_at) : t('gateway.neverUsed')}
                </Td>
              ) : null}
              {shows('expiry') ? (
                <Td className="whitespace-nowrap">{format.dateTime(key.expires_at)}</Td>
              ) : null}
              <Td>
                <KeyState gatewayKey={key} />
              </Td>
            </Tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

/** One call's outcome in words: how it ended, or that it has not been reported. */
function Outcome({ call }: { call: GatewayCall }) {
  const { t, knows } = useI18n();
  if (!call.outcome) return <StatusPill tone="live">{t('gateway.call.running')}</StatusPill>;
  const key = `gateway.call.status.${call.outcome.status}`;
  return (
    <StatusPill tone={call.outcome.status === 'succeeded' ? 'good' : 'critical'}>
      {knows(key) ? t(key) : call.outcome.status}
    </StatusPill>
  );
}

/** The calls made with one key, newest first, a page at a time. */
function CallLog({ workspace, gatewayKey }: { workspace: string; gatewayKey: GatewayKey }) {
  const { t, format } = useI18n();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const calls = useInfiniteQuery({
    queryKey: [...gatewayKeys.calls(workspace, gatewayKey.id), limit],
    queryFn: ({ pageParam, signal }) =>
      listGatewayKeyCalls(
        workspace,
        gatewayKey.id,
        { limit, cursor: pageParam ?? undefined },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = calls.data?.pages.flatMap((page) => page.items) ?? [];
  let body;
  if (calls.isPending) {
    body = <Waiting />;
  } else if (calls.error && items.length === 0) {
    body = isProblem(calls.error) ? (
      <ProblemNotice
        problem={calls.error}
        onRetry={() => {
          void calls.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = <p className="m-0 text-text-2">{t('gateway.calls.empty')}</p>;
  } else {
    body = (
      <>
        <div className="overflow-x-auto">
          <Table>
            <caption className="sr-only">{t('gateway.calls.title')}</caption>
            <thead>
              <tr>
                <Th>{t('gateway.calls.time')}</Th>
                <Th>{t('gateway.calls.outcome')}</Th>
                <Th>{t('gateway.calls.tokens')}</Th>
                <Th>{t('gateway.calls.elapsed')}</Th>
              </tr>
            </thead>
            <tbody>
              {items.map((call) => (
                <Tr key={call.id}>
                  <Td className="whitespace-nowrap">{format.dateTime(call.admitted_at)}</Td>
                  <Td>
                    <Outcome call={call} />
                  </Td>
                  <Td className="tabular-nums">
                    {call.outcome?.usage ? format.number(call.outcome.usage.total_tokens) : '—'}
                  </Td>
                  <Td className="whitespace-nowrap tabular-nums">
                    {call.outcome ? format.duration(call.outcome.elapsed_milliseconds / 1000) : '—'}
                  </Td>
                </Tr>
              ))}
            </tbody>
          </Table>
        </div>
        {calls.error && isProblem(calls.error) ? (
          <ProblemNotice
            problem={calls.error}
            onRetry={() => {
              void calls.fetchNextPage();
            }}
          />
        ) : null}
        {calls.hasNextPage ? (
          <Button
            className="self-start"
            disabled={calls.isFetchingNextPage}
            onClick={() => {
              void calls.fetchNextPage();
            }}
          >
            {t('gateway.calls.more')}
          </Button>
        ) : null}
      </>
    );
  }
  return (
    <section aria-labelledby="key-calls" className="flex flex-col gap-3">
      <h3 id="key-calls" className="m-0 text-[14px] font-semibold">
        {t('gateway.calls.title')}
      </h3>
      {body}
    </section>
  );
}

function KeyDrawer({
  workspace,
  gatewayKey,
  owner,
  byId,
  onClose,
  onAct,
}: {
  workspace: string;
  gatewayKey: GatewayKey | null;
  owner: (id: string) => string;
  byId: (id: string) => GatewayKey | undefined;
  onClose: () => void;
  onAct: (dialog: Dialog) => void;
}) {
  const { t, format } = useI18n();
  const replaced = gatewayKey?.replaced_by ? byId(gatewayKey.replaced_by) : undefined;
  const previous = gatewayKey?.rotated_from ? byId(gatewayKey.rotated_from) : undefined;
  return (
    <Drawer open={gatewayKey !== null} onClose={onClose} title={gatewayKey?.label ?? ''}>
      {gatewayKey ? (
        <div className="flex flex-col gap-5">
          <div className="flex flex-wrap gap-2">
            {canRotate(gatewayKey) ? (
              <Button
                onClick={() => {
                  onAct({ kind: 'rotate', key: gatewayKey });
                }}
              >
                <RefreshCw aria-hidden />
                {t('gateway.rotate.open')}
              </Button>
            ) : null}
            {canRevoke(gatewayKey) ? (
              <Button
                variant="danger"
                onClick={() => {
                  onAct({ kind: 'revoke', key: gatewayKey });
                }}
              >
                <Trash2 aria-hidden />
                {t('gateway.revoke.open')}
              </Button>
            ) : null}
          </div>
          <DetailList>
            <Detail label={t('gateway.column.state')}>
              <KeyState gatewayKey={gatewayKey} />
            </Detail>
            <Detail label={t('gateway.column.prefix')}>
              <span className="font-mono text-[12.5px]">{gatewayKey.prefix}</span>
            </Detail>
            <Detail label={t('gateway.column.scope')}>
              {t('gateway.scope', { model: gatewayKey.alias_name })}
            </Detail>
            <Detail label={t('gateway.column.owner')}>{owner(gatewayKey.created_by)}</Detail>
            <Detail label={t('gateway.detail.created')}>
              {format.dateTime(gatewayKey.created_at)}
            </Detail>
            <Detail label={t('gateway.column.expiry')}>
              {format.dateTime(gatewayKey.expires_at)}
            </Detail>
            <Detail label={t('gateway.column.lastUsed')}>
              {gatewayKey.last_used_at
                ? format.dateTime(gatewayKey.last_used_at)
                : t('gateway.neverUsed')}
            </Detail>
            {gatewayKey.rotated_from ? (
              <Detail label={t('gateway.detail.rotatedFrom')}>
                {previous?.label ?? shortId(gatewayKey.rotated_from)}
              </Detail>
            ) : null}
            {gatewayKey.replaced_by ? (
              <Detail label={t('gateway.detail.replacedBy')}>
                {replaced?.label ?? shortId(gatewayKey.replaced_by)}
              </Detail>
            ) : null}
          </DetailList>
          {gatewayKey.state === 'invalidated' ? (
            <Notice className="m-0" title={t('gateway.detail.invalidated')} />
          ) : null}
          <CallLog workspace={workspace} gatewayKey={gatewayKey} />
        </div>
      ) : null}
    </Drawer>
  );
}

function Keys({
  workspace,
  issuing,
  onIssue,
}: {
  workspace: string;
  issuing: boolean;
  onIssue: (open: boolean) => void;
}) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const list = useListView(definition);
  const tile: KeyTile = readyTiles.find((value) => value === list.state.filters.tile) ?? 'all';
  const term = list.state.filters.name?.toLowerCase() ?? '';
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const [dialog, setDialog] = useState<Dialog>(null);
  const keys = useQuery({
    queryKey: gatewayKeys.list(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listGatewayKeys(workspace, { limit: pageSize, cursor }, { signal, background: true }),
      ),
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  const all = useMemo(() => keys.data?.items ?? [], [keys.data]);
  const owner = useOwnerNames(all);
  // The time of the answer, so the tiles and the rows agree on what "soon" means.
  const now = keys.dataUpdatedAt;
  const items = all.filter(
    (key) =>
      inTile(key, tile, now) &&
      (term === '' ||
        key.label.toLowerCase().includes(term) ||
        key.prefix.toLowerCase().includes(term) ||
        key.alias_name.includes(term)),
  );
  const filtered = Object.keys(list.state.filters).length > 0;
  let body;
  if (keys.isPending) {
    body = <Waiting />;
  } else if (!keys.data) {
    body = isProblem(keys.error) ? (
      <ProblemNotice
        problem={keys.error}
        onRetry={() => {
          void keys.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={KeyRound}
          title={filtered ? t('gateway.emptyFilter.title') : t('gateway.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('gateway.emptyFilter.clear')}</Button>
            ) : (
              <IssueButton
                onClick={() => {
                  onIssue(true);
                }}
              />
            )
          }
        >
          {filtered ? t('gateway.emptyFilter.detail') : t('gateway.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = <Rows items={items} list={list} owner={owner} />;
  }
  const opened = all.find((key) => key.id === list.openRow) ?? null;
  const count = (value: KeyTile) => all.filter((key) => inTile(key, value, now)).length;
  return (
    <>
      <FilterTiles
        label={t('gateway.filter')}
        value={tile}
        tiles={keyTiles.map((value) => ({
          value,
          label: t(`gateway.tile.${value}`),
          count: keys.data && value !== 'overLimit' ? count(value) : undefined,
          attention: value === 'expiring',
          unavailable: value === 'overLimit' ? t('gateway.tile.overLimit.notReady') : undefined,
        }))}
        onChange={(value) => {
          // A second press on the chosen tile goes back to everything.
          list.setFilter('tile', value === 'all' || value === tile ? '' : value);
        }}
      />
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('gateway.search')}
          placeholder={t('gateway.search.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {keys.data && !keys.data.complete ? (
        <Notice className="mb-3.5" title={t('gateway.partial', { count: all.length })} />
      ) : null}
      {body}
      <KeyDrawer
        workspace={workspace}
        gatewayKey={opened}
        owner={owner}
        byId={(id) => all.find((key) => key.id === id)}
        onClose={() => {
          list.setOpenRow(null);
        }}
        onAct={setDialog}
      />
      {issuing ? (
        <IssueKeyDialog
          workspace={workspace}
          onClose={() => {
            onIssue(false);
          }}
        />
      ) : null}
      {dialog?.kind === 'rotate' ? (
        <RotateKeyDialog
          workspace={workspace}
          gatewayKey={dialog.key}
          onClose={() => {
            setDialog(null);
          }}
        />
      ) : null}
      {dialog?.kind === 'revoke' ? (
        <RevokeKeyDialog
          workspace={workspace}
          gatewayKey={dialog.key}
          onClose={() => {
            setDialog(null);
          }}
        />
      ) : null}
    </>
  );
}

function IssueButton({ onClick }: { onClick: () => void }) {
  const { t } = useI18n();
  return (
    <Button variant="primary" onClick={onClick}>
      <Plus aria-hidden />
      {t('gateway.issue.open')}
    </Button>
  );
}

/** `/gateway`: the keys of the workspace in the switcher. `#issue` opens the issue dialog. */
export function GatewayPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const workspace = workspaces.active?.id;
  const [issuing, setIssuing] = useState(useHashAction('issue'));
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('gateway.title')}
          </h1>
          <p className="m-0 text-text-2">
            {workspaces.active
              ? t('gateway.subtitle', { workspace: workspaces.active.name })
              : t('gateway.subtitle.none')}
          </p>
        </div>
        <IssueButton
          onClick={() => {
            setIssuing(true);
          }}
        />
      </div>
      {workspace ? (
        <Keys workspace={workspace} issuing={issuing} onIssue={setIssuing} />
      ) : (
        <Waiting />
      )}
    </>
  );
}
