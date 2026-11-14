// The models a workspace serves: how each is doing, filtered by the tiles above them, and a way to
// deploy another (docs/research/model-services.md).
import { isProblem, listDeployments, type Deployment } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  runtimeConfigQuery,
  SavedViewsMenu,
  useListView,
  useTypedFilter,
  useWorkspaces,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  Drawer,
  EmptyState,
  FilterTiles,
  ProblemNotice,
  SearchField,
  Skeleton,
  Table,
  Td,
  Th,
  Tr,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ArrowRight, ServerOff } from 'lucide-react';
import { useMemo } from 'react';
import {
  DeployLink,
  DeploymentStatePill,
  recipeEngine,
  ReplicaFacts,
  useDeploymentNames,
  type DeploymentName,
} from './deployment-details.tsx';
import { deploymentKeys, deploymentStatus, phases, type Phase } from './deployment-state.ts';

type Tile = 'all' | Phase;

const tiles: readonly Tile[] = ['all', ...phases];

const definition: ListDefinition = {
  key: 'deployments',
  filters: {
    phase: { kind: 'choice', values: phases },
    name: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'deployment', label: 'services.column.deployment', locked: true },
    { id: 'engine', label: 'services.column.engine' },
    { id: 'replicas', label: 'services.column.replicas' },
    { id: 'requests', label: 'services.column.requests' },
    { id: 'latency', label: 'services.column.latency' },
    { id: 'state', label: 'services.column.state', locked: true },
  ],
};

function Waiting() {
  return (
    <div className="flex flex-col gap-3" aria-busy>
      <Skeleton className="h-11 w-full" />
      <Skeleton className="h-11 w-full" />
    </div>
  );
}

function Rows({
  items,
  list,
  name,
}: {
  items: readonly Deployment[];
  list: ListView;
  name: (deployment: Deployment) => DeploymentName;
}) {
  const { t } = useI18n();
  const shows = list.isColumnVisible;
  const notCollected = t('services.metrics.notCollected');
  return (
    <>
      <Card className="overflow-x-auto">
        <Table>
          <caption className="sr-only">{t('services.title')}</caption>
          <thead>
            <tr>
              <Th>{t('services.column.deployment')}</Th>
              {shows('engine') ? <Th>{t('services.column.engine')}</Th> : null}
              {shows('replicas') ? <Th>{t('services.column.replicas')}</Th> : null}
              {shows('requests') ? <Th>{t('services.column.requests')}</Th> : null}
              {shows('latency') ? <Th>{t('services.column.latency')}</Th> : null}
              <Th>{t('services.column.state')}</Th>
            </tr>
          </thead>
          <tbody>
            {items.map((deployment) => {
              const named = name(deployment);
              const ready = deploymentStatus(deployment).phase === 'healthy' ? 1 : 0;
              return (
                <Tr key={deployment.id}>
                  <Td>
                    <button
                      type="button"
                      aria-label={t('services.detail.open', { name: named.title })}
                      className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                      onClick={() => {
                        list.setOpenRow(deployment.id);
                      }}
                    >
                      {named.model}
                    </button>
                    <div className="text-[12.5px] text-text-2">
                      {t('services.on', { node: named.node })}
                    </div>
                  </Td>
                  {shows('engine') ? <Td>{t(recipeEngine)}</Td> : null}
                  {shows('replicas') ? (
                    <Td className="whitespace-nowrap">
                      {t('services.replicas.count', { ready, total: 1 })}
                    </Td>
                  ) : null}
                  {shows('requests') ? (
                    <Td className="text-text-3">
                      <span aria-label={notCollected}>—</span>
                    </Td>
                  ) : null}
                  {shows('latency') ? (
                    <Td className="text-text-3">
                      <span aria-label={notCollected}>—</span>
                    </Td>
                  ) : null}
                  <Td>
                    <DeploymentStatePill deployment={deployment} />
                  </Td>
                </Tr>
              );
            })}
          </tbody>
        </Table>
      </Card>
      {shows('requests') || shows('latency') ? (
        <p className="mt-2 text-[12.5px] text-text-3">{t('services.metrics.notReady')}</p>
      ) : null}
    </>
  );
}

