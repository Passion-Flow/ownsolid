// The models a person may call in the workspace chosen in the switcher: whether each can be called
// now, what is behind its name, and how to start using it (S-304,
// docs/research/portal-model-catalogue.md). Disabled names are left out by model-catalogue.ts.
import {
  isProblem,
  listModelAliases,
  listWorkspaceDeployments,
  listWorkspaceModels,
  type ModelVersion,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  everyPage,
  GatewayCADownload,
  gatewayBaseUrl,
  pageSize,
  runtimeConfigQuery,
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
  buttonVariants,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Cpu, FlaskConical, KeyRound } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import {
  catalogueEntries,
  catalogueTiles,
  contextTokens,
  inCatalogueTile,
  matchesCatalogueTerm,
  sourceRepository,
  type CatalogueEntry,
  type CatalogueTile,
} from './model-catalogue.ts';
import { WorkspaceBody } from './workspace-body.tsx';

const definition: ListDefinition = {
  key: 'portal-models',
  filters: {
    tile: { kind: 'choice', values: catalogueTiles.filter((tile) => tile !== 'all') },
    name: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'name', label: 'portalModels.column.name', locked: true },
    { id: 'model', label: 'portalModels.column.model' },
    { id: 'origin', label: 'portalModels.column.origin' },
    { id: 'context', label: 'portalModels.column.context' },
    { id: 'state', label: 'portalModels.column.state', locked: true },
  ],
};

function EntryState({ entry }: { entry: CatalogueEntry }) {
  const { t } = useI18n();
  return entry.alias.available ? (
    <StatusPill tone="good">{t('portalModels.state.callable')}</StatusPill>
  ) : (
    <StatusPill tone="neutral">{t('portalModels.state.unavailable')}</StatusPill>
  );
}

function originLabel(model: ModelVersion): TranslationKey {
  return model.kind === 'merged_lora'
    ? 'portalModels.origin.fineTuned'
    : 'portalModels.origin.published';
}

/** One fact about the model behind a name: a placeholder while it loads, then what is known. */
function Fact({
  entry,
  pending,
  children,
}: {
  entry: CatalogueEntry;
  pending: boolean;
  children: (model: ModelVersion) => ReactNode;
}) {
  const { t } = useI18n();
  if (entry.model) return children(entry.model);
  return pending ? <Skeleton className="h-3.5 w-20" /> : t('portalModels.notKnown');
}

