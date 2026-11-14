// Single sign-on connectors: which providers people can sign in with, how to add one, and a check
// that tells which part of a connector is wrong (docs/research/sso-page.md).
import {
  getSsoConfiguration,
  isProblem,
  listManagedConnectors,
  type ManagedConnector,
  type SsoConfiguration,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  runtimeConfigQuery,
  useHashAction,
  useListView,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  Drawer,
  EmptyState,
  Notice,
  ProblemNotice,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
  useToast,
} from '@ownsolid/ui';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ArrowRight, KeyRound, Plus } from 'lucide-react';
import { useState } from 'react';
import { AddConnectorDialog, connectorsKey } from './sso-dialogs.tsx';
import { configurationKey, stateOf, states, useConnectorSurface } from './sso-surface.tsx';

const definition: ListDefinition = {
  key: 'sso',
  filters: {},
  columns: [
    { id: 'name', label: 'sso.column.name', locked: true },
    { id: 'status', label: 'sso.column.status' },
    { id: 'origin', label: 'sso.column.origin' },
    { id: 'client', label: 'sso.column.clientId' },
    { id: 'updated', label: 'sso.column.updated' },
  ],
};

function ConnectorDrawer({
  connector,
  configuration,
  missing,
  onClose,
}: {
  connector: ManagedConnector | null;
  configuration: SsoConfiguration | undefined;
  missing: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const surface = useConnectorSurface(connector, configuration);
  return (
    <>
      <Drawer
        open={connector !== null || missing}
        onClose={onClose}
        title={connector ? connector.name : t('sso.detail.missing')}
        description={connector ? t('sso.provider.gitea') : undefined}
        footer={surface.buttons ?? undefined}
      >
        {missing ? <Notice title={t('sso.detail.missing.detail')} /> : null}
        {connector ? (
          <Link
            to="/sso/$connectorId"
            params={{ connectorId: connector.id }}
            className="mb-4 inline-flex items-center gap-1.5 font-medium text-accent-ink hover:underline"
          >
            {t('resource.openPage')}
            <ArrowRight aria-hidden className="size-4" />
          </Link>
        ) : null}
        {surface.body}
      </Drawer>
      {surface.dialogs}
    </>
  );
}

function Rows({
  items,
  list,
  configuration,
}: {
  items: readonly ManagedConnector[];
  list: ListView;
  configuration: SsoConfiguration | undefined;
}) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('sso.title')}</caption>
        <thead>
          <tr>
            <Th>{t('sso.column.name')}</Th>
            {shows('status') ? <Th>{t('sso.column.status')}</Th> : null}
            {shows('origin') ? <Th>{t('sso.column.origin')}</Th> : null}
            {shows('client') ? <Th>{t('sso.column.clientId')}</Th> : null}
            {shows('updated') ? <Th>{t('sso.column.updated')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((connector) => {
            const state = states[stateOf(connector, configuration)];
            return (
              <Tr key={connector.id}>
                <Td>
                  <button
                    type="button"
                    aria-label={t('sso.detail.open', { name: connector.name })}
                    className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                    onClick={() => {
                      list.setOpenRow(connector.id);
                    }}
                  >
                    {connector.name}
                  </button>
                  <div className="text-[12px] text-text-2">{t('sso.provider.gitea')}</div>
                </Td>
                {shows('status') ? (
                  <Td>
                    <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                  </Td>
                ) : null}
                {shows('origin') ? <Td className="break-all">{connector.origin}</Td> : null}
                {shows('client') ? (
                  <Td className="font-mono text-[12px] text-text-2">{connector.client_id}</Td>
                ) : null}
                {shows('updated') ? (
                  <Td className="text-text-2">{format.dateTime(connector.updated_at)}</Td>
                ) : null}
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

/** `/sso`: every single sign-on connector, newest first. */
export function SsoPage() {
  const { t } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const list = useListView(definition);
  const [adding, setAdding] = useState(useHashAction('add'));
  const configuration = useQuery({
    queryKey: configurationKey,
    queryFn: ({ signal }) => getSsoConfiguration({ signal }),
  });
  const connectors = useInfiniteQuery({
    queryKey: [...connectorsKey, limit],
    queryFn: ({ pageParam, signal }) =>
      listManagedConnectors({ limit, cursor: pageParam ?? undefined }, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = connectors.data?.pages.flatMap((page) => page.items) ?? [];
  const opened = items.find((connector) => connector.id === list.openRow) ?? null;
  // A row just added is not in the list until it reloads, so only a settled list can miss one.
  const missing =
    list.openRow !== null &&
    opened === null &&
    connectors.isSuccess &&
    !connectors.isFetching &&
    !connectors.hasNextPage;
  // Adding needs single sign-on on and an approved provider; the notice below says which is missing.
  const blocked = !configuration.data?.enabled || configuration.data.providers.length === 0;
  const explained = configuration.data !== undefined && blocked;
  const open = () => {
    if (!blocked) setAdding(true);
  };
  const addButton = (
    <Button
      variant="primary"
      aria-disabled={blocked ? true : undefined}
      aria-describedby={explained ? 'sso-add-blocked' : undefined}
      onClick={open}
    >
      <Plus aria-hidden />
      {t('sso.add.open')}
    </Button>
  );

  let notice = null;
  if (configuration.error && isProblem(configuration.error)) {
    notice = (
      <ProblemNotice
        className="mb-4"
        problem={configuration.error}
        onRetry={() => {
          void configuration.refetch();
        }}
      />
    );
  } else if (configuration.data && !configuration.data.enabled) {
    notice = (
      <Notice className="mb-4" title={t('sso.off.installation.title')}>
        <span id="sso-add-blocked">{t('sso.off.installation.detail')}</span>
      </Notice>
    );
  } else if (configuration.data?.providers.length === 0) {
    notice = (
      <Notice className="mb-4" title={t('sso.noProviders.title')}>
        <span id="sso-add-blocked">{t('sso.noProviders.detail')}</span>
      </Notice>
    );
  }

  let body;
  if (connectors.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (!connectors.data) {
    body = isProblem(connectors.error) ? (
      <ProblemNotice
        problem={connectors.error}
        onRetry={() => {
          void connectors.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={KeyRound}
          title={t('sso.empty.title')}
          action={blocked ? undefined : addButton}
        >
          {t('sso.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        <Rows items={items} list={list} configuration={configuration.data} />
        {connectors.hasNextPage ? (
          <Button
            className="mt-4"
            disabled={connectors.isFetchingNextPage}
            onClick={() => {
              void connectors.fetchNextPage();
            }}
          >
            {t('sso.loadMore')}
          </Button>
        ) : null}
      </>
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('sso.title')}
          </h1>
          <p className="m-0 max-w-[760px] text-text-2">{t('sso.subtitle')}</p>
        </div>
        {addButton}
      </div>
      {notice}
      {body}
      <ConnectorDrawer
        connector={opened}
        configuration={configuration.data}
        missing={missing}
        onClose={() => {
          list.setOpenRow(null);
        }}
      />
      {adding && configuration.data && !blocked ? (
        <AddConnectorDialog
          configuration={configuration.data}
          onClose={() => {
            setAdding(false);
          }}
          onAdded={(connector) => {
            setAdding(false);
            void queryClient.invalidateQueries({ queryKey: connectorsKey });
            list.setOpenRow(connector.id);
            toast({ message: t('sso.add.done', { name: connector.name }) });
          }}
        />
      ) : null}
    </>
  );
}
