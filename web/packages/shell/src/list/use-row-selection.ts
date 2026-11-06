// Which rows are selected. Only rows on screen count, so a filter that hides a row also
// takes it out of what a bulk action would touch.
import { useCallback, useMemo, useState } from 'react';
import { selectedOnScreen } from './list-state.ts';

/** What {@link useRowSelection} returns. */
export interface RowSelection {
  selected: ReadonlySet<string>;
  isSelected: (id: string) => boolean;
  toggle: (id: string, on: boolean) => void;
  /** Selects every selectable row, or none when all of them are selected. */
  toggleAll: () => void;
  clear: () => void;
  select: (ids: readonly string[]) => void;
  allSelected: boolean;
  someSelected: boolean;
}

/** Tracks selection among the given selectable rows. */
export function useRowSelection(rows: readonly string[]): RowSelection {
  const [raw, setRaw] = useState<ReadonlySet<string>>(new Set());
  const selected = useMemo(() => selectedOnScreen(raw, rows), [raw, rows]);
  const clear = useCallback(() => {
    setRaw(new Set());
  }, []);
  const allSelected = rows.length > 0 && selected.size === rows.length;
  return {
    selected,
    isSelected: (id) => selected.has(id),
    toggle: (id, on) => {
      const next = new Set(selected);
      if (on) next.add(id);
      else next.delete(id);
      setRaw(next);
    },
    toggleAll: () => {
      setRaw(allSelected ? new Set() : new Set(rows));
    },
    clear,
    select: (ids) => {
      setRaw(new Set(ids));
    },
    allSelected,
    someSelected: selected.size > 0 && !allSelected,
  };
}
