// The tasks a person started that are still running. A task runs on its node whatever the
// browser does, so this shows them from any page and says when one finishes
// (docs/research/long-task-pattern.md).
import { getTask, isProblem, listTasks, taskFinished, type TaskSummary } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { Button, Menu, MenuItem, MenuLabel, useToast } from '@ownsolid/ui';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useRouter } from '@tanstack/react-router';
import { LoaderCircle } from 'lucide-react';
import { useEffect } from 'react';
import { useSession } from './session.ts';
import { runtimeConfigQuery } from './routes.tsx';

const storagePrefix = 'ownsolid.tasks.watching.';

// The tray lists at most this many; the task page lists them all.
const shownTasks = 20;

function readWatched(key: string): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) ?? '[]');
    return Array.isArray(value) ? value.filter((id) => typeof id === 'string') : [];
  } catch {
    // Storage can be disabled by browser policy; a finish is then told only in this page view.
    return [];
  }
}

function writeWatched(key: string, ids: readonly string[]) {
  try {
    localStorage.setItem(key, JSON.stringify(ids));
  } catch {
    // The tray still works for this page when storage is unavailable.
  }
}

/** Props for {@link TaskTray}. */
export interface TaskTrayProps {
  /** The page of one task. */
  taskPath: (id: string) => string;
  /** The name of a task. */
  taskName: (task: TaskSummary) => TranslationKey;
}

/** Shows the person's running tasks in the top bar, and tells them when one finishes. */
export function TaskTray({ taskPath, taskName }: TaskTrayProps) {
  const { t, format } = useI18n();
  const toast = useToast();
  const router = useRouter();
  const queryClient = useQueryClient();
  const me = useSession();
  const config = useQuery(runtimeConfigQuery()).data;
  const key = `${storagePrefix}${me?.user.id ?? 'visitor'}`;
  const running = useQuery({
    queryKey: ['tasks', 'mine'],
    queryFn: ({ signal }) =>
      listTasks({ limit: shownTasks, mine: true }, { signal, background: true }),
    refetchInterval: config?.ui.pollMilliseconds ?? false,
  });
  const tasks = running.data?.items;

  // A task that was running and no longer is has finished, even if that happened while the
  // page was closed. Each finish is told once, because the watch list forgets it first.
  useEffect(() => {
    if (!tasks) return;
    const current = new Set(tasks.map((task) => task.id));
    const gone = readWatched(key).filter((id) => !current.has(id));
    writeWatched(key, [...current]);
    if (gone.length === 0) return;
    void queryClient.invalidateQueries({ queryKey: ['tasks'], refetchType: 'active' });
    void Promise.allSettled(gone.map((id) => getTask(id, { background: true }))).then((reads) => {
      const finished = reads.flatMap((read) =>
        read.status === 'fulfilled' && taskFinished(read.value.task.state) ? [read.value.task] : [],
      );
      const [first] = finished;
      if (!first) return;
      if (finished.length > 1) {
        toast({ message: t('tasks.tray.finishedMany', { count: format.number(finished.length) }) });
        return;
      }
      const name = t(taskName(first));
      const message =
        first.state === 'succeeded'
          ? t('tasks.tray.succeeded', { name })
          : t('tasks.tray.notSucceeded', { name });
      toast({
        message,
        action: {
          label: t('tasks.tray.open'),
          run: () => {
            void router.navigate({ href: taskPath(first.id) });
          },
        },
      });
    });
  }, [tasks, key, queryClient, toast, t, format, router, taskPath, taskName]);

  if (running.error && isProblem(running.error)) return null;
  if (!tasks || tasks.length === 0) return null;
  const count = format.number(tasks.length);
  return (
    <Menu
      trigger={
        <Button variant="ghost" size="sm" aria-label={t('tasks.tray.label', { count })}>
          <LoaderCircle aria-hidden className="animate-spin" />
          <span aria-hidden>{count}</span>
        </Button>
      }
    >
      <MenuLabel>{t('tasks.tray.title')}</MenuLabel>
      {tasks.map((task) => (
        <MenuItem key={task.id} asChild>
          <Link to={taskPath(task.id)} className="no-underline">
            <span className="truncate">{t(taskName(task))}</span>
            <span className="ml-auto pl-3 text-[12px] text-text-3">{task.node_name}</span>
          </Link>
        </MenuItem>
      ))}
    </Menu>
  );
}
