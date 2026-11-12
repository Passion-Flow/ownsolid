// A deployment on the gateway: the name callers use and how to call it, or publishing it under a
// name, with a new gateway for its node when it has none. Stopping is confirmed by typing the name
// and names the keys that stop (docs/research/gateway-publishing.md).
import {
  disableModelAlias,
  getNode,
  isProblem,
  listGatewayInstances,
  listGatewayKeys,
  listModelAliases,
  provisionGateway,
  publishModelAlias,
  revokeGatewayInstance,
  type Deployment,
  type GatewayInstance,
  type ModelAlias,
  type ProvisionedGateway,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  everyPage,
  GatewayCADownload,
  gatewayBaseUrl,
  gatewayKeys,
  IssueKeyDialog,
  pageSize,
  ReasonField,
  runtimeConfigQuery,
  sampleCall,
  useIntentKeys,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  CodeBlock,
  CopyText,
  Detail,
  DetailList,
  Dialog,
  Notice,
  ProblemNotice,
  Skeleton,
  StatusPill,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Send, Unplug } from 'lucide-react';
import { useEffect, useId, useState, type SubmitEvent } from 'react';
import { ReportedFailure } from './deployment-details.tsx';
import { AliasDescriptionDialog } from './alias-description-dialog.tsx';
import { hasAliasDescription } from './alias-description.ts';
import { deploymentKeys } from './deployment-state.ts';
import { PublishModelHint } from './publish-model-hint.tsx';
import {
  agentRunsGateway,
  aliasMaximumLength,
  aliasNameProblem,
  gatewayOrigin,
  gatewayReadiness,
  gatewayPublishKeys,
  nodeGateway,
  publishedNames,
  suggestedAliasName,
  unpublishable,
} from './gateway-publish-state.ts';

/** The workspace's names on the gateway; the key dialogs read the same list. */
function useAliases(workspace: string) {
  return useQuery({
    queryKey: gatewayKeys.aliases(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listModelAliases('admin', workspace, { limit: pageSize, cursor }, { signal }),
      ),
  });
}

/**
 * The workspace's gateway instances, one per node at most is active. While the node's agent starts
 * one, the list is read again at the installation's poll interval.
 */
function useGateways(workspace: string, node?: string) {
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  return useQuery({
    queryKey: gatewayPublishKeys.gateways(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listGatewayInstances(workspace, { limit: pageSize, cursor }, { signal }),
      ),
    refetchInterval: (query) => {
      const starting = (query.state.data?.items ?? []).some(
        (gateway) =>
          gateway.node_id === node &&
          gateway.state === 'active' &&
          gateway.delivery === 'agent' &&
          gateway.delivery_state === 'starting',
      );
      return starting ? poll : false;
    },
  });
}

/** How far the node's agent got with the gateway it runs, with what to do when it failed. */
function AgentGatewayState({
  gateway,
  node,
  replace,
}: {
  gateway: GatewayInstance;
  node: string;
  replace: { disabled: boolean; run: () => void };
}) {
  const { t } = useI18n();
  const readiness = gatewayReadiness(gateway);
  if (readiness === 'starting') {
    return (
      <Notice className="m-0" title={t('services.gateway.agent.starting', { node })}>
        {t('services.gateway.agent.startingDetail')}
      </Notice>
    );
  }
  if (readiness === 'failed') {
    return (
      <div className="flex flex-col gap-2">
        <Notice
          tone="critical"
          className="m-0"
          title={t('services.gateway.agent.failed', { node })}
          actions={
            <Button disabled={replace.disabled} onClick={replace.run}>
              {t('services.gateway.agent.replace')}
            </Button>
          }
        >
          {t('services.gateway.agent.failedDetail')}
        </Notice>
        {gateway.delivery_error_code ? (
          <ReportedFailure code={gateway.delivery_error_code} />
        ) : null}
      </div>
    );
  }
  return null;
}

/** Re-reads everything a change on the gateway can alter for this deployment. */
function useRefresh(deployment: Deployment) {
  const queryClient = useQueryClient();
  const workspace = deployment.workspace_id;
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: gatewayKeys.aliases(workspace) }),
      queryClient.invalidateQueries({ queryKey: gatewayPublishKeys.gateways(workspace) }),
      queryClient.invalidateQueries({ queryKey: gatewayKeys.list(workspace) }),
      queryClient.invalidateQueries({
        queryKey: deploymentKeys.callers(workspace, deployment.id),
      }),
    ]);
}

