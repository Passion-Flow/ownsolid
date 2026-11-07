// Deploying a model: choose it, read where it would run and what that takes, and commit. The
// server judges every GPU with the checks the deployment itself makes, so what this page recommends
// the deployment accepts (docs/research/model-services.md).
import {
  createDeployment,
  getPlacement,
  isProblem,
  listModels,
  listNodes,
  observeInferenceNode,
  type Placement,
  type PlacementGpu,
  type PlacementNode,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { runtimeConfigQuery, useWorkspaces } from '@ownsolid/shell';
import {
  AdvancedFold,
  Button,
  buttonVariants,
  Card,
  Detail,
  DetailList,
  EmptyState,
  Notice,
  ProblemNotice,
  SelectField,
  Skeleton,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useRouter, useRouterState } from '@tanstack/react-router';
import { ArrowLeft, Boxes, Cpu, Rocket } from 'lucide-react';
import { useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { recipeEngine } from './deployment-details.tsx';
import { deploymentKeys, deploymentsKey } from './deployment-state.ts';
import { modelKeys } from './model-state.ts';

const mebibyte = 1024 * 1024;

/** One GPU a deployment could take, with the node it sits on. */
interface Option {
  node: PlacementNode;
  gpu: PlacementGpu;
}

function eligible(placement: Placement): Option[] {
  return placement.candidates.flatMap((node) =>
    node.refusal === null
      ? node.gpus.filter((gpu) => gpu.refusal === null).map((gpu) => ({ node, gpu }))
      : [],
  );
}

function optionKey(option: { node_id: string; gpu_uuid: string }): string {
  return `${option.node_id}/${option.gpu_uuid}`;
}

/** The flow's choices live in the address, so leaving and coming back keeps the draft. */
function useDraft(): [Record<string, string>, (next: Record<string, string | null>) => void] {
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const current = Object.fromEntries(new URLSearchParams(location.searchStr));
  return [
    current,
    (next) => {
      const params = new URLSearchParams(location.searchStr);
      for (const [key, value] of Object.entries(next)) {
        if (value === null) params.delete(key);
        else params.set(key, value);
      }
      const search = params.toString();
      void router.navigate({
        href: `${location.pathname}${search ? `?${search}` : ''}`,
        replace: true,
      });
    },
  ];
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card className="p-[18px]">
      <h2 className="m-0 mb-3 text-[15px] font-semibold">{title}</h2>
      {children}
    </Card>
  );
}

function refusalText(t: (key: TranslationKey) => string, code: string): string {
  return t(`problem.${code}.title` as TranslationKey);
}

/** Asks a node's agent to read its GPUs again, once per press. */
function ReadAgain({ nodeId, onRead }: { nodeId: string; onRead: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const key = useRef<string | null>(null);
  const read = useMutation({
    mutationFn: () => {
      key.current ??= crypto.randomUUID();
      return observeInferenceNode(nodeId, t('services.new.read.reason'), key.current);
    },
    onSuccess: () => {
      key.current = null;
      toast({ message: t('services.new.read.done') });
      onRead();
    },
  });
  return (
    <span className="inline-flex flex-col gap-1">
      <Button
        disabled={read.isPending}
        onClick={() => {
          read.mutate();
        }}
      >
        <Cpu aria-hidden />
        {t('services.new.read.open')}
      </Button>
      {isProblem(read.error) ? <ProblemNotice problem={read.error} /> : null}
    </span>
  );
}

/** The nodes that reported GPUs, and why each cannot take the model. */
function Reasons({ placement, onRead }: { placement: Placement; onRead: () => void }) {
  const { t, format } = useI18n();
  return (
    <ul className="m-0 flex list-none flex-col gap-3 p-0">
      {placement.candidates.map((node) => (
        <li key={node.node_id} className="rounded-[12px] bg-surface-2 px-3.5 py-3 text-[13px]">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              <Link
                to="/nodes/$nodeId"
                params={{ nodeId: node.node_id }}
                className="font-semibold text-text hover:underline"
              >
                {node.name}
              </Link>{' '}
              <span className="text-text-3">
                {t('services.new.observed', { time: format.dateTime(node.observed_at) })}
              </span>
            </span>
            {node.refusal === 'INFERENCE_PREFLIGHT_REQUIRED' ? (
              <ReadAgain nodeId={node.node_id} onRead={onRead} />
            ) : null}
          </div>
          {node.refusal ? (
            <p className="m-0 mt-1 text-text-2">{refusalText(t, node.refusal)}</p>
          ) : (
            <ul className="m-0 mt-1 pl-5 text-text-2">
              {node.gpus.map((gpu) => (
                <li key={gpu.uuid}>
                  {gpu.name}: {gpu.refusal ? refusalText(t, gpu.refusal) : t('services.new.fits')}
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}

/** For a workspace whose nodes never reported GPUs: ask one to. */
function ReadAnyNode({ onRead }: { onRead: () => void }) {
  const { t } = useI18n();
  const size = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const nodes = useQuery({
    queryKey: ['nodes', 'online', size],
    queryFn: ({ signal }) =>
      listNodes({ limit: size, status: 'active', connectivity: 'online' }, { signal }),
  });
  const [chosen, setChosen] = useState('');
  if (nodes.isPending) return <Skeleton className="h-10 w-full" />;
  if (!nodes.data) {
    return isProblem(nodes.error) ? (
      <ProblemNotice
        problem={nodes.error}
        onRetry={() => {
          void nodes.refetch();
        }}
      />
    ) : null;
  }
  const items = nodes.data.items;
  if (items.length === 0) {
    return (
      <p className="m-0 text-[13px] text-text-2">
        {t('services.new.noNodes')}{' '}
        <Link to="/nodes" hash="add" className="text-accent-ink underline">
          {t('services.new.noNodes.link')}
        </Link>
      </p>
    );
  }
  const node = chosen || items[0]?.id || '';
  return (
    <div className="flex flex-wrap items-end gap-2.5">
      <SelectField
        label={t('services.new.read.node')}
        value={node}
        options={items.map((item) => ({ value: item.id, label: item.name }))}
        onChange={(event) => {
          setChosen(event.target.value);
        }}
      />
      <ReadAgain key={node} nodeId={node} onRead={onRead} />
    </div>
  );
}

function NotReady({ label, value, reason }: { label: string; value: string; reason: string }) {
  const id = `not-ready-${label.replace(/\W+/g, '-').toLowerCase()}`;
  return (
    <Detail label={label}>
      <span aria-disabled aria-describedby={id} className="text-text-2">
        {value}
      </span>
      <span id={id} className="block text-[12.5px] text-text-3">
        {reason}
      </span>
    </Detail>
  );
}

function Advanced({
  options,
  chosen,
  onChoose,
}: {
  options: readonly Option[];
  chosen: string;
  onChoose: (key: string) => void;
}) {
  const { t, format } = useI18n();
  return (
    <AdvancedFold label={t('services.new.advanced')}>
      <div className="flex flex-col gap-3.5">
        {options.length > 1 ? (
          <SelectField
            label={t('services.new.gpu')}
            value={chosen}
            options={options.map((option) => ({
              value: optionKey({ node_id: option.node.node_id, gpu_uuid: option.gpu.uuid }),
              label: t('services.new.gpu.option', {
                node: option.node.name,
                gpu: option.gpu.name,
                free: format.bytes(option.gpu.memory_free_mib * mebibyte),
              }),
            }))}
            onChange={(event) => {
              onChoose(event.target.value);
            }}
          />
        ) : null}
        <DetailList>
          <NotReady
            label={t('services.new.name')}
            value={t('services.new.name.value')}
            reason={t('services.new.name.notReady')}
          />
          <NotReady
            label={t('services.new.replicas')}
            value="1"
            reason={t('services.new.replicas.notReady')}
          />
          <NotReady
            label={t('services.new.engine')}
            value={t(recipeEngine)}
            reason={t('services.new.engine.notReady')}
          />
          <NotReady
            label={t('services.new.autoscaling')}
            value={t('services.new.off')}
            reason={t('services.new.autoscaling.notReady')}
          />
          <NotReady
            label={t('services.new.readOnlyRoot')}
            value={t('services.new.readOnlyRoot.value')}
            reason={t('services.new.readOnlyRoot.notReady')}
          />
          <NotReady
            label={t('services.new.egress')}
            value={t('services.new.egress.value')}
            reason={t('services.new.egress.notReady')}
          />
        </DetailList>
      </div>
    </AdvancedFold>
  );
}

function Plan({
  model,
  modelName,
  draft,
  setDraft,
}: {
  model: string;
  modelName: string;
  draft: Record<string, string>;
  setDraft: (next: Record<string, string | null>) => void;
}) {
  const { t, format } = useI18n();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  // A read of the GPUs takes a moment on the node, so after asking for one the plan follows it
  // until something can take the model.
  const [following, setFollowing] = useState(false);
  const placement = useQuery({
    queryKey: deploymentKeys.placement(model),
    queryFn: ({ signal }) => getPlacement(model, { signal, background: following }),
    refetchInterval: (query) => (following && query.state.data?.recommended == null ? poll : false),
  });
  const [reason, setReason] = useState(() =>
    t('services.new.reason.default', { model: modelName }),
  );
  // One key per request, so a retry after a lost answer cannot deploy twice, while a changed choice
  // is a new request rather than a conflict with the old one.
  const sent = useRef<{ intent: string; key: string } | null>(null);
  const deploy = useMutation({
    mutationFn: (choice: Option) => {
      const trimmed = reason.trim();
      const intent = `${choice.node.node_id}/${choice.gpu.uuid}/${trimmed}`;
      if (sent.current?.intent !== intent) sent.current = { intent, key: crypto.randomUUID() };
      return createDeployment(
        {
          nodeId: choice.node.node_id,
          modelId: model,
          gpuUuid: choice.gpu.uuid,
          reason: trimmed,
        },
        sent.current.key,
      );
    },
    onSuccess: async (created) => {
      sent.current = null;
      await queryClient.invalidateQueries({ queryKey: deploymentsKey });
      void navigate({ to: '/services/$deploymentId', params: { deploymentId: created.id } });
    },
  });
  if (placement.isPending) return <Skeleton className="h-40 w-full" />;
  if (!placement.data) {
    return isProblem(placement.error) ? (
      <ProblemNotice
        problem={placement.error}
        onRetry={() => {
          void placement.refetch();
        }}
      />
    ) : null;
  }
  const options = eligible(placement.data);
  const recommended = placement.data.recommended;
  const chosenKey =
    draft.gpu &&
    options.some((o) => optionKey({ node_id: o.node.node_id, gpu_uuid: o.gpu.uuid }) === draft.gpu)
      ? draft.gpu
      : recommended
        ? optionKey(recommended)
        : '';
  const choice = options.find(
    (o) => optionKey({ node_id: o.node.node_id, gpu_uuid: o.gpu.uuid }) === chosenKey,
  );
  const onRead = () => {
    setFollowing(true);
  };
  if (!choice) {
    return (
      <Section title={t('services.new.where')}>
        <Notice title={t('services.new.none.title')} className="mb-3.5">
          {placement.data.candidates.length === 0
            ? t('services.new.none.unobserved')
            : t('services.new.none.detail')}
        </Notice>
        {placement.data.candidates.length === 0 ? (
          <ReadAnyNode onRead={onRead} />
        ) : (
          <Reasons placement={placement.data} onRead={onRead} />
        )}
      </Section>
    );
  }
  const left = options.filter((o) => o.node.node_id !== choice.node.node_id).length;
  const sameNode = choice.node.gpus.length - 1;
  const isRecommended = recommended !== null && optionKey(recommended) === chosenKey;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (reason.trim() !== '' && !deploy.isPending) deploy.mutate(choice);
  };
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      <Section title={t('services.new.where')}>
        <p className="m-0 mb-3 text-[14px]">
          {t(isRecommended ? 'services.new.recommendation' : 'services.new.chosen', {
            gpu: choice.gpu.name,
            node: choice.node.name,
            free: format.bytes(choice.gpu.memory_free_mib * mebibyte),
          })}
        </p>
        <Advanced
          options={options}
          chosen={chosenKey}
          onChoose={(value) => {
            setDraft({ gpu: value });
          }}
        />
      </Section>
      <Section title={t('services.new.cost')}>
        <DetailList>
          <Detail label={t('services.new.cost.gpu')}>
            {t('services.new.cost.gpu.value', {
              gpu: choice.gpu.name,
              memory: format.bytes(choice.gpu.memory_total_mib * mebibyte),
            })}
          </Detail>
          <Detail label={t('services.new.cost.node')}>
            {sameNode > 0
              ? t('services.new.cost.node.value', { node: choice.node.name, count: sameNode })
              : t('services.new.cost.node.only', { node: choice.node.name })}
          </Detail>
          <Detail label={t('services.new.cost.left')}>
            {t('services.new.cost.left.value', { count: left })}
          </Detail>
        </DetailList>
      </Section>
      <TextField
        label={t('services.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        value={reason}
        error={reason.trim() === '' ? t('services.reason.required') : undefined}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {isProblem(deploy.error) ? (
        <ProblemNotice
          problem={deploy.error}
          onRetry={() => {
            deploy.mutate(choice);
          }}
        />
      ) : null}
      <div>
        <Button type="submit" variant="primary" disabled={reason.trim() === '' || deploy.isPending}>
          <Rocket aria-hidden />
          {t('services.new.submit')}
        </Button>
      </div>
    </form>
  );
}

/** `/services/new`: deploy a model of the workspace in the switcher. */
export function DeployPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const workspace = workspaces.active?.id;
  const [draft, setDraft] = useDraft();
  const models = useQuery({
    ...modelKeys.list(workspace ?? ''),
    queryFn: ({ signal }) => listModels(workspace ?? '', { signal }),
    enabled: workspace !== undefined,
  });
  const items = models.data ?? [];
  const model = items.find((item) => item.id === draft.model) ?? items[0];
  let body;
  if (!workspace || models.isPending) {
    body = <Skeleton className="h-40 w-full" />;
  } else if (!models.data) {
    body = isProblem(models.error) ? (
      <ProblemNotice
        problem={models.error}
        onRetry={() => {
          void models.refetch();
        }}
      />
    ) : null;
  } else if (!model) {
    body = (
      <Card>
        <EmptyState
          icon={Boxes}
          title={t('services.new.noModels.title')}
          action={
            <Link to="/library" className={buttonVariants({ variant: 'primary' })}>
              {t('services.new.noModels.open')}
            </Link>
          }
        >
          {t('services.new.noModels.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <div className="flex max-w-[720px] flex-col gap-4">
        <Section title={t('services.new.model')}>
          <SelectField
            label={t('services.new.model.field')}
            value={model.id}
            options={items.map((item) => ({ value: item.id, label: item.name }))}
            onChange={(event) => {
              setDraft({ model: event.target.value, gpu: null });
            }}
          />
        </Section>
        <Plan
          key={model.id}
          model={model.id}
          modelName={model.name}
          draft={draft}
          setDraft={setDraft}
        />
      </div>
    );
  }
  return (
    <>
      <Link
        to="/services"
        className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
      >
        <ArrowLeft aria-hidden className="size-4" />
        {t('services.page.back')}
      </Link>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('services.new.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">
        {workspaces.active
          ? t('services.new.subtitle', { workspace: workspaces.active.name })
          : t('services.subtitle.none')}
      </p>
      {body}
    </>
  );
}
