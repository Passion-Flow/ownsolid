// A node's preview over the list. Its own page holds the rest (docs/research/resource-detail-pages.md).
import { isProblem, type Node } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Drawer, ProblemNotice } from '@ownsolid/ui';
import { Link } from '@tanstack/react-router';
import { ArrowRight } from 'lucide-react';
import { useNodeActions } from './node-actions.tsx';
import { NodeFacts, QuietNotice, RecentTasks, SectionTitle } from './node-details.tsx';

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
  const { t } = useI18n();
  // The toast sits behind a modal, so its undo is out of reach until the drawer closes.
  const actions = useNodeActions(node, { onChanged: onClose, onRemoved: onClose });
  return (
    <>
      <Drawer
        open={node !== null || Boolean(problem)}
        onClose={onClose}
        title={node ? node.name : t('nodes.detail.missing')}
        footer={actions.buttons ?? undefined}
      >
        {isProblem(problem) ? (
          <ProblemNotice className="mb-4" problem={problem} onRetry={onRetry} />
        ) : null}
        {actions.notices}
        {node ? (
          <>
            <Link
              to="/nodes/$nodeId"
              params={{ nodeId: node.id }}
              className="mb-4 inline-flex items-center gap-1.5 font-medium text-accent-ink hover:underline"
            >
              {t('resource.openPage')}
              <ArrowRight aria-hidden className="size-4" />
            </Link>
            <QuietNotice node={node} />
            <NodeFacts node={node} />
            <SectionTitle>{t('nodes.detail.tasks')}</SectionTitle>
            {/* Beside the task it started: a toast would sit behind this modal drawer. */}
            <p role="status" className="m-0 text-text-2 empty:hidden">
              {actions.collected ? t('nodes.collect.started') : null}
            </p>
            <RecentTasks node={node} />
          </>
        ) : null}
      </Drawer>
      {actions.dialogs}
    </>
  );
}
