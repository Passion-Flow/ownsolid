import { useId, type ComponentProps, type ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link SelectField}. */
export interface SelectFieldProps extends Omit<ComponentProps<'select'>, 'id' | 'children'> {
  /** Visible label; also the accessible name. */
  label: ReactNode;
  /** Guidance under the field. */
  hint?: ReactNode | undefined;
  /** Choices to display. A disabled choice remains visible in the list. */
  options: readonly { value: string; label: string; disabled?: boolean }[];
}

/** A labelled choice among a few options, using the browser's own list. */
export function SelectField({ label, hint, options, className, ...props }: SelectFieldProps) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <label htmlFor={id} className="font-medium text-text-2">
        {label}
      </label>
      <select
        id={id}
        aria-describedby={hintId}
        className={cn(
          'h-9 w-full cursor-pointer rounded-field border border-border-strong bg-surface px-3 text-text outline-none',
          'focus:border-accent focus:shadow-[0_0_0_3px_var(--accent-soft)] disabled:opacity-55',
        )}
        {...props}
      >
        {options.map((option) => (
          <option
            key={option.value}
            value={option.value}
            disabled={option.disabled}
            aria-disabled={option.disabled}
          >
            {option.label}
          </option>
        ))}
      </select>
      {hint ? (
        <p id={hintId} className="text-[12.5px] text-text-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}
