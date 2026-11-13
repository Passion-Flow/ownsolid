import type * as Api from '@ownsolid/api';
import {
  ApiProblem,
  listBrowserSessions,
  revokeBrowserSession,
  type BrowserSession,
  type BrowserSessionPage,
  type Me,
} from '@ownsolid/api';
import { act, renderHook, waitFor } from '@testing-library/react';
import { focusManager, onlineManager, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createQueryClient, sessionKey } from './session.ts';
import { useAccountSessions } from './use-account-sessions.ts';

vi.mock('@ownsolid/api', async (original) => ({
  ...(await original<typeof Api>()),
  listBrowserSessions: vi.fn(),
  revokeBrowserSession: vi.fn(),
}));
const read = vi.mocked(listBrowserSessions);
const revoke = vi.mocked(revokeBrowserSession);
const current: BrowserSession = {
  id: '11111111-1111-4111-8111-111111111111',
  current: true,
  authentication_method: 'password',
  created_at: '2026-11-13T00:00:00Z',
  last_seen_at: '2026-11-13T00:05:00Z',
  absolute_expires_at: '2026-11-14T00:00:00Z',
};
const other: BrowserSession = {
  ...current,
  id: '22222222-2222-4222-8222-222222222222',
  current: false,
  authentication_method: 'sso',
};
const first: BrowserSessionPage = { items: [current, other], next_cursor: 'next' };
const me: Me = {
  audience: 'portal',
  workspace: null,
  user: {
    id: 'reader',
    email: 'reader@example.test',
    display_name: 'Reader',
    locale: 'en',
    status: 'active',
    is_platform_admin: false,
    email_verified_at: null,
    version: 1,
    created_at: current.created_at,
    updated_at: current.created_at,
  },
};

function setup(strict = false) {
  const client = createQueryClient({ onSignedOut: vi.fn() });
  client.setQueryData(sessionKey, me);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      {strict ? <StrictMode>{children}</StrictMode> : children}
    </QueryClientProvider>
  );
  const mount = (identity = me) => renderHook(() => useAccountSessions(identity), { wrapper });
  return { client, mount };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
beforeEach(() => {
  read.mockReset().mockResolvedValue(first);
  revoke.mockReset().mockResolvedValue(undefined);
});
afterEach(() => {
  focusManager.setFocused(undefined);
  onlineManager.setOnline(true);
});

