import type { ReactNode } from 'react';
import { cn } from '../cn.ts';

/** What a status means. Colors are reserved for state and always come with a label. */
export type StatusTone = 'good' | 'warning' | 'critical' | 'live' | 'neutral';

const dots: Record<StatusTone, string> = {
  good: 'bg-good',
  warning: 'bg-warning',
  critical: 'bg-critical',
  live: 'animate-pulse bg-accent',
  neutral: 'bg-text-3',
};

/** A labelled status, such as "Online" or "Degraded". */
export function StatusPill({ tone, children }: { tone: StatusTone; children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-[13px] font-[550] whitespace-nowrap text-text">
      <i aria-hidden className={cn('size-[7px] shrink-0 rounded-full', dots[tone])} />
      {children}
    </span>
  );
}

/** Props for {@link Tag}. */
export interface TagProps {
  /** `accent` draws attention; the default is neutral. */
  tone?: 'neutral' | 'accent' | undefined;
  children: ReactNode;
}

/** A small label for a category or attribute. */
export function Tag({ tone = 'neutral', children }: TagProps) {
  return (
    <span
      className={cn(
        'inline-flex h-[22px] items-center rounded-[6px] px-2 text-[12px] whitespace-nowrap',
        tone === 'accent' ? 'bg-accent-soft text-accent-ink' : 'bg-surface-2 text-text-2',
      )}
    >
      {children}
    </span>
  );
}

/** A keyboard key or shortcut hint. */
export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded-[5px] border border-b-2 border-border bg-surface-2 px-[5px] font-sans text-[11px] leading-[17px] text-text-3">
      {children}
    </kbd>
  );
}
