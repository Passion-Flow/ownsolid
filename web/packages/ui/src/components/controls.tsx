// Small form controls: segmented choice, switch and checkbox.
import type { ComponentProps, ReactNode } from 'react';
import { cn } from '../cn.ts';

/** One choice in a {@link Segmented} control. */
export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
}

/** Props for {@link Segmented}. */
export interface SegmentedProps<T extends string> {
  /** Accessible name of the group. */
  label: string;
  value: T;
  options: readonly SegmentedOption<T>[];
  onChange: (value: T) => void;
  className?: string | undefined;
}

/** A compact choice between a few views or modes. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: SegmentedProps<T>) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn(
        'inline-flex gap-0.5 rounded-field border border-border bg-surface-2 p-[3px]',
        className,
      )}
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          onClick={() => {
            onChange(option.value);
          }}
          className={cn(
            'h-7 cursor-pointer rounded-[7px] border-0 bg-transparent px-3 text-[13px] font-medium whitespace-nowrap text-text-2',
            option.value === value && 'bg-surface text-text shadow-1',
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

/** Props for {@link Switch}. */
export interface SwitchProps {
  /** Accessible name. */
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean | undefined;
}

/** An on or off setting that applies at once. */
export function Switch({ label, checked, onChange, disabled }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => {
        onChange(!checked);
      }}
      className={cn(
        'relative h-[22px] w-[38px] cursor-pointer rounded-[11px] border-0 transition-colors disabled:opacity-55',
        checked ? 'bg-accent' : 'bg-surface-3',
      )}
    >
      <span
        aria-hidden
        className={cn(
          'absolute top-[3px] size-4 rounded-full bg-white transition-[left] duration-200 ease-standard',
          checked ? 'left-[19px]' : 'left-[3px]',
        )}
      />
    </button>
  );
}

/** A checkbox in the accent color. Give it an accessible name. */
export function Checkbox({ className, ...props }: Omit<ComponentProps<'input'>, 'type'>) {
  return (
    <input
      type="checkbox"
      className={cn('m-0 size-4 cursor-pointer accent-accent', className)}
      {...props}
    />
  );
}
