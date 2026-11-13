import { ApiProblem, type AuditPage, type Me } from '@ownsolid/api';
import { createQueryClient, sessionKey } from '@ownsolid/shell';
import { act, renderHook, waitFor } from '@testing-library/react';
import { focusManager, onlineManager, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useAuditRead } from './audit-read.tsx';

type AuditReader = (cursor: string | null, signal: AbortSignal) => Promise<AuditPage>;

const first: AuditPage = { items: [], next_cursor: 'next' };
const workspace: NonNullable<Me['workspace']> = {
  id: '6f1c7a1e-0000-4000-8000-000000000002',
  tenant_id: '6f1c7a1e-0000-4000-8000-000000000003',
  name: 'Research Lab',
  role: 'owner',
};
const me: Me = {
  audience: 'admin',
  workspace,
  user: {
    id: '6f1c7a1e-0000-4000-8000-000000000001',
    email: 'reader@example.test',
    display_name: 'Audit reader',
    locale: 'en',
    status: 'active',
    is_platform_admin: true,
    email_verified_at: null,
    version: 1,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
  },
};

function setup<TPage extends AuditPage = AuditPage>(
  read: (
    cursor: string | null,
    signal: AbortSignal,
    firstPage: TPage | undefined,
  ) => Promise<TPage>,
) {
  const client = createQueryClient({ onSignedOut: () => undefined });
  client.setQueryData(sessionKey, me);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const mount = () =>
    renderHook(() => useAuditRead<TPage>({ queryKey: ['audit'], read }), { wrapper });
  return { client, mount };
}

afterEach(() => {
  focusManager.setFocused(undefined);
  onlineManager.setOnline(true);
});