/** The new gateway's credential and identity, shown once, with what to do with them. */
function GatewayIdentity({ made, node }: { made: ProvisionedGateway; node: string }) {
  const { t } = useI18n();
  if (!made.secret_available || made.secret === null) return null;
  return (
    <section aria-labelledby="gateway-identity" className="flex flex-col gap-3">
      <Notice title={t('services.gateway.credential.once')} className="m-0">
        <CopyText text={made.secret} className="mt-1 break-all" />
      </Notice>
      <h3 id="gateway-identity" className="m-0 text-[14px] font-semibold">
        {t('services.gateway.credential.title')}
      </h3>
      <DetailList>
        <Detail label={t('services.gateway.credential.gateway')}>
          <CopyText text={made.gateway.id} />
        </Detail>
        <Detail label={t('services.gateway.credential.node')}>
          <CopyText text={made.gateway.node_id} />
        </Detail>
      </DetailList>
      <p className="m-0 text-[13px] text-text-2">
        {t('services.gateway.credential.use', { node, origin: made.gateway.client_origin ?? '' })}
      </p>
    </section>
  );
}

/**
 * Publishes a deployment under a name. When its node has no gateway, the dialog makes one first;
 * that gateway's credential is shown once and stays on screen if publishing then fails, so a
 * retry only publishes.
 */
