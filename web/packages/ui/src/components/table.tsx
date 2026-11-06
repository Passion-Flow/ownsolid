import type { ComponentProps } from 'react';
import { cn } from '../cn.ts';

/** A data table. Row height follows the density setting. */
export function Table({ className, ...props }: ComponentProps<'table'>) {
  return <table className={cn('w-full border-separate border-spacing-0', className)} {...props} />;
}

/** A column heading. */
export function Th({ className, ...props }: ComponentProps<'th'>) {
  return (
    <th
      scope="col"
      className={cn(
        'h-10 border-b border-border bg-surface px-3.5 text-left text-[12px] font-semibold whitespace-nowrap text-text-3',
        className,
      )}
      {...props}
    />
  );
}

/** A table row with hover and selected states. */
export function Tr({ className, ...props }: ComponentProps<'tr'>) {
  return (
    <tr
      className={cn(
        'transition-colors duration-100 hover:[&>td]:bg-surface-2 data-[selected]:[&>td]:bg-accent-soft [&:last-child>td]:border-b-0',
        className,
      )}
      {...props}
    />
  );
}

/** A table cell. Numbers use tabular figures. */
export function Td({ className, ...props }: ComponentProps<'td'>) {
  return (
    <td
      className={cn(
        'h-row border-b border-border px-3.5 whitespace-nowrap tabular-nums',
        className,
      )}
      {...props}
    />
  );
}