describe('explicit audit reads', () => {
  it('keeps cursor window context with old pages until a first-page replacement succeeds', async () => {
    interface WindowedPage extends AuditPage {
      clientWindow: { from: string; to: string };
    }
    const old: WindowedPage = {
      ...first,
      clientWindow: { from: '2026-11-11T00:00:00.000Z', to: '2026-11-12T00:00:00.000Z' },
    };
    const fresh: WindowedPage = {
      ...first,
      clientWindow: { from: '2026-11-12T00:00:00.000Z', to: '2026-11-13T00:00:00.000Z' },
    };
    const read = vi
      .fn<
        (
          cursor: string | null,
          signal: AbortSignal,
          cached: WindowedPage | undefined,
        ) => Promise<WindowedPage>
      >()
      .mockResolvedValueOnce(old)
      .mockResolvedValueOnce({ ...old, next_cursor: 'last' })
      .mockRejectedValueOnce(new ApiProblem('INVALID_CURSOR', { status: 400 }))
      .mockRejectedValueOnce(new ApiProblem('STORAGE_COMMIT_OUTCOME_UNKNOWN', { status: 409 }))
      .mockResolvedValueOnce(fresh)
      .mockResolvedValueOnce({ ...fresh, next_cursor: null });
    const { client, mount } = setup(read);
    const initial = mount();
    await waitFor(() => {
      expect(initial.result.current.isSuccess).toBe(true);
    });
    await act(async () => {
      await initial.result.current.fetchNextPage({ cancelRefetch: false });
    });
    initial.unmount();
    const again = mount();
    expect(read).toHaveBeenCalledTimes(2);
    await act(async () => {
      await again.result.current.fetchNextPage({ cancelRefetch: false });
    });
    act(() => {
      again.result.current.readAgain();
    });
    await waitFor(() => {
      expect(read).toHaveBeenCalledTimes(4);
    });
    await waitFor(() => {
      expect(again.result.current.fetchStatus).toBe('idle');
    });
    expect(again.result.current.data?.pages).toHaveLength(2);
    expect(again.result.current.data?.pages[0]?.clientWindow).toEqual(old.clientWindow);
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['audit'] });
    });
    expect(read).toHaveBeenCalledTimes(4);
    act(() => {
      again.result.current.readAgain();
    });
    await waitFor(() => {
      expect(again.result.current.data?.pages).toEqual([fresh]);
    });
    await act(async () => {
      await again.result.current.fetchNextPage({ cancelRefetch: false });
    });
    expect(read.mock.calls.map(([cursor, , cached]) => [cursor, cached?.clientWindow])).toEqual([
      [null, undefined],
      ['next', old.clientWindow],
      ['last', old.clientWindow],
      [null, old.clientWindow],
      [null, old.clientWindow],
      ['next', fresh.clientWindow],
    ]);
    again.unmount();
    client.clear();
  });

  it('cancels a pending page without discarding its window or replaying on reconnect', async () => {
    const read = vi
      .fn<AuditReader>()
      .mockResolvedValueOnce(first)
      .mockImplementationOnce(
        (_, signal) =>
          new Promise((_, reject) => {
            signal.addEventListener(
              'abort',
              () => {
                reject(new DOMException('Aborted fixture read', 'AbortError'));
              },
              { once: true },
            );
          }),
      );
    const { client, mount } = setup(read);
    const { result, unmount } = mount();
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    act(() => {
      void result.current.fetchNextPage({ cancelRefetch: false });
    });
    await waitFor(() => {
      expect(result.current.isFetchingNextPage).toBe(true);
    });
    await act(async () => {
      await client.cancelQueries({ queryKey: ['audit'] });
    });
    expect(read.mock.calls[1]?.[1].aborted).toBe(true);
    expect(result.current.data?.pages).toEqual([first]);
    await act(async () => {
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
      await client.invalidateQueries({ queryKey: ['audit'] });
    });
    expect(read).toHaveBeenCalledTimes(2);
    expect(result.current.fetchStatus).toBe('idle');
    unmount();
    client.clear();
  });

  it('reads a new snapshot only when an expired view is explicitly reopened', async () => {
    const read = vi.fn<AuditReader>().mockResolvedValue(first);
    const { client, mount } = setup(read);
    client.setQueryDefaults(['audit'], { gcTime: 10 });
    const initial = mount();
    await waitFor(() => {
      expect(initial.result.current.isSuccess).toBe(true);
    });
    initial.unmount();
    await waitFor(() => {
      expect(client.getQueryCache().findAll({ queryKey: ['audit'] })).toHaveLength(0);
    });
    expect(read).toHaveBeenCalledTimes(1);
    const reopened = mount();
    await waitFor(() => {
      expect(reopened.result.current.isSuccess).toBe(true);
    });
    expect(read).toHaveBeenCalledTimes(2);
    reopened.unmount();
    client.clear();
  });
  it.each(['reader', 'workspace', 'tenant'] as const)(
    'does not reuse another %s snapshot after a session change',
    async (scope) => {
      const read = vi
        .fn<AuditReader>()
        .mockResolvedValueOnce(first)
        .mockRejectedValueOnce(new ApiProblem('ACCESS_DENIED', { status: 403 }));
      const { client, mount } = setup(read);
      const { result, unmount } = mount();
      await waitFor(() => {
        expect(result.current.isSuccess).toBe(true);
      });
      const changed: Me =
        scope === 'reader'
          ? { ...me, user: { ...me.user, id: '6f1c7a1e-0000-4000-8000-000000000070' } }
          : {
              ...me,
              workspace: {
                ...workspace,
                ...(scope === 'workspace'
                  ? { id: '6f1c7a1e-0000-4000-8000-000000000004' }
                  : { tenant_id: '6f1c7a1e-0000-4000-8000-000000000005' }),
              },
            };
      act(() => {
        client.setQueryData(sessionKey, changed);
      });
      await waitFor(() => {
        expect(result.current.isError).toBe(true);
      });
      expect(result.current.data).toBeUndefined();
      expect(read).toHaveBeenCalledTimes(2);
      unmount();
      client.clear();
    },
  );
  it('keeps a failed cached read through remount, focus, reconnect and invalidation', async () => {
    const read = vi.fn<AuditReader>().mockRejectedValue(new ApiProblem('NETWORK_UNAVAILABLE'));
    const { client, mount } = setup(read);
    const initial = mount();
    await waitFor(() => {
      expect(initial.result.current.isError).toBe(true);
    });
    initial.unmount();
    const again = mount();
    await act(async () => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
      await client.invalidateQueries({ queryKey: ['audit'] });
    });
    expect(read).toHaveBeenCalledTimes(1);
    expect(again.result.current.isError).toBe(true);
    again.unmount();
    client.clear();
  });

  it('does not queue an offline attempt for reconnect and explicitly recovers once', async () => {
    onlineManager.setOnline(false);
    const read = vi
      .fn<AuditReader>()
      .mockRejectedValueOnce(new ApiProblem('REQUEST_TIMEOUT'))
      .mockResolvedValue(first);
    const { client, mount } = setup(read);
    const { result, unmount } = mount();
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(result.current.fetchStatus).toBe('idle');
    await act(async () => {
      onlineManager.setOnline(true);
      await Promise.resolve();
    });
    expect(read).toHaveBeenCalledTimes(1);
    act(() => {
      result.current.readAgain();
      result.current.readAgain();
    });
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(read).toHaveBeenCalledTimes(2);
    unmount();
    client.clear();
  });

  it('retries only the failed next page and keeps existing pages after failure', async () => {
    const read = vi
      .fn<AuditReader>()
      .mockResolvedValueOnce(first)
      .mockRejectedValueOnce(new ApiProblem('STORAGE_COMMIT_OUTCOME_UNKNOWN', { status: 409 }))
      .mockResolvedValueOnce({ items: [], next_cursor: null });
    const { client, mount } = setup(read);
    const { result, unmount } = mount();
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    await act(async () => {
      await result.current.fetchNextPage({ cancelRefetch: false });
    });
    await waitFor(() => {
      expect(result.current.isFetchNextPageError).toBe(true);
    });
    expect(result.current.data?.pages).toEqual([first]);
    act(() => {
      result.current.readAgain();
    });
    await waitFor(() => {
      expect(result.current.data?.pages).toHaveLength(2);
    });
    expect(read.mock.calls.map(([cursor]) => cursor)).toEqual([null, 'next', 'next']);
    unmount();
    client.clear();
  });

  it('restarts an invalid cursor with one page and keeps history until that read succeeds', async () => {
    const second: AuditPage = { items: [], next_cursor: 'last' };
    const read = vi
      .fn<AuditReader>()
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce(second)
      .mockRejectedValueOnce(new ApiProblem('INVALID_CURSOR', { status: 400 }))
      .mockRejectedValueOnce(new ApiProblem('STORAGE_UNAVAILABLE', { status: 503 }))
      .mockResolvedValueOnce(first);
    const { client, mount } = setup(read);
    const { result, unmount } = mount();
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    await act(async () => {
      await result.current.fetchNextPage({ cancelRefetch: false });
    });
    await act(async () => {
      await result.current.fetchNextPage({ cancelRefetch: false });
    });
    act(() => {
      result.current.readAgain();
    });
    await waitFor(() => {
      expect(read).toHaveBeenCalledTimes(4);
    });
    await waitFor(() => {
      expect(result.current.fetchStatus).toBe('idle');
    });
    expect(result.current.data?.pages).toEqual([first, second]);
    act(() => {
      result.current.readAgain();
    });
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(result.current.data?.pages).toEqual([first]);
    expect(read.mock.calls.map(([cursor]) => cursor)).toEqual([null, 'next', 'last', null, null]);
    unmount();
    client.clear();
  });

  it.each([
    ['ACCESS_DENIED', 403],
    ['RESOURCE_NOT_FOUND', 404],
    ['FUTURE_REFUSAL', 403],
  ] as const)(
    'removes cached pages on current %s (%i) and refuses a retry',
    async (code, status) => {
      const read = vi
        .fn<AuditReader>()
        .mockResolvedValueOnce(first)
        .mockRejectedValueOnce(new ApiProblem(code, { status }));
      const { client, mount } = setup(read);
      const { result, unmount } = mount();
      await waitFor(() => {
        expect(result.current.isSuccess).toBe(true);
      });
      client.setQueryData(['audit', 'earlier-filter'], { pages: [first], pageParams: [null] });
      client.setQueryData(['resource-activity', 'earlier-resource'], {
        pages: [first],
        pageParams: [null],
      });
      client.setQueryData(['nodes'], 'unrelated');
      await act(async () => {
        await result.current.fetchNextPage({ cancelRefetch: false });
      });
      await waitFor(() => {
        expect(result.current.data).toBeUndefined();
      });
      expect(client.getQueryData(['audit', 'earlier-filter'])).toBeUndefined();
      expect(client.getQueryData(['resource-activity', 'earlier-resource'])).toBeUndefined();
      expect(client.getQueryData(['nodes'])).toBe('unrelated');
      act(() => {
        result.current.readAgain();
      });
      expect(read).toHaveBeenCalledTimes(2);
      unmount();
      client.clear();
    },
  );
});
