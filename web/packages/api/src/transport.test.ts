import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isProblem, type ApiProblem } from './problem.ts';
import { get, resetCsrf, setRequestTimeout, write } from './transport.ts';

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>;

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

const future = () => new Date(Date.now() + 3_600_000).toISOString();

let calls: { url: string; init: RequestInit }[];

function serve(handler: Handler): void {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init: RequestInit) => {
      calls.push({ url, init });
      return Promise.resolve(handler(url, init));
    }),
  );
}

function header(init: RequestInit, name: string): string | undefined {
  return (init.headers as Record<string, string> | undefined)?.[name];
}

beforeEach(() => {
  resetCsrf();
  setRequestTimeout(30_000);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('get', () => {
  it('returns the data of the envelope', async () => {
    serve(() => json(200, { data: { id: 'a' } }));
    await expect(get('/api/v1/me')).resolves.toEqual({ id: 'a' });
    expect(calls[0]?.init).toMatchObject({
      method: 'GET',
      credentials: 'same-origin',
      cache: 'no-store',
    });
  });

  it('raises the server problem with its code, request ID and fields', async () => {
    serve(() =>
      json(
        429,
        {
          code: 'RATE_LIMITED',
          request_id: 'r-1',
          fields: [{ field: 'email', code: 'x' }, 'junk'],
        },
        { 'Retry-After': '7' },
      ),
    );
    const error = await get('/api/v1/me').catch((e: unknown) => e);
    expect(isProblem(error, 'RATE_LIMITED')).toBe(true);
    const problem = error as ApiProblem;
    expect(problem.status).toBe(429);
    expect(problem.requestId).toBe('r-1');
    expect(problem.retryAfterSeconds).toBe(7);
    expect(problem.fields).toEqual([{ field: 'email', code: 'x' }]);
    expect(problem.retryable).toBe(true);
  });

  it('treats a body without an envelope as invalid', async () => {
    serve(() => json(200, { id: 'a' }));
    await expect(get('/x')).rejects.toMatchObject({ code: 'RESPONSE_INVALID' });
  });

  it('treats a failure without a problem code as invalid', async () => {
    serve(
      () =>
        new Response('<html>proxy error</html>', { status: 502, headers: { 'X-Request-ID': 'p' } }),
    );
    await expect(get('/x')).rejects.toMatchObject({
      code: 'RESPONSE_INVALID',
      status: 502,
      requestId: 'p',
    });
  });

  it('reports an unreachable server', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))),
    );
    const error = await get('/x').catch((e: unknown) => e);
    expect(error).toMatchObject({ code: 'NETWORK_UNAVAILABLE', status: 0 });
    expect((error as ApiProblem).retryable).toBe(true);
  });

  it('times out after the configured request timeout', async () => {
    setRequestTimeout(20);
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url: string, init: RequestInit) =>
          new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => {
              reject(new DOMException('aborted', 'AbortError'));
            });
          }),
      ),
    );
    await expect(get('/x')).rejects.toMatchObject({ code: 'REQUEST_TIMEOUT' });
  });

  it('passes a caller abort through unchanged', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url: string, init: RequestInit) =>
          new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => {
              reject(new DOMException('aborted', 'AbortError'));
            });
          }),
      ),
    );
    const controller = new AbortController();
    const pending = get('/x', { signal: controller.signal });
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  });
});

describe('write', () => {
  it('sends the CSRF token, the JSON body and the idempotency key', async () => {
    serve((url) =>
      url === '/api/v1/auth/csrf'
        ? json(200, { data: { token: 't-1', expires_at: future() } })
        : json(201, { data: { ok: true } }),
    );
    await expect(
      write('POST', '/api/v1/things', { a: 1 }, { idempotencyKey: 'k-1' }),
    ).resolves.toEqual({
      ok: true,
    });
    const sent = calls[1]?.init;
    expect(sent?.method).toBe('POST');
    expect(sent?.body).toBe('{"a":1}');
    expect(sent && header(sent, 'X-CSRF-Token')).toBe('t-1');
    expect(sent && header(sent, 'Content-Type')).toBe('application/json');
    expect(sent && header(sent, 'Idempotency-Key')).toBe('k-1');
  });

  it('reuses a CSRF token until it nears expiry', async () => {
    serve((url) =>
      url === '/api/v1/auth/csrf'
        ? json(200, { data: { token: 't', expires_at: future() } })
        : new Response(null, { status: 204 }),
    );
    await write('DELETE', '/a');
    await write('DELETE', '/b');
    expect(calls.filter((call) => call.url === '/api/v1/auth/csrf')).toHaveLength(1);
  });

  it('fetches a new CSRF token when the cached one is about to expire', async () => {
    let issued = 0;
    serve((url) => {
      if (url !== '/api/v1/auth/csrf') return new Response(null, { status: 204 });
      issued += 1;
      return json(200, {
        data: { token: `t-${issued}`, expires_at: new Date(Date.now() + 10_000).toISOString() },
      });
    });
    await write('DELETE', '/a');
    await write('DELETE', '/b');
    expect(issued).toBe(2);
  });

  it('retries once with a fresh token when the server rejects the token', async () => {
    let issued = 0;
    let writes = 0;
    serve((url, init) => {
      if (url === '/api/v1/auth/csrf') {
        issued += 1;
        return json(200, { data: { token: `t-${issued}`, expires_at: future() } });
      }
      writes += 1;
      return header(init, 'X-CSRF-Token') === 't-1'
        ? json(403, { code: 'CSRF_INVALID', request_id: 'r' })
        : json(200, { data: 'done' });
    });
    await expect(write('PATCH', '/a', {})).resolves.toBe('done');
    expect(issued).toBe(2);
    expect(writes).toBe(2);
  });

  it('gives up after one CSRF retry', async () => {
    serve((url) =>
      url === '/api/v1/auth/csrf'
        ? json(200, { data: { token: 't', expires_at: future() } })
        : json(403, { code: 'CSRF_INVALID', request_id: 'r' }),
    );
    await expect(write('POST', '/a')).rejects.toMatchObject({ code: 'CSRF_INVALID' });
    expect(calls).toHaveLength(4);
  });

  it('does not cache a failed CSRF request', async () => {
    let first = true;
    serve((url) => {
      if (url === '/api/v1/auth/csrf') {
        if (first) {
          first = false;
          return json(503, { code: 'SERVICE_NOT_READY', request_id: 'r' });
        }
        return json(200, { data: { token: 't', expires_at: future() } });
      }
      return new Response(null, { status: 204 });
    });
    await expect(write('POST', '/a')).rejects.toMatchObject({ code: 'SERVICE_NOT_READY' });
    await expect(write('POST', '/a')).resolves.toBeUndefined();
  });

  it('omits the body and content type when there is no body', async () => {
    serve((url) =>
      url === '/api/v1/auth/csrf'
        ? json(200, { data: { token: 't', expires_at: future() } })
        : new Response(null, { status: 204 }),
    );
    await write('POST', '/a');
    const sent = calls[1]?.init;
    expect(sent?.body).toBeUndefined();
    expect(sent && header(sent, 'Content-Type')).toBeUndefined();
  });
});
