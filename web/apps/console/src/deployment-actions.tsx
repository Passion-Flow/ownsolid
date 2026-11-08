// What a person can do to a deployment. Starting, checking and verifying change nothing that
// cannot be done again, so they act at once; stopping and removing break whoever calls the model,
// so they name the callers and confirm by typing (docs/research/model-services.md).
import {
  isProblem,
  listGatewayKeys,
  listModelAliases,
  requestDeploymentAction,
  type Deployment,
  type DeploymentAction,
  type GatewayKey,
  type ModelAlias,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  ProblemNotice,
  Skeleton,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Activity, BadgeCheck, Play, Square, Trash2 } from 'lucide-react';
import { useRef, useState, type ReactNode } from 'react';
import { canStart, canStop, deploymentKeys, deploymentsKey } from './deployment-state.ts';
import { everyPage, pageSize } from './every-page.ts';

/** The gateway names that send traffic to a deployment, and the keys that call them. */
async function readCallers(
  workspace: string,
  deployment: string,
  signal: AbortSignal,
): Promise<{ aliases: ModelAlias[]; keys: GatewayKey[]; complete: boolean }> {
  const aliases = await everyPage((cursor) =>
    listModelAliases(workspace, { limit: pageSize, cursor }, { signal }),
  );
  const mine = aliases.items.filter(
    (alias) => alias.deployment_id === deployment && alias.disabled_at === null,
  );
  if (mine.length === 0) return { aliases: [], keys: [], complete: aliases.complete };
  const keys = await everyPage((cursor) =>
    listGatewayKeys(workspace, { limit: pageSize, cursor }, { signal }),
  );
  const named = new Set(mine.map((alias) => alias.id));
  return {
    aliases: mine,
    keys: keys.items.filter((key) => named.has(key.alias_id) && key.state === 'active'),
    complete: aliases.complete && keys.complete,
  };
}

