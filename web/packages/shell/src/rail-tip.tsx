import { useEffect, useState, type ReactNode } from 'react';

/**
 * The name beside an icon of the rail. `fixed` escapes the sidebar's scrolling, and it stays while
 * the pointer moves onto it and goes with Escape, as WCAG 1.4.13 asks of content shown on hover.
 */
export function RailTip({ label, children }: { label: string; children: ReactNode }) {
  const [at, setAt] = useState<{ left: number; top: number } | null>(null);
  const show = (element: HTMLElement) => {
    const box = element.getBoundingClientRect();
    setAt({ left: box.right, top: box.top + box.height / 2 });
  };
  const hide = () => {
    setAt(null);
  };
  const showing = at !== null;
  useEffect(() => {
    if (!showing) return undefined;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setAt(null);
    };
    document.addEventListener('keydown', dismiss);
    return () => {
      document.removeEventListener('keydown', dismiss);
    };
  }, [showing]);
  return (
    <div
      role="presentation"
      onMouseEnter={(event) => {
        show(event.currentTarget);
      }}
      onMouseLeave={hide}
      onFocus={(event) => {
        show(event.currentTarget);
      }}
      onBlur={hide}
    >
      {children}
      {at ? (
        <span
          aria-hidden
          className="fixed z-20 -translate-y-1/2 pl-2"
          style={{ left: at.left, top: at.top }}
        >
          <span className="block rounded-control bg-text px-2 py-1 text-xs font-medium whitespace-nowrap text-surface">
            {label}
          </span>
        </span>
      ) : null}
    </div>
  );
}
