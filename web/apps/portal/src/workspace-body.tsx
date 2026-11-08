// The part of a workspace page that depends on which workspace is chosen: waiting for the list,
// the failure to read it, and what to do without one.
import { isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import type { Workspaces } from '@ownsolid/shell';
import { Card, EmptyState, ProblemNotice, Skeleton } from '@ownsolid/ui';
import { UsersRound } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';

const skeletonDelayMilliseconds = 300;

/** Delays a workspace placeholder so fast reads do not flash it on screen. */
export function WorkspaceSkeleton({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => {
      setVisible(true);
    }, skeletonDelayMilliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, []);
  if (!visible) return null;
  return (
    <div role="status">
      <span className="sr-only">{t('status.loading')}</span>
      {children}
    </div>
  );
}

/** Props for {@link WorkspaceBody}. */
export interface WorkspaceBodyProps {
  workspaces: Workspaces;
  /** What a person without a workspace can do about it. */
  noWorkspace: string;
  /** A placeholder shaped like the page it stands in for. */
  loading?: ReactNode;
  /** The page, for the chosen workspace. */
  children: (workspace: string) => ReactNode;
}

/** Renders a page for the workspace in the switcher, or what stands in its way. */
export function WorkspaceBody({ workspaces, noWorkspace, loading, children }: WorkspaceBodyProps) {
  const { t } = useI18n();
  if (workspaces.isPending) {
    // A workspace chosen earlier is known only once the list has loaded.
    return (
      <WorkspaceSkeleton>{loading ?? <Skeleton className="h-[320px] w-full" />}</WorkspaceSkeleton>
    );
  }
  if (workspaces.active) return children(workspaces.active.id);
  if (isProblem(workspaces.error)) {
    return <ProblemNotice problem={workspaces.error} onRetry={workspaces.retry} />;
  }
  return (
    <Card>
      <EmptyState icon={UsersRound} title={t('home.noWorkspace')}>
        {noWorkspace}
      </EmptyState>
    </Card>
  );
}
