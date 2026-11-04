import { LoaderCircle } from 'lucide-react';
import { cn } from '../cn.ts';

/** A loading indicator with an accessible label. */
export function Spinner({ label, className }: { label: string; className?: string | undefined }) {
  return (
    <span role="status" className={cn('inline-flex items-center gap-2 text-text-3', className)}>
      <LoaderCircle aria-hidden className="size-[18px] animate-spin" strokeWidth={1.75} />
      <span className="sr-only">{label}</span>
    </span>
  );
}
