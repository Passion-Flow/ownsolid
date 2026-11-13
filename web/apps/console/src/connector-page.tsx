// One single sign-on connector on its own page: its check and details, and who changed it
// (docs/research/resource-detail-pages.md). The API lists connectors and an installation has a handful, so
// the page finds its own in the list.
import {
  getSsoConfiguration,
  isProblem,
  listManagedConnectors,
  type ManagedConnector,
  type SsoConfiguration,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import { Card, StatusPill, Tabs } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import {
  ResourceActivity,
  ResourceHeader,
  ResourceUnavailable,
  useChosenTab,
} from './resource-page.tsx';
import { connectorsKey } from './sso-dialogs.tsx';
import { configurationKey, stateOf, states, useConnectorSurface } from './sso-surface.tsx';

const route = getRouteApi('/authenticated/frame/sso/$connectorId');

const tabs = ['overview', 'activity'] as const;

function Loaded({
  connector,
  configuration,
}: {
  connector: ManagedConnector;
  configuration: SsoConfiguration | undefined;
}) {
  const { t } = useI18n();
  const [tab, setTab] = useChosenTab(tabs);
  const surface = useConnectorSurface(connector, configuration);
  const state = states[stateOf(connector, configuration)];
  return (
    <>
      <ResourceHeader
        backTo="/sso"
        backLabel={t('sso.page.back')}
        title={connector.name}
        description={t('sso.provider.gitea')}
        status={<StatusPill tone={state.tone}>{t(state.label)}</StatusPill>}
        actions={surface.buttons}
      />
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`resource.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'overview' ? <Card className="p-[18px]">{surface.body}</Card> : null}
        {tab === 'activity' ? <ResourceActivity target={connector.id} /> : null}
      </Tabs>
      {surface.dialogs}
    </>
  );
}

/** `/sso/$connectorId`: one connector, reachable by a link that names it. */
export function ConnectorPage() {
  const { t } = useI18n();
  const { connectorId } = route.useParams();
  const lookup = useQuery(runtimeConfigQuery()).data?.ui.lookupPageSize ?? 100;
  const configuration = useQuery({
    queryKey: configurationKey,
    queryFn: ({ signal }) => getSsoConfiguration({ signal }),
  });
  const connectors = useQuery({
    queryKey: [...connectorsKey, 'lookup', lookup],
    queryFn: ({ signal }) => listManagedConnectors({ limit: lookup }, { signal }),
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  const connector = connectors.data?.items.find((item) => item.id === connectorId);
  if (connector) return <Loaded connector={connector} configuration={configuration.data} />;
  return (
    <ResourceUnavailable
      pending={connectors.isPending}
      error={connectors.error}
      onRetry={() => {
        void connectors.refetch();
      }}
      backTo="/sso"
      backLabel={t('sso.page.back')}
      missingTitle={t('sso.page.missing.title')}
      missingDetail={t('sso.page.missing.detail')}
      failedTitle={t('sso.detail.missing')}
    />
  );
}
