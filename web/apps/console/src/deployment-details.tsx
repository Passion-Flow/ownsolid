// What the Model services pages share: a deployment's name, its one replica, and a failure the
// agent reported (docs/research/model-services.md).
import { getNode, listModels, taskFinished, type Deployment } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { buttonVariants, Detail, DetailList, Notice, StatusPill } from '@ownsolid/ui';
import { useQueries, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Rocket } from 'lucide-react';
import { useMemo } from 'react';
import { deploymentStatus } from './deployment-state.ts';
import { modelKeys } from './model-state.ts';
import { states as taskStates } from './task-names.ts';

/** The name of the engine the approved recipe runs (api/agent/v1/inference-recipe.json). */
export const recipeEngine: TranslationKey = 'capability.serving-engines.vllm.name';

/** The link that starts the deploy flow, with a model already chosen when there is one. */
export function DeployLink({ model, primary = true }: { model?: string; primary?: boolean }) {
  const { t } = useI18n();
  return (
    <Link
      to="/services/new"
      search={model ? { model } : {}}
      className={buttonVariants({ variant: primary ? 'primary' : 'secondary' })}
    >
      <Rocket aria-hidden />
      {t('services.new.open')}
    </Link>
  );
}

/** A deployment as the person knows it: by its model, on its node. */
export interface DeploymentName {
  model: string;
  node: string;
  /** Both together, which is how the pages call a deployment until it has a name (SRV-112). */
  title: string;
}

/** The first block of an identifier, for a resource whose name could not be read. */
function shortId(id: string): string {
  return id.slice(0, 8);
}

/**
 * Names deployments by their models and nodes. The workspace's models come whole; nodes are read
 * one by one, and a deployment sits on few of them.
 */
export function useDeploymentNames(
  workspace: string,
  deployments: readonly Deployment[],
): (deployment: Deployment) => DeploymentName {
  const { t } = useI18n();
  const models = useQuery({
    ...modelKeys.list(workspace),
    queryFn: ({ signal }) => listModels(workspace, { signal }),
  });
  const ids = useMemo(
    () => [...new Set(deployments.map((deployment) => deployment.node_id))],
    [deployments],
  );
  const nodes = useQueries({
    queries: ids.map((id) => ({
      queryKey: ['nodes', 'detail', id],
      queryFn: ({ signal }: { signal: AbortSignal }) => getNode(id, { signal }),
    })),
  });
  const nodeNames = new Map(
    nodes.flatMap((query) => (query.data ? [[query.data.id, query.data.name] as const] : [])),
  );
  const modelNames = new Map((models.data ?? []).map((model) => [model.id, model.name]));
  return (deployment: Deployment) => {
    const model = modelNames.get(deployment.model_id) ?? shortId(deployment.model_id);
    const node = nodeNames.get(deployment.node_id) ?? shortId(deployment.node_id);
    return { model, node, title: t('services.name', { model, node }) };
  };
}

/** A failure the agent or a task reported, explained in the person's language. */
export function ReportedFailure({ code }: { code: string }) {
  const { t, knows } = useI18n();
  const title = `problem.${code}.title`;
  const detail = `problem.${code}.detail`;
  const known = knows(title) && knows(detail);
  return (
    <Notice
      tone="critical"
      title={known ? t(title as TranslationKey) : t('services.failure.unknown')}
    >
      {known ? t(detail as TranslationKey) : null}{' '}
      <span className="font-mono text-[12px] text-text-3">{code}</span>
    </Notice>
  );
}

/** The state of a deployment as a pill with its word. */
export function DeploymentStatePill({ deployment }: { deployment: Deployment }) {
  const { t } = useI18n();
  const status = deploymentStatus(deployment);
  return <StatusPill tone={status.tone}>{t(status.label)}</StatusPill>;
}

/** Where a deployment runs and how it is, which is its one replica until SRV-105. */
export function ReplicaFacts({
  deployment,
  name,
}: {
  deployment: Deployment;
  name: DeploymentName;
}) {
  const { t, format } = useI18n();
  const status = deploymentStatus(deployment);
  const task = deployment.task_state;
  return (
    <div className="flex flex-col gap-3">
      {status.error ? <ReportedFailure code={status.error} /> : null}
      <DetailList>
        <Detail label={t('services.fact.state')}>
          <DeploymentStatePill deployment={deployment} />
        </Detail>
        <Detail label={t('services.fact.model')}>
          <Link
            to="/library/$modelId"
            params={{ modelId: deployment.model_id }}
            className="text-accent-ink hover:underline"
          >
            {name.model}
          </Link>
        </Detail>
        <Detail label={t('services.fact.node')}>
          <Link
            to="/nodes/$nodeId"
            params={{ nodeId: deployment.node_id }}
            className="text-accent-ink hover:underline"
          >
            {name.node}
          </Link>
        </Detail>
        <Detail label={t('services.fact.gpu')}>
          <span className="font-mono text-[12.5px] [overflow-wrap:anywhere]">
            {deployment.gpu_uuid}
          </span>
        </Detail>
        <Detail label={t('services.fact.ready')}>
          {deployment.observation?.ready
            ? t('services.fact.ready.yes')
            : t('services.fact.ready.no')}
        </Detail>
        <Detail label={t('services.fact.observed')}>
          {deployment.observed_at
            ? format.dateTime(deployment.observed_at)
            : t('services.fact.observed.never')}
          {deployment.stale ? ` · ${t('services.fact.stale')}` : ''}
        </Detail>
        {deployment.last_task_id ? (
          <Detail label={t('services.fact.task')}>
            <Link
              to="/tasks/$taskId"
              params={{ taskId: deployment.last_task_id }}
              className="text-accent-ink hover:underline"
            >
              {task ? t(taskStates[task].label) : t('services.fact.task.open')}
            </Link>
            {task && !taskFinished(task) && deployment.task_deadline_at
              ? ` · ${t('services.fact.task.deadline', { time: format.dateTime(deployment.task_deadline_at) })}`
              : ''}
          </Detail>
        ) : null}
      </DetailList>
    </div>
  );
}