function Callers({ deployment }: { deployment: Deployment }) {
  const { t } = useI18n();
  const callers = useQuery({
    queryKey: deploymentKeys.callers(deployment.workspace_id, deployment.id),
    queryFn: ({ signal }) => readCallers(deployment.workspace_id, deployment.id, signal),
  });
  if (callers.isPending) return <Skeleton className="h-10 w-full" />;
  if (!callers.data) {
    return isProblem(callers.error) ? (
      <ProblemNotice
        problem={callers.error}
        onRetry={() => {
          void callers.refetch();
        }}
      />
    ) : null;
  }
  const { aliases, keys, complete } = callers.data;
  return (
    <div className="text-[13px] text-text-2">
      {aliases.length === 0 ? (
        <p className="m-0">{t('services.callers.none')}</p>
      ) : (
        <>
          <p className="m-0 mb-1 font-medium text-text">
            {t('services.callers.aliases', { count: aliases.length })}
          </p>
          <ul className="m-0 mb-2 pl-5">
            {aliases.map((alias) => (
              <li key={alias.id} className="font-mono text-[12.5px]">
                {alias.name}
              </li>
            ))}
          </ul>
          <p className="m-0 mb-1 font-medium text-text">
            {t('services.callers.keys', { count: keys.length })}
          </p>
          {keys.length > 0 ? (
            <ul className="m-0 pl-5">
              {keys.map((key) => (
                <li key={key.id}>
                  {key.label} <span className="font-mono text-[12px]">{key.prefix}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </>
      )}
      {complete ? null : <p className="m-0 mt-2">{t('services.callers.partial')}</p>}
    </div>
  );
}

type Dialog = 'stop' | 'remove' | null;

/** Sends one action, with a key kept until it is answered so a retry cannot act twice. */
function useAction(deployment: Deployment) {
  const queryClient = useQueryClient();
  const keys = useRef(new Map<string, string>());
  return useMutation({
    mutationFn: ({ action, reason }: { action: DeploymentAction; reason: string }) => {
      const intent = `${action}:${String(deployment.revision)}:${reason}`;
      const key = keys.current.get(intent) ?? crypto.randomUUID();
      keys.current.set(intent, key);
      return requestDeploymentAction(
        deployment.id,
        { action, revision: deployment.revision, reason },
        key,
      ).then((updated) => {
        keys.current.delete(intent);
        return updated;
      });
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(deploymentKeys.deployment(updated.id), updated);
    },
    // A refusal on stale data is fixed by the current revision.
    onSettled: () => queryClient.invalidateQueries({ queryKey: deploymentsKey }),
  });
}

function ReasonField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useI18n();
  return (
    <TextField
      label={t('services.reason')}
      name="reason"
      autoComplete="off"
      maxLength={500}
      required
      value={value}
      error={value.trim() === '' ? t('services.reason.required') : undefined}
      onChange={(event) => {
        onChange(event.target.value);
      }}
    />
  );
}

function Confirm({
  deployment,
  name,
  kind,
  onClose,
  onDone,
}: {
  deployment: Deployment;
  name: string;
  kind: 'stop' | 'remove';
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [reason, setReason] = useState(t(`services.${kind}.reason.default`));
  const act = useAction(deployment);
  return (
    <TypedConfirmation
      title={t(`services.${kind}.title`, { name })}
      consequence={
        <>
          <p className="m-0 mb-2">{t(`services.${kind}.consequence`)}</p>
          <Callers deployment={deployment} />
        </>
      }
      expected={name}
      confirmLabel={t(`services.${kind}.submit`)}
      pending={act.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() === '') return;
        act.mutate(
          { action: kind, reason: reason.trim() },
          {
            onSuccess: () => {
              toast({ message: t(`services.${kind}.done`, { name }) });
              onDone();
            },
          },
        );
      }}
    >
      <ReasonField value={reason} onChange={setReason} />
      {isProblem(act.error) ? <ProblemNotice problem={act.error} /> : null}
    </TypedConfirmation>
  );
}

/** The actions of one deployment, its dialogs, and the failure of the last one. */
export function DeploymentActions({
  deployment,
  name,
  onRemoved,
  extra,
}: {
  deployment: Deployment;
  name: string;
  /** Where to go once a removal has been asked for. */
  onRemoved?: (() => void) | undefined;
  /** Actions that come first, such as the page's own primary. */
  extra?: ReactNode;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [dialog, setDialog] = useState<Dialog>(null);
  const act = useAction(deployment);
  const now = (action: DeploymentAction) => {
    act.mutate(
      { action, reason: t(`services.${action}.reason.default`) },
      {
        onSuccess: () => {
          toast({ message: t(`services.${action}.done`, { name }) });
        },
      },
    );
  };
  const changing = act.isPending;
  if (deployment.removed_at !== null) return null;
  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex flex-wrap items-center justify-end gap-2">
        {extra}
        {canStart(deployment) ? (
          <Button
            variant="primary"
            disabled={changing}
            onClick={() => {
              now('deploy');
            }}
          >
            <Play aria-hidden />
            {t('services.deploy.open')}
          </Button>
        ) : null}
        <Button
          disabled={changing}
          onClick={() => {
            now('observe');
          }}
        >
          <Activity aria-hidden />
          {t('services.observe.open')}
        </Button>
        {deployment.observed_state === 'running' ? (
          <Button
            disabled={changing}
            onClick={() => {
              now('verify');
            }}
          >
            <BadgeCheck aria-hidden />
            {t('services.verify.open')}
          </Button>
        ) : null}
        {canStop(deployment) ? (
          <Button
            disabled={changing}
            onClick={() => {
              setDialog('stop');
            }}
          >
            <Square aria-hidden />
            {t('services.stop.open')}
          </Button>
        ) : null}
        <Button
          variant="danger"
          disabled={changing || deployment.desired_state === 'removed'}
          onClick={() => {
            setDialog('remove');
          }}
        >
          <Trash2 aria-hidden />
          {t('services.remove.open')}
        </Button>
      </div>
      {isProblem(act.error) ? (
        <ProblemNotice className="w-full max-w-[560px] text-left" problem={act.error} />
      ) : null}
      {dialog ? (
        <Confirm
          deployment={deployment}
          name={name}
          kind={dialog}
          onClose={() => {
            setDialog(null);
          }}
          onDone={() => {
            setDialog(null);
            if (dialog === 'remove') onRemoved?.();
          }}
        />
      ) : null}
    </div>
  );
}
