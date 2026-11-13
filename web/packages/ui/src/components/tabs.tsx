import { useId, useLayoutEffect, useRef, type KeyboardEvent, type ReactNode } from 'react';
import { cn } from '../cn.ts';

/** One tab of {@link Tabs}. */
export interface TabItem<T extends string> {
  value: T;
  label: string;
}

/** Props for {@link Tabs}. */
export interface TabsProps<T extends string> {
  /** Accessible name of the tab list. */
  label: string;
  value: T;
  tabs: readonly TabItem<T>[];
  onChange: (value: T) => void;
  /** The chosen tab's panel. */
  children: ReactNode;
}

/**
 * Views of one thing, on the WAI-ARIA tabs pattern: the arrow keys, Home and End move between tabs and choose
 * as they go. The chosen tab is marked by a bar and by weight, never by colour alone.
 */
export function Tabs<T extends string>({ label, value, tabs, onChange, children }: TabsProps<T>) {
  const id = useId();
  const list = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const viewport = list.current;
    const selected = viewport?.querySelector('[aria-selected="true"]');
    if (!viewport || !selected) return;
    const reveal = () => {
      const visible = viewport.getBoundingClientRect();
      const active = selected.getBoundingClientRect();
      // Only move the tab strip; revealing a deep link must not move the whole page.
      if (active.left < visible.left) viewport.scrollLeft += active.left - visible.left;
      else if (active.right > visible.right) viewport.scrollLeft += active.right - visible.right;
    };
    reveal();
    if (typeof ResizeObserver === 'undefined') return;
    const resize = new ResizeObserver(reveal);
    resize.observe(viewport);
    resize.observe(selected);
    return () => {
      resize.disconnect();
    };
  }, [value]);
  const move = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const targets: Partial<Record<string, number>> = {
      ArrowRight: (index + 1) % tabs.length,
      ArrowLeft: (index - 1 + tabs.length) % tabs.length,
      Home: 0,
      End: tabs.length - 1,
    };
    const next = targets[event.key];
    const tab = next === undefined ? undefined : tabs[next];
    if (next === undefined || !tab) return;
    event.preventDefault();
    onChange(tab.value);
    list.current?.querySelectorAll('button')[next]?.focus();
  };
  return (
    <>
      <div
        ref={list}
        role="tablist"
        aria-label={label}
        className="mb-4 flex gap-1 overflow-x-auto border-b border-border"
      >
        {tabs.map((tab, index) => {
          const chosen = tab.value === value;
          return (
            <button
              key={tab.value}
              type="button"
              role="tab"
              id={`${id}-${tab.value}`}
              aria-selected={chosen}
              aria-controls={`${id}-panel`}
              tabIndex={chosen ? 0 : -1}
              onClick={() => {
                onChange(tab.value);
              }}
              onKeyDown={(event) => {
                move(event, index);
              }}
              className={cn(
                '-mb-px cursor-pointer border-0 border-b-2 bg-transparent px-3 py-2 whitespace-nowrap',
                chosen
                  ? 'border-accent font-semibold text-text'
                  : 'border-transparent font-medium text-text-2 hover:text-text',
              )}
            >
              {tab.label}
            </button>
          );
        })}
      </div>
      <div id={`${id}-panel`} role="tabpanel" aria-labelledby={`${id}-${value}`}>
        {children}
      </div>
    </>
  );
}
