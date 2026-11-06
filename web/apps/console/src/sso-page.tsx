// Single sign-on connectors: which providers people can sign in with, how to add one, and a check
// that tells which part of a connector is wrong (docs/research/sso-page.md).
import {
  getSsoConfiguration,
  isProblem,
  listManagedConnectors,
  testConnector,
  type ConnectorCheck,
  type ManagedConnector,
  type SsoConfiguration,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
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
  CopyText,
  Detail,
  DetailList,
  Drawer,
  EmptyState,
  Notice,
  ProblemNotice,
  Skeleton,
  Spinner,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
  useToast,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Pencil, Plus, Power, PowerOff, Stethoscope } from 'lucide-react';
import { useState } from 'react';
import {
  AddConnectorDialog,
  CallbackUrls,
  connectorsKey,
  EditConnectorDialog,
  SwitchConnectorDialog,
} from './sso-dialogs.tsx';

const configurationKey = ['sso', 'admin', 'configuration'] as const;

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

type State = 'on' | 'off' | 'unapproved';

const states: Record<State, { tone: StatusTone; label: TranslationKey }> = {
  on: { tone: 'good', label: 'sso.status.on' },
  off: { tone: 'neutral', label: 'sso.status.off' },
  unapproved: { tone: 'warning', label: 'sso.status.unapproved' },
};

/** A connector whose origin left the configuration cannot be used, whatever its switch says. */
function stateOf(connector: ManagedConnector, configuration: SsoConfiguration | undefined): State {
  if (configuration && !configuration.providers.some((p) => p.origin === connector.origin)) {
    return 'unapproved';
  }
  return connector.enabled ? 'on' : 'off';
}

/** What a check found, in words, with the next step. */
function CheckResult({ check, connector }: { check: ConnectorCheck; connector: ManagedConnector }) {
  const { t, format } = useI18n();
  const passed = check.outcome === 'passed';
  const values = {
    origin: connector.origin,
    url: check.callback_url ?? '',
    code: check.provider_error ?? t('sso.check.noCode'),
  };
  return (
    <Notice tone={passed ? 'info' : 'critical'} title={t(`sso.check.${check.outcome}.title`)}>
      <p className="m-0">{t(`sso.check.${check.outcome}.detail`, values)}</p>
      <p className="m-0 mt-1 text-[12px] text-text-2">
        {t('sso.check.at', { time: format.dateTime(check.checked_at) })}
      </p>
    </Notice>
  );
}

type Opened = 'edit' | 'switch' | null;

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
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Opened>(null);
  const [lastCheck, setLastCheck] = useState<ConnectorCheck | null>(null);
  const check = useMutation({
    mutationFn: (id: string) => testConnector(id),
    onSuccess: setLastCheck,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: connectorsKey });
  const changed = (message: string) => {
    setDialog(null);
    // A change can make an earlier check say nothing about this connector.
    setLastCheck(null);
    void refresh();
    toast({ message });
  };
  const state = connector ? stateOf(connector, configuration) : null;
  const last = connector && lastCheck?.connector_id === connector.id ? lastCheck : null;
  const usable = configuration?.enabled === true;
  return (
    <>
      <Drawer
        open={connector !== null || missing}
        onClose={onClose}
        title={connector ? connector.name : t('sso.detail.missing')}
        description={connector ? t('sso.provider.gitea') : undefined}
        footer={
          connector ? (
            <>
              <Button
                onClick={() => {
                  setDialog('edit');
                }}
              >
                <Pencil aria-hidden />
                {t('sso.edit.open')}
              </Button>
              {connector.enabled ? (
                <Button
                  variant="danger"
                  onClick={() => {
                    setDialog('switch');
                  }}
                >
                  <PowerOff aria-hidden />
                  {t('sso.off.open')}
                </Button>
              ) : (
                <Button
                  variant="primary"
                  aria-disabled={!usable || state === 'unapproved' ? true : undefined}
                  onClick={() => {
                    if (usable && state !== 'unapproved') setDialog('switch');
                  }}
                >
                  <Power aria-hidden />
                  {t('sso.on.open')}
                </Button>
              )}
            </>
          ) : undefined
        }
      >
        {missing ? <Notice title={t('sso.detail.missing.detail')} /> : null}
        {connector && configuration && !usable ? (
          <Notice className="mb-4" title={t('sso.off.installation.title')}>
            {t('sso.off.installation.detail')}
          </Notice>
        ) : null}
        {connector && usable && state === 'unapproved' ? (
          <Notice className="mb-4" tone="critical" title={t('sso.detail.unapproved.title')}>
            {t('sso.detail.unapproved.detail', { origin: connector.origin })}
          </Notice>
        ) : null}
        {connector && state ? (
          <div className="flex flex-col gap-5">
            <section aria-labelledby="sso-check" className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 id="sso-check" className="m-0 text-[14px] font-semibold">
                  {t('sso.check.title')}
                </h3>
                <Button
                  disabled={check.isPending || !usable}
                  onClick={() => {
                    check.mutate(connector.id);
                  }}
                >
                  {check.isPending ? (
                    <Spinner label={t('sso.check.running')} />
                  ) : (
                    <Stethoscope aria-hidden />
                  )}
                  {t('sso.check.open')}
                </Button>
              </div>
              <p className="m-0 text-[13px] text-text-2">{t('sso.check.detail')}</p>
              {check.error && isProblem(check.error) ? (
                <ProblemNotice
                  problem={check.error}
                  onRetry={() => {
                    check.mutate(connector.id);
                  }}
                />
              ) : null}
              {last ? <CheckResult check={last} connector={connector} /> : null}
            </section>
            <DetailList>
              <Detail label={t('sso.column.status')}>
                <StatusPill tone={states[state].tone}>{t(states[state].label)}</StatusPill>
              </Detail>
              <Detail label={t('sso.column.origin')}>
                <CopyText text={connector.origin} />
              </Detail>
              <Detail label={t('sso.column.clientId')}>
                <CopyText text={connector.client_id} />
              </Detail>
              <Detail label={t('sso.callbacks.title')}>
                {configuration ? (
                  <CallbackUrls configuration={configuration} connectorId={connector.id} />
                ) : (
                  '-'
                )}
              </Detail>
              <Detail label={t('sso.detail.created')}>
                {format.dateTime(connector.created_at)}
              </Detail>
              <Detail label={t('sso.column.updated')}>
                {format.dateTime(connector.updated_at)}
              </Detail>
              <Detail label={t('sso.detail.id')}>
                <CopyText text={connector.id} />
              </Detail>
            </DetailList>
          </div>
        ) : null}
      </Drawer>
      {connector && dialog === 'edit' ? (
        <EditConnectorDialog
          connector={connector}
          onClose={() => {
            setDialog(null);
          }}
          onSaved={(updated) => {
            changed(t('sso.edit.done', { name: updated.name }));
          }}
        />
      ) : null}
      {connector && dialog === 'switch' ? (
        <SwitchConnectorDialog
          connector={connector}
          onClose={() => {
            setDialog(null);
          }}
          onSwitched={(updated) => {
            changed(
              updated.enabled
                ? t('sso.on.done', { name: updated.name })
                : t('sso.off.done', { name: updated.name }),
            );
          }}
        />
      ) : null}
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