function PublishDialog({
  deployment,
  model,
  node,
  onClose,
}: {
  deployment: Deployment;
  model: string;
  node: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh(deployment);
  const workspace = deployment.workspace_id;
  const gateways = useGateways(workspace, deployment.node_id);
  const intents = useIntentKeys();
  const agentGateways = useQuery(runtimeConfigQuery()).data?.agentGateways ?? false;
  const nodeRecord = useQuery({
    queryKey: ['nodes', 'detail', deployment.node_id],
    queryFn: ({ signal }) => getNode(deployment.node_id, { signal }),
  });
  // The node's agent runs the gateway, so nothing about it is typed or handed over here.
  const delivered = agentRunsGateway(agentGateways, nodeRecord.data);
  // Publishing waits for the agent to start the node's gateway, then names the model.
  const [awaiting, setAwaiting] = useState(false);
  const [name, setName] = useState(() => suggestedAliasName(model));
  const [typedOrigin, setTypedOrigin] = useState('');
  const [gatewayName, setGatewayName] = useState(node);
  const [reason, setReason] = useState('');
  const [made, setMade] = useState<ProvisionedGateway | null>(null);
  const [nameTouched, setNameTouched] = useState(false);
  const [originTouched, setOriginTouched] = useState(false);
  // A gateway made here whose credential was lost is never published to: nobody can configure it.
  const lost = made !== null && made.gateway.delivery === 'manual' && !made.secret_available;
  const existing = nodeGateway(gateways.data?.items ?? [], deployment.node_id);
  const madeGateway =
    made && (made.secret_available || made.gateway.delivery === 'agent') ? made.gateway : undefined;
  const gateway = existing && !(lost && existing.id === made.gateway.id) ? existing : madeGateway;
  const readiness = gateway ? gatewayReadiness(gateway) : undefined;
  const origin = gatewayOrigin(typedOrigin);
  const nameProblem = aliasNameProblem(name);
  const provisionBody = {
    nodeId: deployment.node_id,
    name: gatewayName.trim(),
    clientOrigin: delivered ? null : (origin ?? ''),
    reason: reason.trim(),
  };
  const provision = useMutation({
    mutationFn: () =>
      provisionGateway(workspace, provisionBody, intents.take(provisionBody)).then((answer) => {
        intents.done(provisionBody);
        return answer;
      }),
    onSuccess: (answer) => {
      setMade(answer);
      if (answer.gateway.delivery === 'agent') void gateways.refetch();
    },
    onError: (error) => {
      // Someone made this node's gateway meanwhile: read it, and the form publishes to it.
      if (isProblem(error) && error.code === 'GATEWAY_INSTANCE_EXISTS') {
        void gateways.refetch();
      }
    },
  });
  const publishBody = (gatewayId: string) => ({
    gatewayId,
    name,
    deploymentId: deployment.id,
    deploymentRevision: deployment.revision,
    reason: reason.trim(),
  });
  // `handover` says a new gateway's credential is on screen, so the dialog stays to show it.
  const publish = useMutation({
    mutationFn: ({ gatewayId }: { gatewayId: string; handover: boolean }) => {
      const body = publishBody(gatewayId);
      return publishModelAlias(workspace, body, intents.take(body)).then((alias) => {
        intents.done(body);
        return alias;
      });
    },
    onSuccess: (alias, { handover }) => {
      if (!handover) {
        toast({ message: t('services.gateway.publish.done', { name: alias.name }) });
        onClose();
      }
    },
    onSettled: refresh,
  });
  const replace = useMutation({
    mutationFn: (gatewayId: string) => revokeGatewayInstance(workspace, gatewayId, reason.trim()),
    onSuccess: () => {
      setMade(null);
      setAwaiting(false);
      provision.reset();
    },
    onSettled: refresh,
  });
  // Name the model once the agent has started the gateway. A start that failed waits for the
  // gateway to be replaced, and a failed publish for its retry.
  const publishWhenStarted = awaiting && gateway !== undefined && readiness === 'ready';
  useEffect(() => {
    if (publishWhenStarted && publish.isIdle) {
      publish.mutate({ gatewayId: gateway.id, handover: false });
    }
  }, [publishWhenStarted, publish, gateway]);
  const needsGateway = gateway === undefined;
  const deciding = needsGateway && (nodeRecord.isPending || gateways.isPending);
  const ready =
    nameProblem === null &&
    reason.trim() !== '' &&
    !lost &&
    readiness !== 'failed' &&
    !deciding &&
    (!needsGateway || ((delivered || origin !== null) && gatewayName.trim() !== ''));
  const busy =
    provision.isPending ||
    publish.isPending ||
    replace.isPending ||
    (awaiting && readiness === 'starting');
  const run = async () => {
    setNameTouched(true);
    setOriginTouched(true);
    if (!ready || busy) return;
    if (gateway) {
      if (readiness === 'starting') setAwaiting(true);
      else publish.mutate({ gatewayId: gateway.id, handover: made?.secret_available === true });
      return;
    }
    const answer = await provision.mutateAsync().catch(() => null);
    if (answer?.secret_available) publish.mutate({ gatewayId: answer.gateway.id, handover: true });
    else if (answer?.gateway.delivery === 'agent') setAwaiting(true);
  };
  const published = publish.data;
  const nameError =
    nameTouched && nameProblem ? t(`services.gateway.publish.name.${nameProblem}`) : undefined;
  return (
    <Dialog
      open
      onClose={onClose}
      title={
        published
          ? t('services.gateway.publish.doneTitle', { name: published.name })
          : t('services.gateway.publish.title')
      }
      description={published ? undefined : t('services.gateway.publish.detail', { model })}
      footer={
        published ? (
          <Button variant="primary" onClick={onClose}>
            {t('gateway.secret.done')}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            <Button type="submit" form="publish-deployment" variant="primary" disabled={busy}>
              {t('services.gateway.publish.submit')}
            </Button>
          </>
        )
      }
    >
      <div className="flex flex-col gap-4">
        {made ? <GatewayIdentity made={made} node={node} /> : null}
        {published ? (
          <p className="m-0 text-[13px] text-text-2">
            {t('services.gateway.publish.doneDetail', {
              name: published.name,
              url: gatewayBaseUrl(published.client_origin),
            })}
          </p>
        ) : (
          <form
            id="publish-deployment"
            noValidate
            className="flex flex-col gap-3.5"
            onSubmit={(event: SubmitEvent<HTMLFormElement>) => {
              event.preventDefault();
              void run();
            }}
          >
            {lost ? (
              <Notice
                tone="critical"
                className="m-0"
                title={t('services.gateway.lost.title')}
                actions={
                  <Button
                    disabled={replace.isPending || reason.trim() === ''}
                    onClick={() => {
                      replace.mutate(made.gateway.id);
                    }}
                  >
                    {t('services.gateway.lost.replace')}
                  </Button>
                }
              >
                {t('services.gateway.lost.detail')}
              </Notice>
            ) : null}
            {isProblem(replace.error) ? <ProblemNotice problem={replace.error} /> : null}
            {isProblem(nodeRecord.error) && needsGateway ? (
              <ProblemNotice
                problem={nodeRecord.error}
                onRetry={() => {
                  void nodeRecord.refetch();
                }}
              />
            ) : null}
            {isProblem(gateways.error) ? (
              <ProblemNotice
                problem={gateways.error}
                onRetry={() => {
                  void gateways.refetch();
                }}
              />
            ) : null}
            {isProblem(provision.error) && needsGateway ? (
              <ProblemNotice
                problem={provision.error}
                onRetry={() => {
                  void run();
                }}
              />
            ) : null}
            {isProblem(publish.error) ? (
              <ProblemNotice
                problem={publish.error}
                onRetry={
                  gateway
                    ? () => {
                        publish.mutate({
                          gatewayId: gateway.id,
                          handover: made?.secret_available === true,
                        });
                      }
                    : undefined
                }
              />
            ) : null}
            <TextField
              label={t('services.gateway.publish.name')}
              hint={t('services.gateway.publish.name.hint')}
              name="alias-name"
              autoComplete="off"
              spellCheck={false}
              maxLength={aliasMaximumLength}
              required
              value={name}
              error={nameError}
              onChange={(event) => {
                setName(event.target.value);
              }}
              onBlur={() => {
                setNameTouched(true);
              }}
            />
            {deciding ? <Skeleton className="h-11 w-full" /> : null}
            {gateway?.delivery === 'agent' ? (
              <AgentGatewayState
                gateway={gateway}
                node={node}
                replace={{
                  disabled: replace.isPending || reason.trim() === '',
                  run: () => {
                    replace.mutate(gateway.id);
                  },
                }}
              />
            ) : null}
            {gateway && readiness === 'ready' ? (
              <DetailList>
                <Detail label={t('services.gateway.served')}>
                  {t('services.gateway.served.value', {
                    name: gateway.name,
                    origin: gateway.client_origin ?? '',
                  })}
                </Detail>
              </DetailList>
            ) : null}
            {gateways.data && needsGateway && !deciding && delivered ? (
              <>
                <Notice className="m-0" title={t('services.gateway.agent.title', { node })}>
                  {t('services.gateway.agent.detail')}
                </Notice>
                <TextField
                  label={t('services.gateway.new.name')}
                  hint={t('services.gateway.new.name.hint')}
                  name="gateway-name"
                  autoComplete="off"
                  maxLength={100}
                  required
                  value={gatewayName}
                  error={
                    originTouched && gatewayName.trim() === ''
                      ? t('services.gateway.new.name.required')
                      : undefined
                  }
                  onChange={(event) => {
                    setGatewayName(event.target.value);
                  }}
                />
              </>
            ) : null}
            {gateways.data && needsGateway && !deciding && !delivered ? (
              <>
                <Notice className="m-0" title={t('services.gateway.new.title', { node })}>
                  {t('services.gateway.new.detail')}
                </Notice>
                <TextField
                  label={t('services.gateway.new.origin')}
                  hint={t('services.gateway.new.origin.hint')}
                  name="gateway-origin"
                  type="url"
                  inputMode="url"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={2048}
                  required
                  value={typedOrigin}
                  error={
                    originTouched && origin === null
                      ? t('services.gateway.new.origin.invalid')
                      : undefined
                  }
                  onChange={(event) => {
                    setTypedOrigin(event.target.value);
                  }}
                  onBlur={() => {
                    setOriginTouched(true);
                  }}
                />
                <TextField
                  label={t('services.gateway.new.name')}
                  hint={t('services.gateway.new.name.hint')}
                  name="gateway-name"
                  autoComplete="off"
                  maxLength={100}
                  required
                  value={gatewayName}
                  error={
                    originTouched && gatewayName.trim() === ''
                      ? t('services.gateway.new.name.required')
                      : undefined
                  }
                  onChange={(event) => {
                    setGatewayName(event.target.value);
                  }}
                />
              </>
            ) : null}
            <ReasonField value={reason} onChange={setReason} />
          </form>
        )}
      </div>
    </Dialog>
  );
}

/** Stops publishing a name after it is typed; the keys that call it are named first. */
function StopDialog({
  deployment,
  alias,
  onClose,
}: {
  deployment: Deployment;
  alias: ModelAlias;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh(deployment);
  const workspace = deployment.workspace_id;
  const [reason, setReason] = useState('');
  const keys = useQuery({
    queryKey: gatewayKeys.list(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) =>
        listGatewayKeys('admin', workspace, { limit: pageSize, cursor }, { signal }),
      ),
  });
  const callers = (keys.data?.items ?? []).filter(
    (key) => key.alias_id === alias.id && key.state === 'active',
  );
  const stop = useMutation({
    mutationFn: () =>
      disableModelAlias(workspace, alias.id, {
        expectedRevision: alias.revision,
        reason: reason.trim(),
      }),
    onSuccess: () => {
      toast({ message: t('services.gateway.stop.done', { name: alias.name }) });
      onClose();
    },
    onSettled: refresh,
  });
  let named;
  if (keys.isPending) {
    named = <Skeleton className="h-10 w-full" />;
  } else if (isProblem(keys.error)) {
    named = (
      <ProblemNotice
        problem={keys.error}
        onRetry={() => {
          void keys.refetch();
        }}
      />
    );
  } else if (callers.length === 0) {
    named = <p className="m-0">{t('services.gateway.stop.noKeys')}</p>;
  } else {
    named = (
      <>
        <p className="m-0 mb-1 font-medium text-text">
          {t('services.gateway.stop.keys', { count: callers.length })}
        </p>
        <ul className="m-0 pl-5">
          {callers.map((key) => (
            <li key={key.id}>
              {key.label} <span className="font-mono text-[12px]">{key.prefix}</span>
            </li>
          ))}
        </ul>
        {keys.data?.complete === false ? (
          <p className="m-0 mt-2">{t('services.callers.partial')}</p>
        ) : null}
      </>
    );
  }
  return (
    <TypedConfirmation
      title={t('services.gateway.stop.title', { name: alias.name })}
      consequence={
        <div className="flex flex-col gap-2 text-[13px] text-text-2">
          <p className="m-0">{t('services.gateway.stop.consequence', { name: alias.name })}</p>
          {named}
        </div>
      }
      expected={alias.name}
      confirmLabel={t('services.gateway.stop.submit')}
      pending={stop.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') stop.mutate();
      }}
    >
      <ReasonField value={reason} onChange={setReason} />
      {isProblem(stop.error) ? <ProblemNotice problem={stop.error} /> : null}
    </TypedConfirmation>
  );
}

