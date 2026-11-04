import { ApiProblem } from '@ownsolid/api';
import { describe, expect, it, vi } from 'vitest';
import { createQueryClient, isSignedOut, sessionKey, sessionQuery } from './session.ts';

function serveMe(response: () => Response | Promise<Response>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(response())),
  );
}

const problem = (status: number, code: string) =>
  new Response(JSON.stringify({ code, request_id: 'r' }), { status });

describe('sessionQuery', () => {
  it('returns the identity when signed in', async () => {
    serveMe(() => new Response(JSON.stringify({ data: { user: { id: 'u' } } })));
    const client = createQueryClient({ onSignedOut: () => undefined });
    await expect(client.query(sessionQuery())).resolves.toEqual({ user: { id: 'u' } });
  });

  it.each(['AUTH_REQUIRED', 'AUTH_SESSION_EXPIRED'])('treats %s as signed out', async (code) => {
    serveMe(() => problem(401, code));
    const client = createQueryClient({ onSignedOut: () => undefined });
    await expect(client.query(sessionQuery())).resolves.toBeNull();
  });

  it('keeps other failures as errors', async () => {
    serveMe(() => problem(403, 'ACCESS_DENIED'));
    const client = createQueryClient({ onSignedOut: () => undefined });
    await expect(client.query(sessionQuery())).rejects.toMatchObject({ code: 'ACCESS_DENIED' });
  });
});

describe('createQueryClient', () => {
  it('clears the session and reports when any query finds it over', async () => {
    const onSignedOut = vi.fn();
    const client = createQueryClient({ onSignedOut });
    client.setQueryData(sessionKey, { user: { id: 'u' } });
    await client
      .query({
        queryKey: ['nodes'],
        queryFn: () => Promise.reject(new ApiProblem('AUTH_SESSION_EXPIRED', { status: 401 })),
      })
      .catch(() => undefined);
    expect(onSignedOut).toHaveBeenCalledOnce();
    expect(client.getQueryData(sessionKey)).toBeNull();
  });

  it('does not sign out on other failures', async () => {
    const onSignedOut = vi.fn();
    const client = createQueryClient({ onSignedOut });
    await client
      .query({
        queryKey: ['nodes'],
        queryFn: () => Promise.reject(new ApiProblem('ACCESS_DENIED', { status: 403 })),
      })
      .catch(() => undefined);
    expect(onSignedOut).not.toHaveBeenCalled();
  });

  it('retries a read only for failures that may pass', () => {
    const client = createQueryClient({ onSignedOut: () => undefined });
    const retry = client.getDefaultOptions().queries?.retry as (n: number, e: unknown) => boolean;
    expect(retry(0, new ApiProblem('SERVICE_NOT_READY', { status: 503 }))).toBe(true);
    expect(retry(1, new ApiProblem('NETWORK_UNAVAILABLE'))).toBe(true);
    expect(retry(2, new ApiProblem('NETWORK_UNAVAILABLE'))).toBe(false);
    expect(retry(0, new ApiProblem('ACCESS_DENIED', { status: 403 }))).toBe(false);
    expect(retry(0, new Error('bug'))).toBe(false);
  });
});

describe('isSignedOut', () => {
  it('recognizes only the signed-out codes', () => {
    expect(isSignedOut(new ApiProblem('AUTH_REQUIRED'))).toBe(true);
    expect(isSignedOut(new ApiProblem('ACCESS_DENIED'))).toBe(false);
    expect(isSignedOut(new Error('AUTH_REQUIRED'))).toBe(false);
  });
});
