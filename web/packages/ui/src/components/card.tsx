import type { ComponentProps, ReactNode } from 'react';
import { cn } from '../cn.ts';

/**
 * A surface that groups related content. It contains what is positioned inside it, so the
 * visually hidden text of a table that scrolls in a card cannot widen the page.
 */
export function Card({ className, ...props }: ComponentProps<'section'>) {
  return (
    <section
      className={cn('relative rounded-card border border-border bg-surface shadow-1', className)}
      {...props}
    />
  );
}

/** Props for {@link CardHeader}. */
export interface CardHeaderProps {
  title: ReactNode;
  /** Short context next to the title. */
  subtitle?: ReactNode | undefined;
  /** Controls at the right edge. */
  actions?: ReactNode | undefined;
}

/** A card's title row. */
export function CardHeader({ title, subtitle, actions }: CardHeaderProps) {
  return (
    <div className="flex items-center gap-2.5 px-5 pt-4 pb-0.5">
      <h3 className="m-0 text-[15px] font-[650] tracking-[-0.01em]">{title}</h3>
      {subtitle ? <span className="text-[12.5px] text-text-3">{subtitle}</span> : null}
      {actions ? <div className="ml-auto">{actions}</div> : null}
    </div>
  );
}

/** A card's content area. */
export function CardBody({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('px-5 pt-3 pb-[18px]', className)} {...props} />;
}
