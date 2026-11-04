import { useId, type ComponentProps, type ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link TextField}. */
export interface TextFieldProps extends Omit<ComponentProps<'input'>, 'id'> {
  /** Visible label; also the accessible name. */
  label: ReactNode;
  /** Guidance under the field. */
  hint?: ReactNode | undefined;
  /** Error under the field; marks the input invalid. */
  error?: ReactNode | undefined;
}

/** A labelled single-line input with optional hint and error text. */
export function TextField({ label, hint, error, className, ...props }: TextFieldProps) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [errorId, hintId].filter(Boolean).join(' ') || undefined;
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <label htmlFor={id} className="font-medium text-text-2">
        {label}
      </label>
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={cn(
          'h-9 w-full rounded-field border border-border-strong bg-surface px-3 text-text outline-none',
          'placeholder:text-text-3 focus:border-accent focus:shadow-[0_0_0_3px_var(--accent-soft)]',
          'aria-invalid:border-critical disabled:opacity-55',
        )}
        {...props}
      />
      {error ? (
        <p id={errorId} className="text-[12.5px] text-critical">
          {error}
        </p>
      ) : null}
      {hint ? (
        <p id={hintId} className="text-[12.5px] text-text-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}
