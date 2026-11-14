import type { LucideIcon } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link EmptyState}. */
export interface EmptyStateProps {
  icon: LucideIcon;
  title: ReactNode;
  /** Why it is empty and what to do next. */
  children: ReactNode;
  /** The action that fills it. */
  action?: ReactNode | undefined;
  className?: string | undefined;
}

/** Shown where a list or page has nothing yet; it always names the next action. */
export function EmptyState({ icon: Icon, title, children, action, className }: EmptyStateProps) {
  return (
    <div className={cn('px-6 py-14 text-center', className)}>
      <div className="mx-auto mb-3.5 grid size-12 place-items-center rounded-card bg-accent-soft text-accent-ink">
        <Icon aria-hidden />
      </div>
      <h3 className="m-0 mb-1.5 text-base font-semibold">{title}</h3>
      <div className="mx-auto mb-4 max-w-[420px] text-text-2">{children}</div>
      {action}
    </div>
  );
}

/** A placeholder shaped like the content that is loading. */
export function Skeleton({ className }: { className?: string | undefined }) {
  return (
    <div
      aria-hidden
      className={`animate-pulse rounded-control bg-surface-2 ${className ?? 'h-4 w-full'}`}
    />
  );
}
