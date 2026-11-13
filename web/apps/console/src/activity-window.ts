/** The exact UTC bounds sent with an activity read and every page of its cursor. */
export interface ActivityWindow {
  from: string;
  to: string;
}

/** Recent is a stable cache selection; its actual bounds are snapped at read time. */
export type ActivitySelection = { kind: 'recent' } | ({ kind: 'custom' } & ActivityWindow);

const day = 24 * 60 * 60 * 1_000;
const utcInstant = /^(?:[1-9]\d{3})-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/;
const localInstant = /^([1-9]\d{3})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/;

function instant(value: string): string | null {
  if (!utcInstant.test(value)) return null;
  const parsed = new Date(value);
  if (!Number.isFinite(parsed.getTime())) return null;
  const canonical = parsed.toISOString();
  // Date.parse normalizes impossible calendar dates instead of refusing them.
  return canonical.slice(0, 19) === value.slice(0, 19) ? canonical : null;
}

/** Read only the activity bounds; an incomplete URL remains a correctable custom draft. */
export function activitySelection(search: string): ActivitySelection {
  const params = new URLSearchParams(search);
  return params.has('activity_from') || params.has('activity_to')
    ? {
        kind: 'custom',
        from: params.get('activity_from') ?? '',
        to: params.get('activity_to') ?? '',
      }
    : { kind: 'recent' };
}

/** Validate calendar dates and ordering without guessing the installation's maximum window. */
export function customActivityWindow(window: ActivityWindow): ActivityWindow | null {
  const from = instant(window.from);
  const to = instant(window.to);
  return from && to && Date.parse(from) < Date.parse(to) ? { from, to } : null;
}

/** A product default of 24 elapsed hours fits the smallest permitted server policy. */
export function recentActivityWindow(now: number): ActivityWindow {
  return { from: new Date(now - day).toISOString(), to: new Date(now).toISOString() };
}

/** Keep unrelated filters and the selected detail tab when applying or clearing dates. */
export function activitySearch(search: string, selection: ActivitySelection): string {
  const params = new URLSearchParams(search);
  params.delete('activity_from');
  params.delete('activity_to');
  if (selection.kind === 'custom') {
    params.set('activity_from', selection.from);
    params.set('activity_to', selection.to);
  }
  const query = params.toString();
  return query ? `?${query}` : '';
}

/** Show an instant in a native date/time field in the browser's time zone. */
export function localActivityTime(value: string): string {
  const canonical = instant(value);
  if (!canonical) return '';
  const parsed = new Date(canonical);
  const pad = (part: number, digits = 2) => String(part).padStart(digits, '0');
  return `${pad(parsed.getFullYear(), 4)}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}T${pad(parsed.getHours())}:${pad(parsed.getMinutes())}:${pad(parsed.getSeconds())}.${pad(parsed.getMilliseconds(), 3)}`;
}

/** Reject normalized calendar dates and nonexistent local daylight-saving times. */
export function activityTimeFromField(value: string): string | null {
  const parts = localInstant.exec(value);
  if (!parts) return null;
  const parsed = new Date(value);
  const expected = `${parts[1]}-${parts[2]}-${parts[3]}T${parts[4]}:${parts[5]}:${parts[6] ?? '00'}.${(parts[7] ?? '').padEnd(3, '0')}`;
  return Number.isFinite(parsed.getTime()) && localActivityTime(parsed.toISOString()) === expected
    ? parsed.toISOString()
    : null;
}