/** One published name: whether it answers, where, and how to call it. */
function Published({
  alias,
  workspace,
  onIssue,
  onStop,
  onEdit,
}: {
  alias: ModelAlias;
  workspace: string;
  onIssue: () => void;
  onStop: () => void;
  onEdit: () => void;
}) {
  const { t } = useI18n();
  const descriptionHint = useId();
  const described = hasAliasDescription(alias);
  return (
    <div role="group" aria-label={alias.name} className="flex flex-col gap-3">
      <DetailList>
        <Detail label={t('services.gateway.name')}>
          <CopyText text={alias.name} />
        </Detail>
        <Detail label={t('services.gateway.state')}>
          {alias.available ? (
            <StatusPill tone="good">{t('services.gateway.available')}</StatusPill>
          ) : (
            <StatusPill tone="warning">{t('services.gateway.unavailable')}</StatusPill>
          )}
        </Detail>
        <Detail label={t('services.gateway.baseUrl')}>
          <CopyText text={gatewayBaseUrl(alias.client_origin)} />
        </Detail>
        <Detail label={t('services.gateway.description.label')}>
          <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">
            {alias.description?.length
              ? alias.description
              : t('services.gateway.description.empty')}
          </span>
        </Detail>
      </DetailList>
      <div className="flex flex-col items-start gap-1">
        <Button
          size="sm"
          aria-disabled={!described}
          aria-describedby={described ? undefined : descriptionHint}
          onClick={() => {
            if (described) onEdit();
          }}
        >
          {t('services.gateway.description.edit')}
        </Button>
        {described ? null : (
          <p id={descriptionHint} className="m-0 text-[12.5px] text-text-3">
            {t('services.gateway.description.notReady')}
          </p>
        )}
      </div>
      <GatewayCADownload workspace={workspace} aliasId={alias.id} />
      {alias.available ? null : (
        <p id={`${descriptionHint}-availability`} className="m-0 text-[13px] text-text-2">
          {t('services.gateway.unavailable.detail')}
        </p>
      )}
      <CodeBlock
        label={t('services.gateway.sample')}
        code={sampleCall('curl', alias.client_origin, alias.name)}
      />
      <div className="flex flex-wrap gap-2">
        <Button
          aria-disabled={!alias.available}
          aria-describedby={alias.available ? undefined : `${descriptionHint}-availability`}
          onClick={() => {
            if (alias.available) onIssue();
          }}
        >
          <KeyRound aria-hidden />
          {t('services.gateway.issue')}
        </Button>
        <Button onClick={onStop}>
          <Unplug aria-hidden />
          {t('services.gateway.stop.open')}
        </Button>
      </div>
    </div>
  );
}

