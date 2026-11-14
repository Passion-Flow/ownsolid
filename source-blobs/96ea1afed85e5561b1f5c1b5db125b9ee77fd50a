import { cn } from '../cn.ts';

/** The ownsolid mark and word mark. The product name is not translated. */
export function Brand({
  className,
  markOnly = false,
}: {
  className?: string | undefined;
  /** Shows only the mark, and keeps the name for assistive technology. */
  markOnly?: boolean;
}) {
  return (
    <div className={cn('flex items-center gap-2.5', className)}>
      <span aria-hidden className="grid size-7 place-items-center rounded-control bg-text">
        <span className="size-[11px] rotate-45 rounded-[3px] bg-accent" />
      </span>
      <span className={markOnly ? 'sr-only' : 'text-[17px] font-bold tracking-[-0.02em]'}>
        {'ownsolid'}
      </span>
    </div>
  );
}
