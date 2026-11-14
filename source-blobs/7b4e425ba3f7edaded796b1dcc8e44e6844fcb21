// The part of a workspace page that depends on which workspace is chosen: waiting for the list,
// the failure to read it, and what to do without one.
import { isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import type { Workspaces } from '@ownsolid/shell';
import { Card, EmptyState, ProblemNotice, Skeleton } from '@ownsolid/ui';
import { UsersRound } from 'lucide-react';
import type { ReactNode } from 'react';

/** Props for {@link WorkspaceBody}. */
export interface WorkspaceBodyProps {
  workspaces: Workspaces;
  /** What a person without a workspace can do about it. */
  noWorkspace: string;
  /** The page, for the chosen workspace. */
  children: (workspace: string) => ReactNode;
}

/** Renders a page for the workspace in the switcher, or what stands in its way. */
export function WorkspaceBody({ workspaces, noWorkspace, children }: WorkspaceBodyProps) {
  const { t } = useI18n();
  if (workspaces.isPending) {
    // A workspace chosen earlier is known only once the list has loaded.
    return <Skeleton aria-busy className="h-[320px] w-full" />;
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
