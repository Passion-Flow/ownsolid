// The steps of a long task (design-system long-running tasks).
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { Check, Minus, X } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Where a step of a task stands. */
export type StepState = 'pending' | 'running' | 'done' | 'failed' | 'cancelled';

/** One step of a {@link StepTimeline}. */
export interface TimelineStep {
  id: string;
  title: string;
  /** One line under the title. */
  detail?: ReactNode | undefined;
  state: StepState;
  /** How long it took; a running step says so by itself. */
  duration?: string | undefined;
}

/** Props for {@link StepTimeline}. */
export interface StepTimelineProps {
  /** Names the list for screen readers. */
  label: string;
  steps: readonly TimelineStep[];
  className?: string | undefined;
}

const stateLabels: Record<StepState, TranslationKey> = {
  pending: 'timeline.state.pending',
  running: 'timeline.state.running',
  done: 'timeline.state.done',
  failed: 'timeline.state.failed',
  cancelled: 'timeline.state.cancelled',
};

const dots: Record<StepState, string> = {
  pending: 'border-border-strong bg-surface text-text-3',
  running: 'border-accent bg-surface text-accent shadow-[0_0_0_4px_var(--accent-soft)]',
  done: 'border-accent bg-accent text-white',
  failed: 'border-critical bg-critical text-white',
  cancelled: 'border-border-strong bg-surface-2 text-text-2',
};

/** Steps in order, each with its state and how long it took. */
export function StepTimeline({ label, steps, className }: StepTimelineProps) {
  const { t } = useI18n();
  return (
    <ol aria-label={label} className={cn('m-0 list-none p-0', className)}>
      {steps.map((step, index) => (
        <li
          key={step.id}
          aria-current={step.state === 'running' ? 'step' : undefined}
          className="relative grid grid-cols-[28px_1fr_auto] gap-3 pb-4 last:pb-0"
        >
          {index < steps.length - 1 ? (
            <span
              aria-hidden
              className={cn(
                'absolute top-7 bottom-0.5 left-[13px] w-0.5',
                step.state === 'done' ? 'bg-accent' : 'bg-border',
              )}
            />
          ) : null}
          <span
            aria-hidden
            className={cn(
              'grid size-7 place-items-center rounded-full border-2 text-[12px] font-semibold',
              dots[step.state],
            )}
          >
            {step.state === 'done' ? <Check className="size-3.5 stroke-[2.6]" /> : null}
            {step.state === 'failed' ? <X className="size-3.5 stroke-[2.6]" /> : null}
            {step.state === 'cancelled' ? <Minus className="size-3.5 stroke-[2.6]" /> : null}
            {step.state === 'pending' || step.state === 'running' ? index + 1 : null}
          </span>
          <div className="min-w-0">
            <div className="pt-[3px] font-semibold">{step.title}</div>
            {step.detail ? <div className="text-[12.5px] text-text-3">{step.detail}</div> : null}
          </div>
          <div className="pt-1 text-[12px] whitespace-nowrap text-text-3">
            {step.state === 'running' ||
            step.state === 'pending' ||
            step.state === 'cancelled' ||
            !step.duration ? (
              <span className={step.state === 'pending' ? 'sr-only' : undefined}>
                {t(stateLabels[step.state])}
              </span>
            ) : (
              <>
                <span className="sr-only">{t(stateLabels[step.state])} </span>
                {step.duration}
              </>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}
