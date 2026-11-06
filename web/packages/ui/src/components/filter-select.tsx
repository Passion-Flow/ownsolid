import type { ComponentProps } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link FilterSelect}. */
export interface FilterSelectProps extends Omit<
  ComponentProps<'select'>,
  'children' | 'aria-label'
> {
  /** Accessible name; the options say what the toolbar shows. */
  label: string;
  options: readonly { value: string; label: string }[];
}

/** A choice in a list's toolbar, where a visible label would take the room of the options. */
export function FilterSelect({ label, options, className, ...props }: FilterSelectProps) {
  return (
    <select
      aria-label={label}
      className={cn(
        'h-9 cursor-pointer rounded-field border border-border-strong bg-surface px-3 text-text outline-none',
        'focus:border-accent focus:shadow-[0_0_0_3px_var(--accent-soft)]',
        className,
      )}
      {...props}
    >
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}
