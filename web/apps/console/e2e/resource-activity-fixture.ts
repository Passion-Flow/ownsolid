// Mirrors the existing parser's time policy and raw query fingerprint. This is
// transport coverage, not a real server, durable audit append or SQL acceptance.
import type { AuditEvent } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

/** Mutable faults and observations for the resource history browser flows. */
export interface ActivityFixture {
  queries: URLSearchParams[];
  failure: { status: number; code: string } | null;
  hold: (() => Promise<void>) | null;
  events: AuditEvent[];
}

/** Reject drift before restoring pinned bounds, as makeScopedListPlan does. */
export async function boundedActivity(
  page: Page,
  events: AuditEvent[],
  maximumDays = 31,
): Promise<ActivityFixture> {
  const state: ActivityFixture = { queries: [], failure: null, hold: null, events };
  const cursors = new Map<string, { filter: string; start: number; from: string; to: string }>();
  let sequence = 0;
  const refuse = (route: Route, status: number, code: string) =>
    route.fulfill({
      status,
      contentType: 'application/problem+json',
      body: JSON.stringify({ code, status, request_id: '6f1c7a1e-0000-4000-8000-0000000000fa' }),
    });
  await page.route('**/api/v1/admin/audit-events?*', async (route) => {
    const query = new URL(route.request().url()).searchParams;
    state.queries.push(query);
    if (state.hold) await state.hold();
    if (route.request().failure()) return;
    if (state.failure) return refuse(route, state.failure.status, state.failure.code);
    const fingerprint = new URLSearchParams(query);
    fingerprint.delete('limit');
    fingerprint.delete('cursor');
    fingerprint.sort();
    const filter = fingerprint.toString();
    const rawCursor = query.get('cursor');
    const cursor = rawCursor ? cursors.get(rawCursor) : null;
    if (rawCursor && (!cursor || cursor.filter !== filter))
      return refuse(route, 400, 'INVALID_CURSOR');
    const now = Date.now();
    const from = query.get('from') ?? cursor?.from ?? new Date(now - 7 * 86_400_000).toISOString();
    const to = query.get('to') ?? cursor?.to ?? new Date(now).toISOString();
    const duration = Date.parse(to) - Date.parse(from);
    if (!Number.isFinite(duration) || duration <= 0 || duration > maximumDays * 86_400_000) {
      return refuse(route, 400, 'VALIDATION_FAILED');
    }
    if (cursor && (from !== cursor.from || to !== cursor.to))
      return refuse(route, 400, 'INVALID_CURSOR');
    const matches = state.events.filter(
      (event) =>
        (!query.get('target_id') || event.target_id === query.get('target_id')) &&
        (!query.get('actor_user_id') || event.actor_user_id === query.get('actor_user_id')) &&
        Date.parse(event.occurred_at) >= Date.parse(from) &&
        Date.parse(event.occurred_at) <= Date.parse(to),
    );
    const start = cursor?.start ?? 0;
    const end = start + Number(query.get('limit') ?? 20);
    const next = end < matches.length ? `history-${++sequence}` : null;
    if (next) cursors.set(next, { filter, start: end, from, to });
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: { items: matches.slice(start, end), next_cursor: next } }),
    });
  });
  return state;
}
