// What the console shows about a node, in its drawer and on its own page.
import { isProblem, listNodeTasks, type Node, type NodeTask } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery, useWorkspaces } from '@ownsolid/shell';
import {
  CopyText,
  Detail,
  DetailList,
  Notice,
  ProblemNotice,
  Skeleton,
  StatusPill,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { nodeState, profiles } from './node-state.ts';
import { operations, states as taskStates } from './task-names.ts';

const shownTasks = 5;

export function SectionTitle({ children }: { children: string }) {
  return <h3 className="mt-6 mb-2.5 text-[13px] font-semibold text-text-2">{children}</h3>;
}

/** Why a node has gone quiet and what to look at, while it is meant to be working. */

export function QuietNotice({ node }: { node: Node }) {
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

/** A node's tasks, newest first. */
export function NodeTaskRows({ tasks }: { tasks: readonly NodeTask[] }) {
  const { t, format } = useI18n();
  return (
    <ul className="m-0 list-none p-0">
      {tasks.map((task) => {
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

/** The latest few tasks of a node, for the drawer. */
export function RecentTasks({ node }: { node: Node }) {
  const { t } = useI18n();
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
  return <NodeTaskRows tasks={tasks.data.items} />;
}

/** What a node is, and what it reported about itself. */
export function NodeFacts({ node }: { node: Node }) {
  const { t, format } = useI18n();
  const workspaces = useWorkspaces();
  const workspace = workspaces.items.find((item) => item.id === node.workspace_id);
  const state = nodeState(node);
  return (
    <>
      <DetailList>
        <Detail label={t('nodes.column.state')}>
          <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
        </Detail>
        <Detail label={t('nodes.column.seen')}>{format.dateTime(node.last_heartbeat_at)}</Detail>
        <Detail label={t('nodes.detail.workspace')}>
          {workspace ? workspace.name : <CopyText text={node.workspace_id} />}
        </Detail>
        <Detail label={t('nodes.detail.profile')}>{t(profiles[node.execution_profile])}</Detail>
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
    </>
  );
}
