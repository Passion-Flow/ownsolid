import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadRuntimeConfig, parseRuntimeConfig } from './runtime-config.ts';

const valid = {
  data: {
    topology: { size: 'single-node' },
    transport: { mode: 'direct_https', plaintext: false },
    request_timeout_milliseconds: 30_000,
    ui: {
      poll_milliseconds: 5_000,
      search_debounce_milliseconds: 150,
      page_size: 20,
      lookup_page_size: 100,
    },
    workspaces: { maximum_count: 10 },
  },
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('parseRuntimeConfig', () => {
  it('keeps the fields the interface uses', () => {
    expect(parseRuntimeConfig(valid)).toEqual({
      topologySize: 'single-node',
      plaintext: false,
      requestTimeoutMilliseconds: 30_000,
      ui: {
        pollMilliseconds: 5_000,
        searchDebounceMilliseconds: 150,
        pageSize: 20,
        lookupPageSize: 100,
      },
    });
  });

  it.each([
    ['no envelope', { topology: {} }],
    ['no topology size', { data: { ...valid.data, topology: {} } }],
    ['a non-boolean plaintext flag', { data: { ...valid.data, transport: { plaintext: 'no' } } }],
    ['a timeout below the minimum', { data: { ...valid.data, request_timeout_milliseconds: 10 } }],
    [
      'a fractional page size',
      { data: { ...valid.data, ui: { ...valid.data.ui, page_size: 2.5 } } },
    ],
    [
      'a page size above the maximum',
      { data: { ...valid.data, ui: { ...valid.data.ui, page_size: 500 } } },
    ],
  ])('rejects %s', (_name, document) => {
    expect(() => parseRuntimeConfig(document)).toThrow('RESPONSE_INVALID');
  });
});

describe('loadRuntimeConfig', () => {
  it('fetches the document from the same origin', async () => {
    const fetch = vi.fn(() => Promise.resolve(new Response(JSON.stringify(valid))));
    vi.stubGlobal('fetch', fetch);
    await expect(loadRuntimeConfig()).resolves.toMatchObject({ topologySize: 'single-node' });
    expect(fetch).toHaveBeenCalledWith(
      '/runtime-config.json',
      expect.objectContaining({ cache: 'no-store' }),
    );
  });

  it('reports a server that is not ready', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('', { status: 503 }))),
    );
    await expect(loadRuntimeConfig()).rejects.toMatchObject({
      code: 'SERVICE_NOT_READY',
      status: 503,
    });
  });

  it('reports an unreachable server', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('offline'))),
    );
    await expect(loadRuntimeConfig()).rejects.toMatchObject({ code: 'NETWORK_UNAVAILABLE' });
  });
});
