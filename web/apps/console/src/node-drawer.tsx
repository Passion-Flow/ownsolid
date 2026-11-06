// A node's details and what can be done to it. Renaming and disabling happen at once and offer
// undo; removing clears the node's credential for good, so its name is typed first.
import { collectNodeFacts, isProblem, listNodeTasks, updateNode, type Node } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery, useWorkspaces } from '@ownsolid/shell';
import {
  Button,
  CopyText,
  Detail,
  DetailList,
  Dialog,
  Drawer,
  Notice,
  ProblemNotice,
  Skeleton,
  StatusPill,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Ban, Check, Pencil, RefreshCw, Trash2 } from 'lucide-react';
import { useRef, useState, type SubmitEvent } from 'react';
import { nodeState, profiles } from './node-state.ts';
import { operations, states as taskStates } from './task-names.ts';

const shownTasks = 5;

function SectionTitle({ children }: { children: string }) {
  return <h3 className="mt-6 mb-2.5 text-[13px] font-semibold text-text-2">{children}</h3>;
}

/** Why a node has gone quiet and what to look at, while it is meant to be working. */
function QuietNotice({ node }: { node: Node }) {
  const { t, format } = useI18n();
  if (node.status !== 'active' || node.connectivity === 'online') return null;
  return (
    <Notice
      tone={node.connectivity === 'offline' ? 'critical' : 'info'}
      className="mb-4"
      title={t('nodes.detail.quiet', { time: format.dateTime(node.last_heartbeat_at) })}
    >
      {t('nodes.detail.quietNext')}
    </Notice>
  );
}

function RecentTasks({ node }: { node: Node }) {
  const { t, format } = useI18n();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const tasks = useQuery({
    queryKey: ['nodes', node.id, 'tasks'],
    queryFn: ({ signal }) =>
      listNodeTasks(node.id, { limit: shownTasks }, { signal, background: true }),
    refetchInterval: poll,
  });
  if (tasks.isPending) return <Skeleton className="h-10 w-full" />;
  if (!tasks.data) {
    return isProblem(tasks.error) ? (
      <ProblemNotice
        problem={tasks.error}
        onRetry={() => {
          void tasks.refetch();
        }}
      />
    ) : null;
  }
  if (tasks.data.items.length === 0) {
    return <p className="m-0 text-text-3">{t('nodes.detail.noTasks')}</p>;
  }
  return (
    <ul className="m-0 list-none p-0">
      {tasks.data.items.map((task) => {
        const state = taskStates[task.state];
        return (
          <li
            key={task.id}
            className="flex items-center justify-between gap-3 border-b border-border py-2.5 last:border-b-0"
          >
            <div className="min-w-0">
              <Link
                to="/tasks/$taskId"
                params={{ taskId: task.id }}
                className="font-medium text-text hover:underline"
              >
                {t(operations[task.operation])}
              </Link>
              <div className="text-[12.5px] text-text-3">{format.dateTime(task.created_at)}</div>
            </div>
            <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
          </li>
        );
      })}
    </ul>
  );
}

