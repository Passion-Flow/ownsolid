// The keys that call a workspace's models through the gateway: what each may call and when it stops,
// filtered by the tiles above them, with how to use it and its calls in its drawer. Both entrances
// show this list; the session's audience decides which keys the server returns
// (docs/research/gateway-keys.md, docs/research/portal-api-keys.md).
import {
  downloadGatewayKeyCalls,
  isProblem,
  listGatewayKeyCalls,
  listGatewayKeys,
  type GatewayCall,
  type GatewayKey,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
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
  saveFile,
  SearchField,
  SelectField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery } from '@tanstack/react-query';
import { Download, KeyRound, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { useId, useMemo, useState, type ReactNode } from 'react';
import { everyPage, pageSize } from '../every-page.ts';
import { ColumnChooser } from '../list/column-chooser.tsx';
import { CopyLinkButton } from '../list/copy-link-button.tsx';
import { SavedViewsMenu } from '../list/saved-views-menu.tsx';
import type { ListDefinition } from '../list/list-state.ts';
import { useListView, type ListView } from '../list/use-list-view.ts';
import { useTypedFilter } from '../list/use-typed-filter.ts';
import { runtimeConfigQuery } from '../routes.tsx';
import { useAudience } from '../session.ts';
import { IssueKeyDialog, RevokeKeyDialog, RotateKeyDialog } from './key-dialogs.tsx';
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
} from './key-state.ts';
import { KeyUsage } from './key-usage-panel.tsx';

const filters: ListDefinition['filters'] = {
  tile: { kind: 'choice', values: readyTiles.filter((tile) => tile !== 'all') },
  name: { kind: 'text', maximum: 100 },
};

const columns = (owner: boolean): ListDefinition['columns'] => [
  { id: 'name', label: 'gateway.column.name', locked: true },
  { id: 'prefix', label: 'gateway.column.prefix' },
  { id: 'scope', label: 'gateway.column.scope' },
  ...(owner ? [{ id: 'owner', label: 'gateway.column.owner' } as const] : []),
  { id: 'lastUsed', label: 'gateway.column.lastUsed' },
  { id: 'expiry', label: 'gateway.column.expiry' },
  { id: 'state', label: 'gateway.column.state', locked: true },
];

// One list for each entrance: a person's saved views hold no owner column.
const everyonesKeys: ListDefinition = { key: 'gateway-keys', filters, columns: columns(true) };
const myKeys: ListDefinition = { key: 'my-keys', filters, columns: columns(false) };

type Dialog = { kind: 'rotate' | 'revoke'; key: GatewayKey } | null;

/** Shows who holds a key. Only the console has one to show: in the portal every key is the person's. */
export type OwnerName = (id: string) => ReactNode;

/** The first block of an identifier, for a person whose name could not be read. */
function shortId(id: string): string {
  return id.slice(0, 8);
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
  owner: OwnerName | undefined;
}) {
  const { t, format } = useI18n();
  const shows = (column: string) => list.isColumnVisible(column) && (column !== 'owner' || !!owner);
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
              {shows('owner') && owner ? <Td>{owner(key.created_by)}</Td> : null}
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
  const audience = useAudience();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const calls = useInfiniteQuery({
    queryKey: [...gatewayKeys.calls(workspace, gatewayKey.id), limit],
    queryFn: ({ pageParam, signal }) =>
      listGatewayKeyCalls(
        audience,
        workspace,
        gatewayKey.id,
        { limit, cursor: pageParam ?? undefined },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const csvScope = useId();
  const [csvPage, setCsvPage] = useState(0);
  const pages = calls.data?.pages ?? [];
  const selectedPage = csvPage < pages.length ? csvPage : 0;
  const csv = useMutation({
    mutationFn: (cursor: string | null) =>
      downloadGatewayKeyCalls(audience, workspace, gatewayKey.id, limit, cursor),
  });
  const startCsv = (cursor: string | null) => {
    if (csv.isPending) return;
    csv.mutate(cursor, {
      onSuccess: ({ content, name }) => {
        saveFile(content, name);
      },
    });
  };
  const items = pages.flatMap((page) => page.items);
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
      <div className="flex flex-wrap items-end gap-2">
        {pages.length > 1 ? (
          <SelectField
            className="min-w-28"
            label={t('gateway.calls.csvPage')}
            value={String(selectedPage)}
            disabled={csv.isPending}
            options={pages.map((_, index) => ({
              value: String(index),
              label: t('gateway.calls.csvPageNumber', { page: format.number(index + 1) }),
            }))}
            onChange={(event) => {
              setCsvPage(Number(event.currentTarget.value));
              csv.reset();
            }}
          />
        ) : null}
        <Button
          aria-disabled={csv.isPending || pages.length === 0 || undefined}
          aria-describedby={csvScope}
          onClick={() => {
            if (pages.length === 0) return;
            if (selectedPage === 0) {
              startCsv(null);
              return;
            }
            const cursor = pages[selectedPage - 1]?.next_cursor;
            if (cursor) startCsv(cursor);
          }}
        >
          <Download aria-hidden />
          {t(csv.isPending ? 'gateway.download.pending' : 'gateway.calls.csv')}
        </Button>
      </div>
      <p id={csvScope} className="m-0 text-[13px] text-text-2">
        {t(pages.length ? 'gateway.calls.csvScope' : 'gateway.calls.csvWaiting')}
      </p>
      {isProblem(csv.error) ? (
        <ProblemNotice
          problem={csv.error}
          onRetry={() => {
            if (csv.variables !== undefined) startCsv(csv.variables);
          }}
        />
      ) : null}
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
  owner: OwnerName | undefined;
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
            {owner ? (
              <Detail label={t('gateway.column.owner')}>{owner(gatewayKey.created_by)}</Detail>
            ) : null}
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
          <KeyUsage key={gatewayKey.id} workspace={workspace} gatewayKey={gatewayKey} />
          <CallLog key={gatewayKey.id} workspace={workspace} gatewayKey={gatewayKey} />
        </div>
      ) : null}
    </Drawer>
  );
}

/** Props for {@link GatewayKeys}. */
export interface GatewayKeysProps {
  workspace: string;
  /** Whether the issue dialog is open. */
  issuing: boolean;
  onIssue: (open: boolean) => void;
  /** Names a key's owner; left out where every key is the person's own. */
  owner?: OwnerName | undefined;
  /** What to do when no model can take a key. */
  noModel: ReactNode;
}

/** A workspace's keys, with their tiles, search, drawer and dialogs. */
export function GatewayKeys({ workspace, issuing, onIssue, owner, noModel }: GatewayKeysProps) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const audience = useAudience();
  const list = useListView(owner ? everyonesKeys : myKeys);
  const tile: KeyTile = readyTiles.find((value) => value === list.state.filters.tile) ?? 'all';
  const term = list.state.filters.name?.toLowerCase() ?? '';
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const [dialog, setDialog] = useState<Dialog>(null);
  const keys = useQuery({
    queryKey: gatewayKeys.list(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listGatewayKeys(
          audience,
          workspace,
          { limit: pageSize, cursor },
          { signal, background: true },
        ),
      ),
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  const all = useMemo(() => keys.data?.items ?? [], [keys.data]);
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
              <IssueKeyButton
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
          noModel={noModel}
          onIssued={(key) => {
            list.setOpenRow(key.id);
          }}
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

/** The button that opens the issue dialog. */
export function IssueKeyButton({ onClick }: { onClick: () => void }) {
  const { t } = useI18n();
  return (
    <Button variant="primary" onClick={onClick}>
      <Plus aria-hidden />
      {t('gateway.issue.open')}
    </Button>
  );
}
