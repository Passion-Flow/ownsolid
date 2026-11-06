// One task: its steps with their durations and its log, live while it runs. Leaving this page
// leaves the task running on its node (docs/research/long-task-pattern.md).
import {
  collectNodeFacts,
  getTask,
  getTaskLog,
  isProblem,
  taskFinished,
  type TaskDetail,
  type TaskLogLine,
  type TaskStep,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  CopyText,
  LiveLog,
  Notice,
  ProblemNotice,
  Skeleton,
  StatusPill,
  StepTimeline,
  type LiveLogLine,
  type StepState,
  type TimelineStep,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, getRouteApi, useRouter } from '@tanstack/react-router';
import { ArrowLeft, RotateCw } from 'lucide-react';
import { useEffect, useMemo, useRef, type ReactNode } from 'react';
import { operations, states, stepNames, taskPath } from './task-names.ts';

const route = getRouteApi('/authenticated/frame/tasks/$taskId');

// The most lines one read asks for, which the contract allows.
const logPageSize = 500;

// The reasons a task can give. Any other code shows as itself under a general reason.
const reasons: Record<string, TranslationKey> = {
  DISCOVERY_FAILED: 'tasks.reason.DISCOVERY_FAILED',
  TASK_DEADLINE_EXCEEDED: 'tasks.reason.TASK_DEADLINE_EXCEEDED',
};

interface LoadedLog {
  lines: readonly TaskLogLine[];
  cursor: string | undefined;
}

const stepStates: Record<TaskStep['state'], StepState> = {
  running: 'running',
  succeeded: 'done',
  failed: 'failed',
};

function seconds(from: string, to: string): number {
  return Math.max(0, (new Date(to).getTime() - new Date(from).getTime()) / 1_000);
}

function useTaskLog(id: string, live: boolean, poll: number | false) {
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => ['tasks', 'log', id] as const, [id]);
  const log = useQuery({
    queryKey,
    // Each read continues from what is already here, so a long log is fetched once.
    queryFn: async ({ signal }): Promise<LoadedLog> => {
      const known = queryClient.getQueryData<LoadedLog>(queryKey);
      let lines = known?.lines ?? [];
      let cursor = known?.cursor;
      for (;;) {
        const page = await getTaskLog(
          id,
          { limit: logPageSize, after: cursor },
          { signal, background: true },
        );
        lines = [...lines, ...page.items];
        cursor = page.cursor;
        if (page.items.length < logPageSize) return { lines, cursor };
      }
    },
    structuralSharing: false,
    refetchInterval: live ? poll : false,
  });
  // The agent sends its last lines before the result, so one read after the finish has them.
  const wasLive = useRef(live);
  const { refetch } = log;
  useEffect(() => {
    if (wasLive.current && !live) void refetch();
    wasLive.current = live;
  }, [live, refetch]);
  return log;
}

function Timeline({ detail }: { detail: TaskDetail }) {
  const { t, format } = useI18n();
  const { task } = detail;
  const steps: TimelineStep[] = detail.steps.map((step) => ({
    id: step.step,
    title: t(stepNames[step.step]),
    state: stepStates[step.state],
    duration: step.finished_at
      ? format.duration(seconds(step.started_at, step.finished_at))
      : undefined,
  }));
  if (steps.length > 0) return <StepTimeline label={t('tasks.detail.steps')} steps={steps} />;
  const waiting: Partial<Record<TaskDetail['task']['state'], TranslationKey>> = {
    pending: 'tasks.detail.waiting',
    running: 'tasks.detail.started',
  };
  return <p className="m-0 text-text-2">{t(waiting[task.state] ?? 'tasks.detail.noSteps')}</p>;
}

function Facts({ detail }: { detail: TaskDetail }) {
  const { t, format } = useI18n();
  const { task } = detail;
  const rows: [TranslationKey, ReactNode][] = [
    ['tasks.column.node', task.node_name],
    ['tasks.column.started', format.dateTime(task.created_at)],
    ['tasks.column.finished', task.completed_at ? format.dateTime(task.completed_at) : '-'],
    [
      'tasks.detail.took',
      task.completed_at ? format.duration(seconds(task.created_at, task.completed_at)) : '-',
    ],
    ['tasks.detail.attempt', format.number(task.attempt)],
    ['tasks.detail.id', <CopyText key="id" text={task.id} />],
  ];
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-text-3">{t(label)}</dt>
          <dd className="m-0 min-w-0 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function RunAgain({ detail }: { detail: TaskDetail }) {
  const { t } = useI18n();
  const router = useRouter();
  const queryClient = useQueryClient();
  const key = useRef<string | null>(null);
  const again = useMutation({
    mutationFn: () => {
      key.current ??= crypto.randomUUID();
      return collectNodeFacts(detail.task.node_id, key.current);
    },
    onSuccess: (task) => {
      key.current = null;
      void queryClient.invalidateQueries({ queryKey: ['tasks'] });
      void router.navigate({ href: taskPath(task.id) });
    },
  });
  return (
    <>
      <Button
        disabled={again.isPending}
        onClick={() => {
          again.mutate();
        }}
      >
        <RotateCw aria-hidden />
        {t('tasks.detail.again')}
      </Button>
      {again.error && isProblem(again.error) ? (
        <ProblemNotice
          className="mt-3 w-full"
          problem={again.error}
          onRetry={() => {
            again.mutate();
          }}
        />
      ) : null}
    </>
  );
}