function RenameDialog({
  node,
  onClose,
  onRename,
  pending,
  problem,
}: {
  node: Node;
  onClose: () => void;
  onRename: (name: string) => void;
  pending: boolean;
  problem: unknown;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(node.name);
  const usable = name.trim() !== '' && name.trim() !== node.name;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (usable && !pending) onRename(name.trim());
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('nodes.rename.title', { name: node.name })}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form="rename-node" variant="primary" disabled={!usable || pending}>
            {t('nodes.rename.submit')}
          </Button>
        </>
      }
    >
      <form id="rename-node" className="flex flex-col gap-3.5" onSubmit={submit}>
        {isProblem(problem) ? <ProblemNotice problem={problem} /> : null}
        <TextField
          label={t('nodes.add.name')}
          name="node-name"
          autoComplete="off"
          maxLength={100}
          required
          value={name}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function RemoveDialog({
  node,
  onClose,
  onRemove,
  pending,
  problem,
}: {
  node: Node;
  onClose: () => void;
  onRemove: (reason: string) => void;
  pending: boolean;
  problem: unknown;
}) {
  const { t } = useI18n();
  const [reason, setReason] = useState(t('nodes.remove.reason.default'));
  return (
    <TypedConfirmation
      title={t('nodes.remove.title')}
      consequence={t('nodes.remove.consequence', { name: node.name })}
      expected={node.name}
      confirmLabel={t('nodes.remove.submit')}
      pending={pending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') onRemove(reason.trim());
      }}
    >
      <TextField
        label={t('nodes.remove.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        hint={t('nodes.remove.reason.hint')}
        value={reason}
        error={reason.trim() === '' ? t('nodes.remove.reason.required') : undefined}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {isProblem(problem) ? <ProblemNotice problem={problem} /> : null}
    </TypedConfirmation>
  );
}

/** The drawer for one node. It shows nothing while `node` is null and no `problem` is given. */
export function NodeDrawer({
  node,
  problem,
  onClose,
  onRetry,
}: {
  node: Node | null;
  problem: unknown;
  onClose: () => void;
  onRetry: () => void;
}) {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const workspaces = useWorkspaces();
  const [dialog, setDialog] = useState<'rename' | 'remove' | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['nodes'] });
  const change = useMutation({
    mutationFn: (input: { target: Node; name?: string; status?: Node['status']; reason: string }) =>
      updateNode(input.target.id, {
        name: input.name ?? input.target.name,
        status: input.status ?? input.target.status,
        version: input.target.version,
        reason: input.reason,
      }),
    onSettled: refresh,
  });
  // One key per request, so a retry after a lost answer cannot start a second task.
  const collectKey = useRef<string | null>(null);
  const collect = useMutation({
    mutationFn: (id: string) => {
      collectKey.current ??= crypto.randomUUID();
      return collectNodeFacts(id, collectKey.current);
    },
    onSuccess: () => {
      collectKey.current = null;
      return refresh();
    },
  });

  /** Changes the node and offers to put back what it was. */
  const reversible = (
    target: Node,
    next: { name?: string; status?: Node['status'] },
    reason: string,
    done: string,
  ) => {
    change.mutate(
      { target, ...next, reason },
      {
        onSuccess: (updated) => {
          // The toast sits behind a modal, so its undo is out of reach until the drawer closes.
          setDialog(null);
          onClose();
          toast({
            message: done,
            undo: () =>
              updateNode(updated.id, {
                name: target.name,
                status: target.status,
                version: updated.version,
                reason: t('nodes.reason.undo'),
              }).finally(refresh),
          });
        },
      },
    );
  };

  const workspace = workspaces.items.find((item) => item.id === node?.workspace_id);
  const state = node ? nodeState(node) : null;
  const active = node?.status === 'active';
  const removed = node?.status === 'removed';
  return (
    <>
      <Drawer
        open={node !== null || Boolean(problem)}
        onClose={onClose}
        title={node ? node.name : t('nodes.detail.missing')}
        footer={
          node && !removed ? (
            <>
              <Button
                variant="danger"
                onClick={() => {
                  change.reset();
                  setDialog('remove');
                }}
              >
                <Trash2 aria-hidden />
                {t('nodes.remove.open')}
              </Button>
              <Button
                onClick={() => {
                  change.reset();
                  setDialog('rename');
                }}
              >
                <Pencil aria-hidden />
                {t('nodes.rename.open')}
              </Button>
              <Button
                disabled={change.isPending}
                onClick={() => {
                  if (active) {
                    reversible(
                      node,
                      { status: 'disabled' },
                      t('nodes.reason.disable'),
                      t('nodes.disable.done', { name: node.name }),
                    );
                  } else {
                    reversible(
                      node,
                      { status: 'active' },
                      t('nodes.reason.enable'),
                      t('nodes.enable.done', { name: node.name }),
                    );
                  }
                }}
              >
                {active ? <Ban aria-hidden /> : <Check aria-hidden />}
                {active ? t('nodes.disable.open') : t('nodes.enable.open')}
              </Button>
              {active ? (
                <Button
                  variant="primary"
                  disabled={collect.isPending}
                  onClick={() => {
                    collect.mutate(node.id);
                  }}
                >
                  <RefreshCw aria-hidden />
                  {t('nodes.collect.open')}
                </Button>
              ) : null}
            </>
          ) : undefined
        }
      >
        {isProblem(problem) ? (
          <ProblemNotice className="mb-4" problem={problem} onRetry={onRetry} />
        ) : null}
        {dialog === null && change.error && isProblem(change.error) ? (
          <ProblemNotice className="mb-4" problem={change.error} />
        ) : null}
        {collect.error && isProblem(collect.error) ? (
          <ProblemNotice
            className="mb-4"
            problem={collect.error}
            onRetry={() => {
              if (node) collect.mutate(node.id);
            }}
          />
        ) : null}
        {node && state ? (
          <>
            <QuietNotice node={node} />
            <DetailList>
              <Detail label={t('nodes.column.state')}>
                <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
              </Detail>
              <Detail label={t('nodes.column.seen')}>
                {format.dateTime(node.last_heartbeat_at)}
              </Detail>
              <Detail label={t('nodes.detail.workspace')}>
                {workspace ? workspace.name : <CopyText text={node.workspace_id} />}
              </Detail>
              <Detail label={t('nodes.detail.profile')}>
                {t(profiles[node.execution_profile])}
              </Detail>
              <Detail label={t('nodes.detail.joined')}>{format.dateTime(node.created_at)}</Detail>
              <Detail label={t('nodes.detail.id')}>
                <CopyText text={node.id} />
              </Detail>
            </DetailList>
            <SectionTitle>{t('nodes.detail.system')}</SectionTitle>
            <DetailList>
              <Detail label={t('nodes.detail.hostname')}>
                <CopyText text={node.facts.hostname} />
              </Detail>
              <Detail label={t('nodes.column.system')}>
                {`${node.facts.os_id} ${node.facts.os_version} · ${node.facts.architecture}`}
              </Detail>
              <Detail label={t('nodes.detail.cpu')}>
                {t('nodes.cpus', { count: node.facts.logical_cpus })}
              </Detail>
              <Detail label={t('nodes.detail.memory')}>
                {format.bytes(node.facts.memory_total_bytes)}
              </Detail>
              <Detail label={t('nodes.detail.disk')}>
                {t('nodes.detail.diskValue', {
                  available: format.bytes(node.facts.disk_available_bytes),
                  total: format.bytes(node.facts.disk_total_bytes),
                  path: node.facts.disk_path,
                })}
              </Detail>
              <Detail label={t('nodes.column.agent')}>{node.facts.agent_version}</Detail>
              <Detail label={t('nodes.detail.observed')}>
                {format.dateTime(node.facts.observed_at)}
              </Detail>
            </DetailList>
            <SectionTitle>{t('nodes.detail.tasks')}</SectionTitle>
            {/* Beside the task it started: a toast would sit behind this modal drawer. */}
            <p role="status" className="m-0 text-text-2 empty:hidden">
              {collect.isSuccess ? t('nodes.collect.started') : null}
            </p>
            <RecentTasks node={node} />
          </>
        ) : null}
      </Drawer>
      {/* Beside the drawer, not in it: a modal inside a modal can lose clicks to its parent. */}
      {node && dialog === 'rename' ? (
        <RenameDialog
          node={node}
          pending={change.isPending}
          problem={change.error}
          onClose={() => {
            setDialog(null);
          }}
          onRename={(name) => {
            reversible(node, { name }, t('nodes.reason.rename'), t('nodes.rename.done', { name }));
          }}
        />
      ) : null}
      {node && dialog === 'remove' ? (
        <RemoveDialog
          node={node}
          pending={change.isPending}
          problem={change.error}
          onClose={() => {
            setDialog(null);
          }}
          onRemove={(reason) => {
            change.mutate(
              { target: node, status: 'removed', reason },
              {
                onSuccess: () => {
                  setDialog(null);
                  onClose();
                  toast({ message: t('nodes.remove.done', { name: node.name }) });
                },
              },
            );
          }}
        />
      ) : null}
    </>
  );
}