function ServiceDrawer({
  deployment,
  name,
  onClose,
}: {
  deployment: Deployment | null;
  name: DeploymentName | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <Drawer open={deployment !== null} onClose={onClose} title={name?.title ?? ''}>
      {deployment && name ? (
        <div className="flex flex-col gap-4">
          <Link
            to="/services/$deploymentId"
            params={{ deploymentId: deployment.id }}
            className="inline-flex items-center gap-1.5 font-medium text-accent-ink hover:underline"
          >
            {t('resource.openPage')}
            <ArrowRight aria-hidden className="size-4" />
          </Link>
          <h3 className="m-0 text-[14px] font-semibold">{t('services.replicas.title')}</h3>
          <ReplicaFacts deployment={deployment} name={name} />
        </div>
      ) : null}
    </Drawer>
  );
}

function Deployments({ workspace }: { workspace: string }) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const list = useListView(definition);
  const { phase, name: term } = list.state.filters;
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const deployments = useQuery({
    queryKey: deploymentKeys.list(workspace),
    queryFn: ({ signal }) => listDeployments(workspace, {}, { signal, background: true }),
    refetchInterval: config?.pollMilliseconds ?? false,
  });
  const all = useMemo(() => deployments.data ?? [], [deployments.data]);
  const name = useDeploymentNames(workspace, all);
  const count = (tile: Tile) =>
    tile === 'all' ? all.length : all.filter((d) => deploymentStatus(d).phase === tile).length;
  // A workspace's deployments come whole (one per node), so filtering here sees every one.
  const items = all.filter((deployment) => {
    const lower = term?.toLowerCase() ?? '';
    const named = name(deployment);
    return (
      (phase === undefined || deploymentStatus(deployment).phase === phase) &&
      (lower === '' ||
        named.model.toLowerCase().includes(lower) ||
        named.node.toLowerCase().includes(lower))
    );
  });
  const filtered = Object.keys(list.state.filters).length > 0;
  let body;
  if (deployments.isPending) {
    body = <Waiting />;
  } else if (!deployments.data) {
    body = isProblem(deployments.error) ? (
      <ProblemNotice
        problem={deployments.error}
        onRetry={() => {
          void deployments.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={ServerOff}
          title={filtered ? t('services.emptyFilter.title') : t('services.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('services.emptyFilter.clear')}</Button>
            ) : (
              <DeployLink />
            )
          }
        >
          {filtered ? t('services.emptyFilter.detail') : t('services.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = <Rows items={items} list={list} name={name} />;
  }
  const opened = all.find((deployment) => deployment.id === list.openRow) ?? null;
  return (
    <>
      <FilterTiles
        label={t('services.filter')}
        value={phase ?? 'all'}
        tiles={tiles.map((value) => ({
          value,
          label: t(`services.tile.${value}`),
          count: deployments.data ? count(value) : undefined,
          attention: value === 'degraded',
        }))}
        onChange={(value) => {
          // A second press on the chosen tile goes back to everything.
          list.setFilter('phase', value === 'all' || value === phase ? '' : value);
        }}
      />
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('services.search')}
          placeholder={t('services.search.placeholder')}
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
      {body}
      <ServiceDrawer
        deployment={opened}
        name={opened ? name(opened) : null}
        onClose={() => {
          list.setOpenRow(null);
        }}
      />
    </>
  );
}

/** `/services`: what the workspace in the switcher serves. */
export function ServicesPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const workspace = workspaces.active?.id;
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('services.title')}
          </h1>
          <p className="m-0 text-text-2">
            {workspaces.active
              ? t('services.subtitle', { workspace: workspaces.active.name })
              : t('services.subtitle.none')}
          </p>
        </div>
        <DeployLink />
      </div>
      {workspace ? <Deployments workspace={workspace} /> : <Waiting />}
    </>
  );
}