describe('mounted account session view', () => {
  it('uses a background initial read, an explicit cursor read, and a one-page explicit refresh', async () => {
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    expect(read.mock.calls[0]?.[0]).toBeNull();
    expect(read.mock.calls[0]?.[1]?.background).toBe(true);
    expect(read.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal);
    read.mockResolvedValueOnce({ items: [], next_cursor: null });
    act(() => {
      view.result.current.loadMore();
    });
    await waitFor(() => {
      expect(read).toHaveBeenCalledTimes(2);
    });
    expect(read.mock.calls[1]?.[0]).toBe('next');
    await waitFor(() => {
      expect(view.result.current.busy).toBe(false);
    });
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(read).toHaveBeenCalledTimes(3);
    });
    expect(read.mock.calls[2]?.[0]).toBeNull();
    expect(read.mock.calls[2]?.[1]?.background).toBeUndefined();
    view.unmount();
  });

  it('does not retry, poll, or replay on focus, reconnect or cache invalidation', async () => {
    read.mockRejectedValue(new ApiProblem('NETWORK_UNAVAILABLE'));
    const { client, mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.readError).toBeTruthy();
    });
    await act(async () => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
      await client.invalidateQueries({ queryKey: ['own-browser-sessions'] });
    });
    expect(read).toHaveBeenCalledTimes(1);
    expect(revoke).not.toHaveBeenCalled();
    view.unmount();
  });

  it.each(['STORAGE_UNAVAILABLE', 'INVALID_CURSOR'])(
    'keeps earlier pages on %s and recovers only explicitly',
    async (code) => {
      const { mount } = setup();
      const view = mount();
      await waitFor(() => {
        expect(view.result.current.rows).toHaveLength(2);
      });
      read.mockRejectedValueOnce(
        new ApiProblem(code, { status: code === 'INVALID_CURSOR' ? 400 : 503 }),
      );
      act(() => {
        view.result.current.loadMore();
      });
      await waitFor(() => {
        expect(view.result.current.readError).toBeTruthy();
      });
      expect(view.result.current.rows).toEqual(first.items);
      act(() => {
        view.result.current.readAgain();
      });
      await waitFor(() => {
        expect(read).toHaveBeenCalledTimes(3);
      });
      expect(read.mock.calls[2]?.[0]).toBe(code === 'INVALID_CURSOR' ? null : 'next');
      view.unmount();
    },
  );

  it.each([401, 403])('hides cached rows and a selected target on a %s read', async (status) => {
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    act(() => {
      view.result.current.select(other);
    });
    read.mockRejectedValueOnce(
      new ApiProblem(status === 401 ? 'AUTH_REQUIRED' : 'ACCESS_DENIED', { status }),
    );
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(view.result.current.denied).toBe(true);
    });
    expect(view.result.current.rows).toEqual([]);
    expect(view.result.current.target).toBeNull();
    act(() => {
      view.result.current.confirm();
    });
    expect(revoke).not.toHaveBeenCalled();
    view.unmount();
  });

  it('never selects the authoritative current row or submits twice while pending', async () => {
    const pending = deferred<undefined>();
    revoke.mockReturnValue(pending.promise);
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    act(() => {
      view.result.current.select(current);
    });
    expect(view.result.current.target).toBeNull();
    act(() => {
      view.result.current.select(other);
    });
    act(() => {
      view.result.current.confirm();
      view.result.current.confirm();
    });
    await waitFor(() => {
      expect(revoke).toHaveBeenCalledTimes(1);
    });
    expect(view.result.current.rows).toHaveLength(2);
    read.mockResolvedValue({ items: [current], next_cursor: null });
    await act(async () => {
      pending.resolve(undefined);
      await pending.promise;
    });
    await waitFor(() => {
      expect(view.result.current.rows).toEqual([current]);
    });
    expect(view.result.current.ended).toBe(true);
    expect(read).toHaveBeenCalledTimes(2);
    view.unmount();
  });

  it('keeps an explicitly rolled-back target for a deliberate retry', async () => {
    revoke.mockRejectedValueOnce(new ApiProblem('STORAGE_COMMIT_ROLLED_BACK', { status: 503 }));
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    act(() => {
      view.result.current.select(other);
    });
    act(() => {
      view.result.current.confirm();
    });
    await waitFor(() => {
      expect(view.result.current.endError).toBeTruthy();
    });
    expect(view.result.current.target).toEqual(other);
    expect(view.result.current.mustRead).toBe(false);
    expect(revoke).toHaveBeenCalledTimes(1);
    act(() => {
      view.result.current.confirm();
    });
    await waitFor(() => {
      expect(revoke).toHaveBeenCalledTimes(2);
    });
    view.unmount();
  });

  it.each(['REQUEST_TIMEOUT', 'STORAGE_COMMIT_OUTCOME_UNKNOWN', 'RESOURCE_NOT_FOUND'])(
    'requires a fresh read after %s and does not claim success',
    async (code) => {
      revoke.mockRejectedValueOnce(
        new ApiProblem(code, {
          status: code === 'RESOURCE_NOT_FOUND' ? 404 : code === 'REQUEST_TIMEOUT' ? 0 : 409,
        }),
      );
      const { mount } = setup();
      const view = mount();
      await waitFor(() => {
        expect(view.result.current.rows).toHaveLength(2);
      });
      act(() => {
        view.result.current.select(other);
      });
      act(() => {
        view.result.current.confirm();
      });
      await waitFor(() => {
        expect(view.result.current.mustRead).toBe(true);
      });
      expect(view.result.current.target).toBeNull();
      expect(view.result.current.ended).toBe(false);
      act(() => {
        view.result.current.select(other);
        view.result.current.confirm();
      });
      expect(revoke).toHaveBeenCalledTimes(1);
      expect(read).toHaveBeenCalledTimes(1);
      read.mockResolvedValue({ items: [current], next_cursor: null });
      act(() => {
        view.result.current.readAgain();
      });
      await waitFor(() => {
        expect(view.result.current.mustRead).toBe(false);
      });
      expect(view.result.current.ended).toBe(false);
      expect(revoke).toHaveBeenCalledTimes(1);
      view.unmount();
    },
  );

  it('isolates a same-account remount and a late previous page without clearing unrelated cache', async () => {
    const late = deferred<BrowserSessionPage>();
    const { client, mount } = setup();
    client.setQueryData(['unrelated'], 'kept');
    const old = mount();
    await waitFor(() => {
      expect(old.result.current.rows).toHaveLength(2);
    });
    read.mockReturnValueOnce(late.promise);
    act(() => {
      old.result.current.loadMore();
    });
    await waitFor(() => {
      expect(read).toHaveBeenCalledTimes(2);
    });
    old.unmount();
    read.mockResolvedValue({ items: [{ ...other, current: true }], next_cursor: null });
    const fresh = mount();
    expect(fresh.result.current.rows).toEqual([]);
    await waitFor(() => {
      expect(fresh.result.current.rows[0]?.id).toBe(other.id);
    });
    await act(async () => {
      late.resolve({ items: [current], next_cursor: null });
      await late.promise;
    });
    expect(fresh.result.current.rows).toEqual([{ ...other, current: true }]);
    expect(client.getQueryData(['unrelated'])).toBe('kept');
    fresh.unmount();
  });

  it('preserves history when an invalid-cursor restart also fails', async () => {
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    read.mockRejectedValueOnce(new ApiProblem('INVALID_CURSOR', { status: 400 }));
    act(() => {
      view.result.current.loadMore();
    });
    await waitFor(() => {
      expect(view.result.current.readError).toBeTruthy();
    });
    read.mockRejectedValueOnce(new ApiProblem('STORAGE_UNAVAILABLE', { status: 503 }));
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(view.result.current.readError).toMatchObject({ code: 'STORAGE_UNAVAILABLE' });
    });
    expect(view.result.current.rows).toEqual(first.items);
    view.unmount();
  });

  it('hides rows and the target after a forbidden end', async () => {
    revoke.mockRejectedValueOnce(new ApiProblem('ACCESS_DENIED', { status: 403 }));
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    act(() => {
      view.result.current.select(other);
    });
    act(() => {
      view.result.current.confirm();
    });
    await waitFor(() => {
      expect(view.result.current.denied).toBe(true);
    });
    expect(view.result.current.rows).toEqual([]);
    expect(view.result.current.target).toBeNull();
    view.unmount();
  });

  it('does not reveal refused cached rows when a fresh authorization check also fails', async () => {
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    read.mockRejectedValueOnce(new ApiProblem('ACCESS_DENIED', { status: 403 }));
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(view.result.current.denied).toBe(true);
    });
    read.mockRejectedValueOnce(new ApiProblem('STORAGE_UNAVAILABLE', { status: 503 }));
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(view.result.current.readError).toMatchObject({ code: 'STORAGE_UNAVAILABLE' });
    });
    expect(view.result.current.rows).toEqual([]);
    expect(view.result.current.target).toBeNull();
    act(() => {
      view.result.current.readAgain();
    });
    await waitFor(() => {
      expect(view.result.current.denied).toBe(false);
    });
    expect(view.result.current.rows).toEqual(first.items);
    view.unmount();
  });

  it('honors Retry-After on explicit rollback without scheduling a write', async () => {
    const { mount } = setup();
    const view = mount();
    await waitFor(() => {
      expect(view.result.current.rows).toHaveLength(2);
    });
    vi.useFakeTimers();
    try {
      revoke.mockRejectedValueOnce(
        new ApiProblem('STORAGE_COMMIT_ROLLED_BACK', { status: 503, retryAfterSeconds: 2 }),
      );
      act(() => {
        view.result.current.select(other);
      });
      await act(async () => {
        view.result.current.confirm();
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(view.result.current.waiting).toBe(true);
      act(() => {
        view.result.current.confirm();
      });
      expect(revoke).toHaveBeenCalledTimes(1);
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(view.result.current.waiting).toBe(false);
      expect(revoke).toHaveBeenCalledTimes(1);
      act(() => {
        view.result.current.confirm();
      });
      await act(async () => vi.advanceTimersByTimeAsync(0));
      expect(revoke).toHaveBeenCalledTimes(2);
    } finally {
      view.unmount();
      vi.useRealTimers();
    }
  });

  it('survives StrictMode cleanup and ignores an old write refusal after a new login', async () => {
    const pending = deferred<undefined>();
    revoke.mockReturnValue(pending.promise);
    const { client, mount } = setup(true);
    const old = mount();
    await waitFor(() => {
      expect(old.result.current.rows).toHaveLength(2);
    });
    act(() => {
      old.result.current.select(other);
    });
    act(() => {
      old.result.current.confirm();
    });
    await waitFor(() => {
      expect(revoke).toHaveBeenCalledTimes(1);
    });
    old.unmount();
    const fresh = mount();
    await waitFor(() => {
      expect(fresh.result.current.rows).toHaveLength(2);
    });
    await act(async () => {
      pending.reject(new ApiProblem('AUTH_REQUIRED', { status: 401 }));
      await Promise.resolve();
    });
    expect(client.getQueryData(sessionKey)).toEqual(me);
    expect(fresh.result.current.denied).toBe(false);
    fresh.unmount();
  });
});
