import { cn } from '../cn.ts';

/** Props for {@link Meter}. */
export interface MeterProps {
  /** Filled share, from 0 to 100. Values outside are clamped. */
  value: number;
  /** Accessible name, such as "CPU usage". */
  label: string;
  /** Uses the accent color instead of the neutral fill. */
  accent?: boolean | undefined;
  className?: string | undefined;
}

/** A small usage bar. */
export function Meter({ value, label, accent = false, className }: MeterProps) {
  const percent = Math.min(100, Math.max(0, value));
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuenow={percent}
      aria-valuemin={0}
      aria-valuemax={100}
      className={cn('h-1.5 flex-1 overflow-hidden rounded-[3px] bg-surface-3', className)}
    >
      <div
        className={cn('h-full rounded-[3px]', accent ? 'bg-accent' : 'bg-text-3')}
        style={{ width: `${percent}%` }}
      />
    </div>
  );
}
