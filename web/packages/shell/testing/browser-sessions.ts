import type { BrowserSession } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';
import type { ScriptedProblem } from './control-plane.ts';

/** Contract-shaped fixture only; no device or location metadata is invented. */
export const browserSession: BrowserSession = {
  id: '11111111-1111-4111-8111-111111111111',
  current: true,
  authentication_method: 'password',
  created_at: '2026-11-13T00:00:00Z',
  last_seen_at: '2026-11-13T00:05:00Z',
  absolute_expires_at: '2026-11-14T00:00:00Z',
};

/** Browser fixture state for the account-scoped session endpoints. */
export interface BrowserSessionScript {
  browserSessions: BrowserSession[];
  browserSessionPageSize: number;
  browserSessionReads: { cursor: string | null; background: boolean }[];
  browserSessionReadProblem: ScriptedProblem | null;
  browserSessionEndProblem: ScriptedProblem | null;
  browserSessionEnds: string[];
  browserSessionRevokedIds: string[];
}

/** One current sign-in by default, independently reset for every browser fixture. */
export function emptyBrowserSessionScript(): BrowserSessionScript {
  return {
    browserSessions: [{ ...browserSession }],
    browserSessionPageSize: 20,
    browserSessionReads: [],
    browserSessionReadProblem: null,
    browserSessionEndProblem: null,
    browserSessionEnds: [],
    browserSessionRevokedIds: [],
  };
}

/** Replaces this browser's session after a confirmed credential rotation. */
export function rotateBrowserSession(state: BrowserSessionScript, endOthers: boolean): void {
  state.browserSessions = [
    { ...browserSession, id: crypto.randomUUID() },
    ...(endOthers ? [] : state.browserSessions.map((row) => ({ ...row, current: false }))),
  ];
}

function problem(route: Route, failure: ScriptedProblem) {
  return route.fulfill({
    status: failure.status,
    contentType: 'application/problem+json',
    headers: failure.headers ?? {},
    body: JSON.stringify({ code: failure.code, request_id: 'browser-session-fixture' }),
  });
}

/** Uses the production wire shape; endpoint authority remains the installed server's job. */
export async function routeBrowserSessions(
  page: Page,
  state: BrowserSessionScript & { signedIn: boolean },
): Promise<void> {
  await page.route(
    (url) => url.pathname === '/api/v1/me/sessions',
    (route) => {
      if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_REQUIRED' });
      const request = route.request();
      const cursor = new URL(request.url()).searchParams.get('cursor');
      state.browserSessionReads.push({
        cursor,
        background: request.headers()['x-ownsolid-background'] === 'true',
      });
      if (state.browserSessionReadProblem) return problem(route, state.browserSessionReadProblem);
      if (cursor !== null && !/^session_[0-9]+$/.test(cursor))
        return problem(route, { status: 400, code: 'INVALID_CURSOR' });
      const offset = cursor === null ? 0 : Number(cursor.slice('session_'.length));
      const items = state.browserSessions.slice(offset, offset + state.browserSessionPageSize);
      const next = offset + items.length;
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            items,
            next_cursor: next < state.browserSessions.length ? `session_${next}` : null,
          },
        }),
      });
    },
  );
  await page.route('**/api/v1/me/sessions/*', (route) => {
    if (!state.signedIn) return problem(route, { status: 401, code: 'AUTH_REQUIRED' });
    const request = route.request();
    if (
      request.method() !== 'DELETE' ||
      request.postData() !== null ||
      new URL(request.url()).search !== ''
    )
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    if (request.headers()['x-csrf-token'] !== 'csrf-token')
      return problem(route, { status: 403, code: 'CSRF_INVALID' });
    const id = new URL(request.url()).pathname.split('/').at(-1) ?? '';
    state.browserSessionEnds.push(id);
    if (state.browserSessionEndProblem) {
      const failure = state.browserSessionEndProblem;
      state.browserSessionEndProblem = null;
      return problem(route, failure);
    }
    const row = state.browserSessions.find((item) => item.id === id);
    if (!row && !state.browserSessionRevokedIds.includes(id))
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (row?.current) state.signedIn = false;
    state.browserSessions = state.browserSessions.filter((row) => row.id !== id);
    state.browserSessionRevokedIds.push(id);
    return route.fulfill({ status: 204 });
  });
}