/** Shows one task, live while it runs. */
export function TaskPage() {
  const { t, format } = useI18n();
  const { taskId } = route.useParams();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const detail = useQuery({
    queryKey: ['tasks', 'detail', taskId],
    queryFn: ({ signal }) => getTask(taskId, { signal, background: true }),
    refetchInterval: (query) => {
      const data = query.state.data;
      return data && taskFinished(data.task.state) ? false : poll;
    },
  });
  const data = detail.data;
  const live = data ? !taskFinished(data.task.state) : false;
  const log = useTaskLog(taskId, live, poll);
  const lines = useMemo<LiveLogLine[]>(() => {
    const out: LiveLogLine[] = [];
    let attempt = 0;
    for (const line of log.data?.lines ?? []) {
      const time = format.time(line.at);
      if (attempt !== 0 && line.attempt !== attempt) {
        out.push({
          id: `attempt-${String(line.attempt)}`,
          time,
          level: 'info',
          message: t('tasks.detail.attemptStarted', { attempt: format.number(line.attempt) }),
          marker: true,
        });
      }
      attempt = line.attempt;
      out.push({
        id: `${String(line.attempt)}.${String(line.sequence)}`,
        time,
        level: line.level,
        message: line.message,
      });
    }
    return out;
  }, [log.data, format, t]);

  const back = (
    <Link
      to="/tasks"
      className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 no-underline hover:text-text"
    >
      <ArrowLeft aria-hidden className="size-3.5" />
      {t('tasks.detail.back')}
    </Link>
  );

  if (detail.isPending) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-[320px] w-full" />
      </div>
    );
  }
  if (!data) {
    return (
      <>
        {back}
        {isProblem(detail.error) ? (
          <ProblemNotice
            problem={detail.error}
            onRetry={() => {
              void detail.refetch();
            }}
          />
        ) : null}
      </>
    );
  }

  const { task } = data;
  const state = states[task.state];
  const name = t(operations[task.operation]);
  const stale = detail.error ?? log.error;
  return (
    <>
      {back}
      <div className="mb-6 flex flex-wrap items-center gap-3">
        <h1 className="m-0 min-w-0 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
          {name}
        </h1>
        <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
        {task.state !== 'pending' &&
        task.state !== 'running' &&
        task.operation === 'collect_facts.v1' ? (
          <div className="ml-auto">
            <RunAgain detail={data} />
          </div>
        ) : null}
      </div>
      {live ? <p className="m-0 mb-4 text-text-2">{t('tasks.detail.continues')}</p> : null}
      {stale && isProblem(stale) ? (
        <ProblemNotice
          className="mb-4"
          problem={stale}
          onRetry={() => {
            void detail.refetch();
            void log.refetch();
          }}
        />
      ) : null}
      {task.state === 'failed' ? (
        <Notice className="mb-4" tone="critical" title={t('tasks.detail.failed')}>
          {t(reasons[task.error_code ?? ''] ?? 'tasks.reason.other')}
          {task.error_code ? (
            <span className="mt-1 block font-mono text-[12px]">{task.error_code}</span>
          ) : null}
        </Notice>
      ) : null}
      <div className="grid gap-5 lg:grid-cols-[2fr_1fr]">
        <Card>
          <CardHeader title={t('tasks.detail.progress')} />
          <CardBody>
            <Timeline detail={data} />
            <div className="mt-5 mb-2 text-[12.5px] font-semibold text-text-3">
              {t('tasks.detail.log')}
            </div>
            <LiveLog
              label={t('tasks.detail.logLabel', { name })}
              lines={lines}
              live={live}
              fileName={`task-${task.id}.log`}
              empty={t(log.isPending ? 'tasks.detail.logLoading' : 'tasks.detail.logEmpty')}
            />
          </CardBody>
        </Card>
        <Card className="self-start">
          <CardHeader title={t('tasks.detail.about')} />
          <CardBody>
            <Facts detail={data} />
          </CardBody>
        </Card>
      </div>
    </>
  );
}
