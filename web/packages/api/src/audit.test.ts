import { afterEach, expect, it, vi } from 'vitest';
import { listAuditEvents } from './audit.ts';

afterEach(() => {
  vi.unstubAllGlobals();
});

it('preserves the first-page query fingerprint with an opaque cursor', async () => {
  const fetch = vi.fn((_url: string) =>
    Promise.resolve(new Response(JSON.stringify({ data: { items: [], next_cursor: null } }))),
  );
  vi.stubGlobal('fetch', fetch);
  const query = {
    limit: 20,
    targetId: 'node-1',
    from: '2026-11-11T00:00:00.000Z',
    to: '2026-11-12T00:00:00.000Z',
  };
  await listAuditEvents(query);
  await listAuditEvents({ ...query, cursor: 'opaque-next' });
  const parameters = fetch.mock.calls.map(([url]) => new URL(url, 'http://example.test').searchParams);
  expect(parameters[1]?.get('cursor')).toBe('opaque-next');
  for (const params of parameters) {
    params.delete('cursor');
    params.delete('limit');
    params.sort();
  }
  expect(parameters[1]?.toString()).toBe(parameters[0]?.toString());
});

it('does not invent explicit bounds when a caller only has a cursor', async () => {
  const fetch = vi.fn((_url: string) =>
    Promise.resolve(new Response(JSON.stringify({ data: { items: [], next_cursor: null } }))),
  );
  vi.stubGlobal('fetch', fetch);
  await listAuditEvents({ limit: 20, cursor: 'opaque-next' });
  const params = new URL(fetch.mock.calls[0]?.[0] ?? '', 'http://example.test').searchParams;
  expect(params.get('cursor')).toBe('opaque-next');
  expect(params.has('from')).toBe(false);
  expect(params.has('to')).toBe(false);
});