type Open =
  { kind: 'publish' } | { kind: 'issue' | 'stop' | 'description'; alias: ModelAlias } | null;

/** The deployment's Overview card for the gateway. */
export function DeploymentGateway({
  deployment,
  model,
  node,
}: {
  deployment: Deployment;
  model: string;
  node: string;
}) {
  const { t } = useI18n();
  const workspace = deployment.workspace_id;
  const aliases = useAliases(workspace);
  const [open, setOpen] = useState<Open>(null);
  const heading = useId();
  const close = () => {
    setOpen(null);
  };
  const names = publishedNames(aliases.data?.items ?? [], deployment.id);
  const blocked = unpublishable(deployment);
  let body;
  if (aliases.isPending) {
    body = <Skeleton className="h-11 w-full" />;
  } else if (!aliases.data) {
    body = isProblem(aliases.error) ? (
      <ProblemNotice
        problem={aliases.error}
        onRetry={() => {
          void aliases.refetch();
        }}
      />
    ) : null;
  } else if (names.length === 0) {
    body = (
      <div className="flex flex-col items-start gap-3">
        <p className="m-0 text-[13px] text-text-2">{t('services.gateway.none')}</p>
        {aliases.data.complete ? null : (
          <p className="m-0 text-[13px] text-text-2">{t('services.gateway.partial')}</p>
        )}
        {blocked ? (
          <p className="m-0 text-[13px] text-text-2">{t(`services.gateway.blocked.${blocked}`)}</p>
        ) : (
          <Button
            variant="primary"
            onClick={() => {
              setOpen({ kind: 'publish' });
            }}
          >
            <Send aria-hidden />
            {t('services.gateway.publish.open')}
          </Button>
        )}
      </div>
    );
  } else {
    body = (
      <div className="flex flex-col gap-5">
        {names.map((alias) => (
          <Published
            key={alias.id}
            alias={alias}
            workspace={workspace}
            onIssue={() => {
              setOpen({ kind: 'issue', alias });
            }}
            onStop={() => {
              setOpen({ kind: 'stop', alias });
            }}
            onEdit={() => {
              setOpen({ kind: 'description', alias });
            }}
          />
        ))}
      </div>
    );
  }
  return (
    <Card className="p-[18px]" aria-labelledby={heading}>
      <h2 id={heading} className="m-0 mb-1 text-[15px] font-semibold">
        {t('services.gateway.title')}
      </h2>
      <p className="m-0 mb-3 text-[13px] text-text-2">{t('services.gateway.subtitle')}</p>
      {body}
      {open?.kind === 'publish' ? (
        <PublishDialog deployment={deployment} model={model} node={node} onClose={close} />
      ) : null}
      {open?.kind === 'issue' ? (
        <IssueKeyDialog
          workspace={workspace}
          alias={open.alias.id}
          noModel={<PublishModelHint />}
          onClose={close}
        />
      ) : null}
      {open?.kind === 'stop' ? (
        <StopDialog deployment={deployment} alias={open.alias} onClose={close} />
      ) : null}
      {open?.kind === 'description' && hasAliasDescription(open.alias) ? (
        <AliasDescriptionDialog key={open.alias.id} alias={open.alias} onClose={close} />
      ) : null}
    </Card>
  );
}
