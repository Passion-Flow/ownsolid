import { ChevronRight } from 'lucide-react';
import { useState, type ReactNode } from 'react';

/** Props for {@link AdvancedFold}. */
export interface AdvancedFoldProps {
  /** Says what is behind it and how much, such as "Advanced settings (3)". */
  label: string;
  /** Something inside needs attention, so it opens and cannot close over the problem. */
  invalid?: boolean;
  children: ReactNode;
}

/**
 * The one fold a form may have: what has a default that works goes behind it (ADR 0037). Anything without a
 * safe default, that costs money or capacity, or that cannot be undone stays outside.
 */
export function AdvancedFold({ label, invalid = false, children }: AdvancedFoldProps) {
  const [chosen, setChosen] = useState(false);
  return (
    <details
      open={chosen || invalid}
      onToggle={(event) => {
        setChosen(event.currentTarget.open);
      }}
      className="group/fold"
    >
      <summary
        onClick={(event) => {
          // The browser closes it on its own, whatever `open` says.
          if (invalid) event.preventDefault();
        }}
        className="flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-control text-[13px] font-medium text-text-2 hover:text-text [&::-webkit-details-marker]:hidden"
      >
        <ChevronRight
          aria-hidden
          className="size-4 group-open/fold:rotate-90 motion-safe:transition-transform motion-safe:duration-150"
        />
        {label}
      </summary>
      <div className="mt-3">{children}</div>
    </details>
  );
}
