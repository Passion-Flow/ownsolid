// One deployment on its own page: its replica, its configuration and who touched it. Traffic, logs,
// scaling and rollback are shown with the items that bring them (docs/research/model-services.md).
import { getDeployment, isProblem, type Deployment } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import { Button, Card, Detail, DetailList, Notice, Tabs } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { Scaling, Undo2 } from 'lucide-react';
import { DeploymentActions } from './deployment-actions.tsx';
import {
  DeploymentStatePill,
  recipeEngine,
  ReplicaFacts,
  useDeploymentNames,
  type DeploymentName,
} from './deployment-details.tsx';
import { deploymentKeys, deploymentStatus } from './deployment-state.ts';
import {
  ResourceActivity,
  ResourceHeader,
  ResourceUnavailable,
  useChosenTab,
} from './resource-page.tsx';

const route = getRouteApi('/authenticated/frame/services/$deploymentId');

const tabs = ['overview', 'traffic', 'logs', 'configuration', 'activity'] as const;

function Mono({ children }: { children: string }) {
  return <span className="font-mono text-[12.5px] [overflow-wrap:anywhere]">{children}</span>;
}

function Overview({ deployment, name }: { deployment: Deployment; name: DeploymentName }) {
  const { t, format } = useI18n();
  const verification = deployment.observation?.verification;
  return (
    <div className="flex flex-col gap-4">
      <Card className="p-[18px]">
        <h2 className="m-0 mb-1 text-[15px] font-semibold">{t('services.replicas.title')}</h2>
        <p className="m-0 mb-3 text-[13px] text-text-2">{t('services.replicas.one')}</p>
        <ReplicaFacts deployment={deployment} name={name} />
      </Card>
      <Card className="p-[18px]">
        <h2 className="m-0 mb-3 text-[15px] font-semibold">{t('services.verification.title')}</h2>
        {verification ? (
          <DetailList>
            <Detail label={t('services.verification.when')}>
              {format.dateTime(verification.observed_at)}
            </Detail>
            <Detail label={t('services.verification.tokens')}>
              {t('services.verification.tokens.value', {
                input: verification.input_tokens,
                output: verification.output_tokens,
              })}
            </Detail>
          </DetailList>
        ) : (
          <p className="m-0 text-[13px] text-text-2">{t('services.verification.none')}</p>
        )}
      </Card>
    </div>
  );
}

function Configuration({ deployment }: { deployment: Deployment }) {
  const { t, format } = useI18n();
  return (
    <Card className="p-[18px]">
      <DetailList>
        <Detail label={t('services.config.engine')}>{t(recipeEngine)}</Detail>
        <Detail label={t('services.config.kind')}>
          {t(`models.kind.${deployment.model_kind}`)}
        </Detail>
        <Detail label={t('services.config.revision')}>{deployment.revision}</Detail>
        <Detail label={t('services.config.desired')}>
          {t(`services.desired.${deployment.desired_state}`)}
        </Detail>
        <Detail label={t('services.config.catalog')}>
          <Mono>{deployment.catalog_digest}</Mono>
        </Detail>
        {deployment.serving_recipe_sha256 ? (
          <Detail label={t('services.config.recipe')}>
            <Mono>{deployment.serving_recipe_sha256}</Mono>
          </Detail>
        ) : null}
        {deployment.observation?.image_id ? (
          <Detail label={t('services.config.image')}>
            <Mono>{deployment.observation.image_id}</Mono>
          </Detail>
        ) : null}
        {deployment.observation?.container_id ? (
          <Detail label={t('services.config.container')}>
            <Mono>{deployment.observation.container_id}</Mono>
          </Detail>
        ) : null}
        <Detail label={t('services.config.created')}>
          {format.dateTime(deployment.created_at)}
        </Detail>
      </DetailList>
    </Card>
  );
}

function Logs({ deployment }: { deployment: Deployment }) {
  const { t } = useI18n();
  return (
    <Notice
      title={t('services.logs.notReady.title')}
      actions={
        deployment.last_task_id ? (
          <Link
            to="/tasks/$taskId"
            params={{ taskId: deployment.last_task_id }}
            className="font-medium text-accent-ink hover:underline"
          >
            {t('services.logs.task')}
          </Link>
        ) : null
      }
    >
      {t('services.logs.notReady.detail')}
    </Notice>
  );
}

function Loaded({ deployment }: { deployment: Deployment }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [tab, setTab] = useChosenTab(tabs);
  const name = useDeploymentNames(deployment.workspace_id, [deployment])(deployment);
  return (
    <>
      <ResourceHeader
        backTo="/services"
        backLabel={t('services.page.back')}
        title={name.title}
        description={t('services.page.description', { engine: t(recipeEngine) })}
        status={<DeploymentStatePill deployment={deployment} />}
        actions={
          <DeploymentActions
            deployment={deployment}
            name={name.model}
            onRemoved={() => {
              void navigate({ to: '/services' });
            }}
            extra={
              <>
                <Button aria-disabled aria-describedby="deployment-scale-not-ready">
                  <Scaling aria-hidden />
                  {t('services.scale.open')}
                </Button>
                <Button aria-disabled aria-describedby="deployment-rollback-not-ready">
                  <Undo2 aria-hidden />
                  {t('services.rollback.open')}
                </Button>
              </>
            }
          />
        }
      />
      <ul className="m-0 mt-[-12px] mb-4 list-none p-0 text-[12.5px] text-text-3">
        <li id="deployment-scale-not-ready">{t('services.scale.notReady')}</li>
        <li id="deployment-rollback-not-ready">{t('services.rollback.notReady')}</li>
      </ul>
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`services.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'overview' ? <Overview deployment={deployment} name={name} /> : null}
        {tab === 'traffic' ? (
          <Notice title={t('services.traffic.notReady.title')}>
            {t('services.traffic.notReady.detail')}
          </Notice>
        ) : null}
        {tab === 'logs' ? <Logs deployment={deployment} /> : null}
        {tab === 'configuration' ? <Configuration deployment={deployment} /> : null}
        {tab === 'activity' ? (
          <ResourceActivity target={deployment.id} since={deployment.created_at} />
        ) : null}
      </Tabs>
    </>
  );
}

/** `/services/$deploymentId`: one deployment, reachable by a link that names it. */
export function DeploymentPage() {
  const { t } = useI18n();
  const { deploymentId } = route.useParams();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const deployment = useQuery({
    queryKey: deploymentKeys.deployment(deploymentId),
    queryFn: ({ signal }) => getDeployment(deploymentId, { signal, background: true }),
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
    // While work is in progress the state changes on its own, so the page follows it.
    refetchInterval: (query) =>
      query.state.data && deploymentStatus(query.state.data).phase === 'changing' ? poll : false,
  });
  if (deployment.data) return <Loaded deployment={deployment.data} />;
  return (
    <ResourceUnavailable
      pending={deployment.isPending}
      error={deployment.error}
      onRetry={() => {
        void deployment.refetch();
      }}
      backTo="/services"
      backLabel={t('services.page.back')}
      missingTitle={t('services.page.missing.title')}
      missingDetail={t('services.page.missing.detail')}
      failedTitle={t('services.page.failed')}
    />
  );
}
