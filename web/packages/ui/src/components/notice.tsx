import { CircleAlert, Info } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link Notice}. */
export interface NoticeProps {
  /** `critical` for failures, announced to screen readers at once; `info` otherwise. */
  tone?: 'info' | 'critical' | undefined;
  /** What happened. */
  title: ReactNode;
  /** The title's element: a heading when the notice is all the page shows. */
  titleAs?: 'p' | 'h1' | 'h2' | undefined;
  /** Likely cause and next step. */
  children?: ReactNode | undefined;
  /** Actions that fix the problem, such as a retry button. */
  actions?: ReactNode | undefined;
  className?: string | undefined;
}

/** An inline message. Every problem names its next step (design-system principle 2). */
export function Notice({
  tone = 'info',
  title,
  titleAs: Title = 'p',
  children,
  actions,
  className,
}: NoticeProps) {
  const Icon = tone === 'critical' ? CircleAlert : Info;
  return (
    <div
      role={tone === 'critical' ? 'alert' : 'status'}
      className={cn(
        'flex gap-2.5 rounded-[12px] px-3.5 py-[11px] text-[13px] text-text-2',
        tone === 'critical'
          ? 'bg-[color-mix(in_srgb,var(--critical)_10%,var(--surface))]'
          : 'bg-surface-2',
        className,
      )}
    >
      <Icon
        aria-hidden
        className={cn(
          'mt-0.5 size-4 shrink-0',
          tone === 'critical' ? 'text-critical' : 'text-accent',
        )}
      />
      <div className="min-w-0 flex-1">
        <Title className="font-semibold text-text">{title}</Title>
        {children ? <div className="mt-0.5">{children}</div> : null}
        {actions ? <div className="mt-2 flex flex-wrap gap-1.5">{actions}</div> : null}
      </div>
    </div>
  );
}
