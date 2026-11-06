// What a list shows: its filters and which columns are hidden. A saved view stores this, so
// it may come from an older version of the page, and anything unknown is dropped.
import type { TranslationKey } from '@ownsolid/i18n';

/** One column of a list. */
export interface ListColumn {
  id: string;
  label: TranslationKey;
  /** The column that names the row cannot be hidden. */
  locked?: boolean;
}

/** One filter: a text prefix, or a choice among known values. */
export type ListFilter =
  { kind: 'text'; maximum: number } | { kind: 'choice'; values: readonly string[] };

/** What a page declares about its list. */
export interface ListDefinition {
  /** Names the list on the server, such as `mail-deliveries`. */
  key: string;
  filters: Readonly<Record<string, ListFilter>>;
  columns: readonly ListColumn[];
}

/** The state of a list. An empty filter is left out. */
export interface ListState {
  filters: Readonly<Record<string, string>>;
  hiddenColumns: readonly string[];
}

/** The state when nothing has been chosen. */
export const emptyListState: ListState = { filters: {}, hiddenColumns: [] };

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** Reads a stored state, keeping only what the page still has. */
export function readListState(stored: unknown, definition: ListDefinition): ListState {
  const source = record(stored);
  const filters: Record<string, string> = {};
  for (const [name, value] of Object.entries(record(source.filters))) {
    const filter = definition.filters[name];
    if (!filter || typeof value !== 'string' || value === '') continue;
    const known =
      filter.kind === 'text' ? value.length <= filter.maximum : filter.values.includes(value);
    if (known) filters[name] = value;
  }
  const hideable = definition.columns.filter((column) => !column.locked).map((item) => item.id);
  const hidden = Array.isArray(source.hiddenColumns) ? (source.hiddenColumns as unknown[]) : [];
  return {
    filters,
    hiddenColumns: hideable.filter((id) => hidden.includes(id)),
  };
}

/** True when two states show the same thing, whatever order they were built in. */
export function sameListState(left: ListState, right: ListState): boolean {
  const canonical = (state: ListState) =>
    JSON.stringify([
      Object.entries(state.filters).sort(([a], [b]) => a.localeCompare(b)),
      [...state.hiddenColumns].sort(),
    ]);
  return canonical(left) === canonical(right);
}

/** Sets or clears one filter. */
export function withFilter(state: ListState, name: string, value: string): ListState {
  const filters = Object.fromEntries(Object.entries(state.filters).filter(([key]) => key !== name));
  if (value !== '') filters[name] = value;
  return { ...state, filters };
}

/** Shows or hides one column. */
export function withColumn(state: ListState, id: string, visible: boolean): ListState {
  const hidden = state.hiddenColumns.filter((column) => column !== id);
  return { ...state, hiddenColumns: visible ? hidden : [...hidden, id] };
}

/** Selected rows that are still on screen, for after a filter or a reload. */
export function selectedOnScreen(
  selected: ReadonlySet<string>,
  rows: readonly string[],
): ReadonlySet<string> {
  const kept = rows.filter((id) => selected.has(id));
  return kept.length === selected.size ? selected : new Set(kept);
}
