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

/** The query parameter that names the row whose drawer is open. */
export const openRowParam = 'open';

/**
 * Reads a list's filters and open row from a query string. A link may be old or edited by
 * hand, so values are checked like a saved view's.
 */
export function readListLink(
  query: string,
  definition: ListDefinition,
): { filters: ListState['filters']; open: string | null } {
  const params = new URLSearchParams(query);
  const raw: Record<string, string> = {};
  for (const name of Object.keys(definition.filters)) {
    const value = params.get(name);
    if (value !== null) raw[name] = value;
  }
  const open = params.get(openRowParam);
  return {
    filters: readListState({ filters: raw }, definition).filters,
    open: open !== null && /^[A-Za-z0-9_-]{1,64}$/.test(open) ? open : null,
  };
}

/** True when a query string names any of a list's filters or an open row. */
export function linksList(query: string, definition: ListDefinition): boolean {
  const params = new URLSearchParams(query);
  return [...Object.keys(definition.filters), openRowParam].some((name) => params.has(name));
}

/**
 * Writes a list's filters and open row into a query string, keeping parameters that belong to
 * others, in a stable order so equal states give equal links.
 */
export function writeListLink(
  query: string,
  definition: ListDefinition,
  filters: ListState['filters'],
  open: string | null,
): string {
  const params = new URLSearchParams(query);
  for (const name of [...Object.keys(definition.filters), openRowParam]) params.delete(name);
  for (const name of Object.keys(definition.filters).sort()) {
    const value = filters[name];
    if (value) params.set(name, value);
  }
  if (open) params.set(openRowParam, open);
  const text = params.toString();
  return text === '' ? '' : `?${text}`;
}
