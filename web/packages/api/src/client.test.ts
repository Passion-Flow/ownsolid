import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import { buildUrl, createClient } from './client.ts';
import type { identityContract, nodesContract, notificationsContract } from './gen/index.ts';
import { resetCsrf } from './transport.ts';

const identity = createClient<identityContract.paths>();
const notifications = createClient<notificationsContract.paths>();

function respond(body: unknown, status = 200) {
  const fetch = vi.fn((url: string) =>
    Promise.resolve(
      url.endsWith('/auth/csrf')
        ? new Response(JSON.stringify({ data: { token: 't', expires_at: '2999-01-01T00:00:00Z' } }))
        : new Response(status === 204 ? null : JSON.stringify(body), { status }),
    ),
  );
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

beforeEach(() => {
  resetCsrf();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const nodes = createClient<nodesContract.paths>();
const nodeList = '/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes';

describe('buildUrl', () => {
  it('fills path parameters and encodes them', () => {
    expect(buildUrl('/a/{id}/b', { id: 'x/y z' })).toBe('/a/x%2Fy%20z/b');
  });

  it('skips empty query values and repeats arrays', () => {
    expect(buildUrl('/a', {}, { limit: 5, cursor: undefined, none: null, tag: ['x', 'y'] })).toBe(
      '/a?limit=5&tag=x&tag=y',
    );
  });

  it('refuses a missing path parameter', () => {
    expect(() => buildUrl('/a/{id}')).toThrow('missing path parameter id');
  });
});

describe('createClient', () => {
  it('reads with the query from the contract', async () => {
    const fetch = respond({ data: { items: [], next_cursor: null } });
    await notifications.get('/api/v1/notifications', { query: { limit: 5 } });
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/notifications?limit=5');
  });

  it('writes with path parameters, a body and the idempotency key', async () => {
    const fetch = respond({ data: {} });
    await identity.post('/api/v1/admin/invitations/{invitation_id}/resends', {
      path: { invitation_id: 'i-1' },
      body: { version: 3 },
      idempotencyKey: 'k-1',
    });
    const [url, init] = fetch.mock.calls[1] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/admin/invitations/i-1/resends');
    expect(init.body).toBe('{"version":3}');
    expect(init.headers).toMatchObject({ 'Idempotency-Key': 'k-1', 'X-CSRF-Token': 't' });
  });

  it('fills parameters a contract declares for the whole path', async () => {
    const fetch = respond({ data: { items: [], next_cursor: null } });
    await nodes.get(nodeList, {
      path: { site_id: 'default', cell_id: 'default' },
      query: { limit: 20 },
    });
    expect(fetch.mock.calls[0]?.[0]).toBe(
      '/api/v1/sites/default/cells/default/admin/nodes?limit=20',
    );
  });

  it('resolves to nothing for an empty response', async () => {
    respond(null, 204);
    await expect(identity.post('/api/v1/auth/logout')).resolves.toBeUndefined();
  });
});

describe('types follow the contract', () => {
  type Schemas = identityContract.components['schemas'];

  it('unwraps the success envelope', () => {
    expectTypeOf(identity.get<'/api/v1/me'>).returns.resolves.toEqualTypeOf<Schemas['MeData']>();
  });

  it('rejects calls that disagree with the contract', () => {
    const neverCalled = () => {
      // @ts-expect-error the contract has no such path
      void identity.get('/api/v1/no-such-path');
      // @ts-expect-error this path has no GET
      void identity.get('/api/v1/auth/login');
      // @ts-expect-error the body is required
      void identity.post('/api/v1/auth/login');
      // @ts-expect-error the body misses a required field
      void identity.post('/api/v1/auth/login', { body: { email: 'a' } });
      // @ts-expect-error the path parameter is required
      void identity.get('/api/v1/admin/users/{user_id}');
      // @ts-expect-error a parameter declared for the whole path is required too
      void nodes.get(nodeList);
      // @ts-expect-error the contract requires an idempotency key
      void identity.post('/api/v1/admin/invitations', {
        body: { email: 'a@example.test', locale: 'en' },
      });
    };
    expect(neverCalled).toBeTypeOf('function');
  });
});
