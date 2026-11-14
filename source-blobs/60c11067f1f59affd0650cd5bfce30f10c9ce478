// One model on its own page: what it is, what it is made of, where it came from and who touched it
// (docs/research/model-library.md).
import {
  getModel,
  isProblem,
  listDeployments,
  listModelImports,
  retireModel,
  type ModelVersion,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  Detail,
  DetailList,
  EmptyState,
  ProblemNotice,
  Skeleton,
  StatusPill,
  Table,
  Tabs,
  Tag,
  Td,
  TextField,
  Th,
  Tr,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ServerOff, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { DeployLink, DeploymentStatePill, useDeploymentNames } from './deployment-details.tsx';
import { deploymentKeys } from './deployment-state.ts';
import { ModelFacts, ModelFiles, modelSummary } from './model-details.tsx';
import { modelKeys, modelKinds, modelsKey } from './model-state.ts';
import {
  ResourceActivity,
  ResourceHeader,
  ResourceUnavailable,
  useChosenTab,
} from './resource-page.tsx';

const route = getRouteApi('/authenticated/frame/library/$modelId');

const tabs = ['overview', 'files', 'deployments', 'activity'] as const;

function RetireDialog({ model, onClose }: { model: ModelVersion; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState('');
  const retire = useMutation({
    mutationFn: () => retireModel(model.id, reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: modelsKey });
      toast({ message: t('models.retire.done', { name: model.name }) });
      void navigate({ to: '/library' });
    },
  });
  return (
    <TypedConfirmation
      title={t('models.retire.title', { name: model.name })}
      consequence={t('models.retire.consequence')}
      expected={model.name}
      confirmLabel={t('models.retire.submit')}
      pending={retire.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') retire.mutate();
      }}
    >
      <TextField
        label={t('models.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        value={reason}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {isProblem(retire.error) ? <ProblemNotice problem={retire.error} /> : null}
    </TypedConfirmation>
  );
}

/** Where a model came from: the model it was made from, or the import that brought it. */
function Origin({ model }: { model: ModelVersion }) {
  const { t, format } = useI18n();
  const { baseModelId } = modelSummary(model);
  const imports = useQuery({
    ...modelKeys.imports(model.workspace_id),
    queryFn: ({ signal }) => listModelImports(model.workspace_id, { signal }),
    enabled: baseModelId === null,
  });
  if (baseModelId !== null) {
    return (
      <Detail label={t('models.origin')}>
        <Link
          to="/library/$modelId"
          params={{ modelId: baseModelId }}
          className="text-accent-ink hover:underline"
        >
          {t('models.origin.base')}
        </Link>
      </Detail>
    );
  }
  const arrival = imports.data?.find((item) => item.model_id === model.id);
  return (
    <Detail label={t('models.origin')}>
      {arrival ? (
        <Link
          to="/library/imports/$importId"
          params={{ importId: arrival.id }}
          className="text-accent-ink hover:underline"
        >
          {t('models.origin.import', { time: format.dateTime(arrival.created_at) })}
        </Link>
      ) : (
        t('models.origin.unknown')
      )}
    </Detail>
  );
}

/** The deployments serving a model, or the way to make the first. */
function ModelDeployments({ model }: { model: ModelVersion }) {
  const { t, format } = useI18n();
  const deployments = useQuery({
    queryKey: deploymentKeys.list(model.workspace_id, model.id),
    queryFn: ({ signal }) => listDeployments(model.workspace_id, { modelId: model.id }, { signal }),
  });
  const name = useDeploymentNames(model.workspace_id, deployments.data ?? []);
  if (deployments.isPending) return <Skeleton className="h-11 w-full" />;
  if (!deployments.data) {
    return isProblem(deployments.error) ? (
      <ProblemNotice
        problem={deployments.error}
        onRetry={() => {
          void deployments.refetch();
        }}
      />
    ) : null;
  }
  if (deployments.data.length === 0) {
    return (
      <Card>
        <EmptyState
          icon={ServerOff}
          title={t('models.deployments.empty.title')}
          action={<DeployLink model={model.id} />}
        >
          {t('models.deployments.empty.detail')}
        </EmptyState>
      </Card>
    );
  }
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('models.deployments.title')}</caption>
        <thead>
          <tr>
            <Th>{t('services.fact.node')}</Th>
            <Th>{t('services.column.state')}</Th>
            <Th>{t('services.config.created')}</Th>
          </tr>
        </thead>
        <tbody>
          {deployments.data.map((deployment) => (
            <Tr key={deployment.id}>
              <Td className="font-semibold">
                <Link
                  to="/services/$deploymentId"
                  params={{ deploymentId: deployment.id }}
                  className="text-text underline-offset-2 hover:underline"
                >
                  {name(deployment).node}
                </Link>
              </Td>
              <Td>
                <DeploymentStatePill deployment={deployment} />
              </Td>
              <Td className="text-text-2">{format.dateTime(deployment.created_at)}</Td>
            </Tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function Loaded({ model }: { model: ModelVersion }) {
  const { t, format } = useI18n();
  const [tab, setTab] = useChosenTab(tabs);
  const [retiring, setRetiring] = useState(false);
  const summary = modelSummary(model);
  return (
    <>
      <ResourceHeader
        backTo="/library"
        backLabel={t('models.page.back')}
        title={model.name}
        description={summary.repository}
        status={
          <div className="flex flex-wrap items-center gap-2">
            <StatusPill tone="good">{t('models.state.ready')}</StatusPill>
            <Tag>{t(modelKinds[model.kind])}</Tag>
          </div>
        }
        actions={
          <>
            <DeployLink model={model.id} />
            <Button
              variant="danger"
              onClick={() => {
                setRetiring(true);
              }}
            >
              <Trash2 aria-hidden />
              {t('models.retire.open')}
            </Button>
          </>
        }
      />
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({
          value,
          label: t(value === 'deployments' ? 'models.deployments.title' : `resource.tab.${value}`),
        }))}
        onChange={setTab}
      >
        {tab === 'overview' ? (
          <Card className="flex flex-col gap-2.5 p-[18px]">
            <ModelFacts summary={summary} />
            <DetailList>
              <Origin model={model} />
              <Detail label={t('models.column.added')}>{format.dateTime(model.created_at)}</Detail>
            </DetailList>
          </Card>
        ) : null}
        {tab === 'files' ? (
          <Card className="overflow-x-auto">
            <ModelFiles files={summary.files} />
          </Card>
        ) : null}
        {tab === 'deployments' ? <ModelDeployments model={model} /> : null}
        {tab === 'activity' ? (
          <ResourceActivity target={model.id} since={model.created_at} />
        ) : null}
      </Tabs>
      {retiring ? (
        <RetireDialog
          model={model}
          onClose={() => {
            setRetiring(false);
          }}
        />
      ) : null}
    </>
  );
}

/** `/library/$modelId`: one model, reachable by a link that names it. */
export function ModelPage() {
  const { t } = useI18n();
  const { modelId } = route.useParams();
  const model = useQuery({
    queryKey: modelKeys.model(modelId),
    queryFn: ({ signal }) => getModel(modelId, { signal }),
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  if (model.data) return <Loaded model={model.data} />;
  return (
    <ResourceUnavailable
      pending={model.isPending}
      error={model.error}
      onRetry={() => {
        void model.refetch();
      }}
      backTo="/library"
      backLabel={t('models.page.back')}
      missingTitle={t('models.page.missing.title')}
      missingDetail={t('models.page.missing.detail')}
      failedTitle={t('models.page.failed')}
    />
  );
}
