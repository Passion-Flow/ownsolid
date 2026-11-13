import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import { listBrowserSessions, revokeBrowserSession, type BrowserSessionPage } from './identity.ts';
import { resetCsrf } from './transport.ts';

beforeEach(resetCsrf);
afterEach(() => vi.unstubAllGlobals());

function serve(response: Response) {
  const fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>((url) =>
    Promise.resolve(
      url.endsWith('/auth/csrf')
        ? new Response(
            JSON.stringify({ data: { token: 'csrf-test', expires_at: '2999-01-01T00:00:00Z' } }),
          )
        : response,
    ),
  );
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('account session client', () => {
  it('uses the installed page size and marks only an unrequested read as background', async () => {
    const fetch = serve(
      new Response(JSON.stringify({ data: { items: [], next_cursor: 'opaque-cursor' } })),
    );
    expectTypeOf(listBrowserSessions).returns.resolves.toEqualTypeOf<BrowserSessionPage>();
    await expect(listBrowserSessions(null, { background: true })).resolves.toEqual({
      items: [],
      next_cursor: 'opaque-cursor',
    });
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/me/sessions');
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({
      headers: { 'X-Ownsolid-Background': 'true' },
    });
  });

  it('passes a cursor unchanged without a workspace, limit, or background header on an explicit read', async () => {
    const fetch = serve(new Response(JSON.stringify({ data: { items: [], next_cursor: null } })));
    await listBrowserSessions('opaque_cursor-42');
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/me/sessions?cursor=opaque_cursor-42');
    expect(fetch.mock.calls[0]?.[1]).not.toHaveProperty('headers');
  });

  it('sends a bodyless DELETE with CSRF and accepts an empty 204', async () => {
    const fetch = serve(new Response(null, { status: 204 }));
    await expect(
      revokeBrowserSession('11111111-1111-4111-8111-111111111111'),
    ).resolves.toBeUndefined();
    const [url, init] = fetch.mock.calls[1] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/me/sessions/11111111-1111-4111-8111-111111111111');
    expect(init).toMatchObject({
      method: 'DELETE',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': 'csrf-test' },
    });
    expect(init.body).toBeUndefined();
    expect(new Headers(init.headers).has('Content-Type')).toBe(false);
    expect(new Headers(init.headers).has('Idempotency-Key')).toBe(false);
  });

  it.each([
    [403, 'CSRF_INVALID'],
    [404, 'RESOURCE_NOT_FOUND'],
    [409, 'STORAGE_COMMIT_OUTCOME_UNKNOWN'],
    [503, 'STORAGE_COMMIT_ROLLED_BACK'],
  ] as const)('does not replay or reinterpret %s %s as success', async (status, code) => {
    const fetch = serve(
      new Response(JSON.stringify({ code, request_id: 'session-test' }), {
        status,
        headers: { 'Retry-After': '2' },
      }),
    );
    await expect(
      revokeBrowserSession('11111111-1111-4111-8111-111111111111'),
    ).rejects.toMatchObject({ code, status, requestId: 'session-test', retryAfterSeconds: 2 });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
