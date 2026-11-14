import { Search } from 'lucide-react';
import type { ComponentProps } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link SearchField}. */
export interface SearchFieldProps extends Omit<ComponentProps<'input'>, 'type' | 'aria-label'> {
  /** Accessible name; the placeholder alone is not one. */
  label: string;
}

/** The text filter of a list's toolbar. */
export function SearchField({ label, className, ...props }: SearchFieldProps) {
  return (
    <div
      className={cn(
        'flex h-9 items-center gap-2 rounded-field border border-border-strong bg-surface px-3 text-text-3',
        'focus-within:border-accent focus-within:shadow-[0_0_0_3px_var(--accent-soft)]',
        className,
      )}
    >
      <Search aria-hidden className="size-4 shrink-0" />
      <input
        type="search"
        aria-label={label}
        className="w-full min-w-0 border-0 bg-transparent text-text outline-none placeholder:text-text-3"
        {...props}
      />
    </div>
  );
}