function Rows({
  items,
  list,
  pending,
}: {
  items: readonly CatalogueEntry[];
  list: ListView;
  pending: boolean;
}) {
  const { t, format } = useI18n();
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('portalModels.title')}</caption>
        <thead>
          <tr>
            <Th>{t('portalModels.column.name')}</Th>
            {list.isColumnVisible('model') ? <Th>{t('portalModels.column.model')}</Th> : null}
            {list.isColumnVisible('origin') ? <Th>{t('portalModels.column.origin')}</Th> : null}
            {list.isColumnVisible('context') ? <Th>{t('portalModels.column.context')}</Th> : null}
            <Th>{t('portalModels.column.state')}</Th>
          </tr>
        </thead>
        <tbody>
          {items.map((entry) => (
            <Tr key={entry.alias.id}>
              <Td>
                <button
                  type="button"
                  aria-label={t('portalModels.detail.open', { name: entry.alias.name })}
                  className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-mono text-[13px] font-semibold text-text hover:underline"
                  onClick={() => {
                    list.setOpenRow(entry.alias.id);
                  }}
                >
                  {entry.alias.name}
                </button>
              </Td>
              {list.isColumnVisible('model') ? (
                <Td>
                  <Fact entry={entry} pending={pending}>
                    {(model) => model.name}
                  </Fact>
                </Td>
              ) : null}
              {list.isColumnVisible('origin') ? (
                <Td>
                  <Fact entry={entry} pending={pending}>
                    {(model) => t(originLabel(model))}
                  </Fact>
                </Td>
              ) : null}
              {list.isColumnVisible('context') ? (
                <Td className="whitespace-nowrap tabular-nums">
                  <Fact entry={entry} pending={pending}>
                    {(model) =>
                      t('portalModels.tokens', { count: format.number(contextTokens(model)) })
                    }
                  </Fact>
                </Td>
              ) : null}
              <Td>
                <EntryState entry={entry} />
              </Td>
            </Tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function IssueKeyLink() {
  const { t } = useI18n();
  return (
    <Link to="/keys" hash="issue" className={buttonVariants({ variant: 'primary' })}>
      <KeyRound aria-hidden />
      {t('portalModels.issue')}
    </Link>
  );
}

function EntryDrawer({
  entry,
  workspace,
  pending,
  onClose,
}: {
  entry: CatalogueEntry | null;
  workspace: string;
  pending: boolean;
  onClose: () => void;
}) {
  const { t, format } = useI18n();
  const description = entry?.alias.description;
  return (
    <Drawer open={entry !== null} onClose={onClose} title={entry?.alias.name ?? ''}>
      {entry ? (
        <div className="flex flex-col gap-5">
          <div className="flex flex-wrap gap-2">
            <IssueKeyLink />
          </div>
          {entry.alias.available ? null : (
            <Notice className="m-0" title={t('portalModels.unavailable.title')}>
              {t('portalModels.unavailable.detail')}
            </Notice>
          )}
          <div className="flex flex-col gap-3">
            <DetailList className="grid-cols-[96px_1fr] sm:grid-cols-[130px_1fr]">
              <Detail label={t('portalModels.column.state')}>
                <EntryState entry={entry} />
              </Detail>
              <Detail label={t('portalModels.detail.callName')}>
                <span className="font-mono text-[12.5px]">{entry.alias.name}</span>
              </Detail>
              <Detail label={t('portalModels.detail.baseUrl')}>
                <span className="font-mono text-[12.5px] [overflow-wrap:anywhere]">
                  {gatewayBaseUrl(entry.alias.client_origin)}
                </span>
              </Detail>
            </DetailList>
            <GatewayCADownload
              key={entry.alias.id}
              workspace={workspace}
              aliasId={entry.alias.id}
            />
          </div>
          <DetailList className="grid-cols-[96px_1fr] sm:grid-cols-[130px_1fr]">
            <Detail label={t('portalModels.column.model')}>
              <Fact entry={entry} pending={pending}>
                {(model) => model.name}
              </Fact>
            </Detail>
            <Detail label={t('portalModels.detail.source')}>
              <Fact entry={entry} pending={pending}>
                {sourceRepository}
              </Fact>
            </Detail>
            <Detail label={t('portalModels.column.origin')}>
              <Fact entry={entry} pending={pending}>
                {(model) => t(originLabel(model))}
              </Fact>
            </Detail>
            <Detail label={t('portalModels.detail.goodFor')}>
              <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">
                {description?.length ? description : t('portalModels.detail.goodFor.chat')}
              </span>
              {description === '' ? (
                <span className="mt-1 block text-text-3">
                  {t('portalModels.detail.goodFor.empty')}
                </span>
              ) : null}
            </Detail>
            <Detail label={t('portalModels.column.context')}>
              <Fact entry={entry} pending={pending}>
                {(model) =>
                  t('portalModels.tokens', { count: format.number(contextTokens(model)) })
                }
              </Fact>
            </Detail>
            <Detail label={t('portalModels.detail.license')}>
              <Fact entry={entry} pending={pending}>
                {(model) => model.manifest.license}
              </Fact>
            </Detail>
          </DetailList>
        </div>
      ) : null}
    </Drawer>
  );
}

function Waiting() {
  return (
    <div className="flex flex-col gap-3" aria-busy>
      <Skeleton className="h-11 w-full" />
      <Skeleton className="h-11 w-full" />
    </div>
  );
}

function Catalogue({ workspace }: { workspace: string }) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const list = useListView(definition);
  const tile: CatalogueTile =
    catalogueTiles.find((value) => value === list.state.filters.tile) ?? 'all';
  const term = list.state.filters.name?.toLowerCase() ?? '';
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const aliases = useQuery({
    queryKey: ['portal-models', 'aliases', workspace],
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listModelAliases(
          'portal',
          workspace,
          { limit: pageSize, cursor },
          { signal, background: true },
        ),
      ),
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  const deployments = useQuery({
    queryKey: ['portal-models', 'deployments', workspace],
    queryFn: ({ signal }) => listWorkspaceDeployments(workspace, { signal }),
  });
  const models = useQuery({
    queryKey: ['portal-models', 'models', workspace],
    queryFn: ({ signal }) => listWorkspaceModels(workspace, { signal }),
  });
  const aliasProblem = isProblem(aliases.error) ? aliases.error : null;
  const denied = aliasProblem?.status === 403 || aliasProblem?.status === 404;
  const all = useMemo(
    () =>
      catalogueEntries(
        denied ? [] : (aliases.data?.items ?? []),
        deployments.data ?? [],
        models.data ?? [],
      ),
    [aliases.data, deployments.data, models.data, denied],
  );
  const items = all.filter(
    (entry) => inCatalogueTile(entry, tile) && matchesCatalogueTerm(entry, term),
  );
  const filtered = Object.keys(list.state.filters).length > 0;
  const factsPending = deployments.isPending || models.isPending;
  const factsProblem = [deployments.error, models.error].find((error) => isProblem(error));
  let body;
  if (aliases.isPending) {
    body = <Waiting />;
  } else if (denied || !aliases.data) {
    body = isProblem(aliases.error) ? (
      <ProblemNotice
        problem={aliases.error}
        onRetry={() => {
          void aliases.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={Cpu}
          title={filtered ? t('portalModels.emptyFilter.title') : t('portalModels.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('portalModels.emptyFilter.clear')}</Button>
            ) : undefined
          }
        >
          {filtered ? t('portalModels.emptyFilter.detail') : t('portalModels.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = <Rows items={items} list={list} pending={factsPending} />;
  }
  const opened = all.find((entry) => entry.alias.id === list.openRow) ?? null;
  return (
    <>
      <FilterTiles
        label={t('portalModels.filter')}
        value={tile}
        tiles={catalogueTiles.map((value) => ({
          value,
          label: t(`portalModels.tile.${value}`),
          count:
            aliases.data && !denied
              ? all.filter((entry) => inCatalogueTile(entry, value)).length
              : undefined,
        }))}
        onChange={(value) => {
          // A second press on the chosen tile goes back to everything.
          list.setFilter('tile', value === 'all' || value === tile ? '' : value);
        }}
      />
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('portalModels.search')}
          placeholder={t('portalModels.search.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {aliases.data && !denied && !aliases.data.complete ? (
        <Notice className="mb-3.5" title={t('portalModels.partial', { count: all.length })} />
      ) : null}
      {aliasProblem && aliases.data && !denied ? (
        <ProblemNotice
          className="mb-3.5"
          problem={aliasProblem}
          title={t('portalModels.refreshUnavailable')}
          onRetry={() => {
            void aliases.refetch();
          }}
        />
      ) : null}
      {factsProblem && aliases.data && !denied ? (
        <div className="mb-3.5">
          <ProblemNotice
            problem={factsProblem}
            title={t('portalModels.factsUnavailable.title')}
            detail={factsProblem.retryable ? t('portalModels.factsUnavailable.detail') : undefined}
            onRetry={() => {
              void deployments.refetch();
              void models.refetch();
            }}
          />
        </div>
      ) : null}
      {body}
      <EntryDrawer
        entry={opened}
        workspace={workspace}
        pending={factsPending}
        onClose={() => {
          list.setOpenRow(null);
        }}
      />
    </>
  );
}

/** `/models`: what the person may call in the workspace chosen in the switcher. */
export function ModelsPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const active = workspaces.active;
  const issueUnavailable = workspaces.isPending
    ? t('status.loading')
    : workspaces.error
      ? t('workspace.loadFailed')
      : t('keys.noWorkspace.detail');
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('portalModels.title')}
          </h1>
          <p className="m-0 text-text-2">
            {active
              ? t('portalModels.subtitle', { workspace: active.name })
              : t('portalModels.subtitle.none')}
          </p>
        </div>
        <div className="flex flex-col items-start gap-1 sm:items-end">
          <div className="flex flex-wrap gap-2">
            <Button aria-disabled aria-describedby="portal-models-try-not-ready">
              <FlaskConical aria-hidden />
              {t('portalModels.try')}
            </Button>
            {active ? (
              <IssueKeyLink />
            ) : (
              <Button
                variant="primary"
                aria-disabled
                aria-describedby="portal-models-issue-unavailable"
              >
                <KeyRound aria-hidden />
                {t('portalModels.issue')}
              </Button>
            )}
          </div>
          <p id="portal-models-try-not-ready" className="m-0 text-[12.5px] text-text-3">
            {t('portalModels.try.notReady')}
          </p>
          {!active ? (
            <p id="portal-models-issue-unavailable" className="m-0 text-[12.5px] text-text-3">
              {issueUnavailable}
            </p>
          ) : null}
        </div>
      </div>
      <WorkspaceBody workspaces={workspaces} noWorkspace={t('portalModels.noWorkspace.detail')}>
        {(workspace) => <Catalogue key={workspace} workspace={workspace} />}
      </WorkspaceBody>
    </>
  );
}
