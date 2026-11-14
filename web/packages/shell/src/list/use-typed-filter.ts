import { useEffect, useState } from 'react';
import type { ListView } from './use-list-view.ts';

/**
 * A text filter's field: it follows typing, the list follows the field after a pause, and a
 * change from elsewhere, such as a saved view or the back button, comes back to the field.
 */
export function useTypedFilter(
  list: ListView,
  name: string,
  debounceMilliseconds: number,
): [string, (value: string) => void] {
  const applied = list.state.filters[name];
  const [typed, setTyped] = useState(applied ?? '');
  const [seen, setSeen] = useState(applied);
  if (applied !== seen) {
    setSeen(applied);
    setTyped(applied ?? '');
  }
  const { setFilter } = list;
  useEffect(() => {
    if (typed.trim() === (applied ?? '')) return undefined;
    const timer = setTimeout(() => {
      setFilter(name, typed.trim());
    }, debounceMilliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, [typed, applied, name, debounceMilliseconds, setFilter]);
  return [typed, setTyped];
}
