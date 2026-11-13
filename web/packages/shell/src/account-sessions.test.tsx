import type * as Api from '@ownsolid/api';
import {
  ApiProblem,
  changePassword,
  listBrowserSessions,
  revokeBrowserSession,
  signIn,
  type BrowserSession,
  type Me,
} from '@ownsolid/api';
import { I18nProvider } from '@ownsolid/i18n';
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { PreferencesProvider, ToastProvider } from '@ownsolid/ui';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClientProvider } from '@tanstack/react-query';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { StrictMode } from 'react';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { AccountPage } from './account-page.tsx';
import { createQueryClient, establishSession, sessionKey } from './session.ts';
import { SignInPage } from './sign-in-page.tsx';

vi.mock('@ownsolid/api', async (original) => ({
  ...(await original<typeof Api>()),
  listBrowserSessions: vi.fn(),
  revokeBrowserSession: vi.fn(),
  changePassword: vi.fn(),
  signIn: vi.fn(),
  listSsoBindings: vi.fn().mockResolvedValue([]),
  listSsoConnectors: vi.fn().mockResolvedValue([]),
}));
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
};
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
const read = vi.mocked(listBrowserSessions);

beforeAll(() => {
  Object.assign(HTMLDialogElement.prototype, {
    showModal(this: HTMLDialogElement) {
      this.open = true;
    },
    close(this: HTMLDialogElement) {
      this.open = false;
    },
  });
});
beforeEach(() => {
  vi.stubGlobal('scrollTo', vi.fn());
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })),
  );
  read.mockReset().mockResolvedValue({ items: [current, other], next_cursor: null });
  vi.mocked(revokeBrowserSession).mockReset().mockResolvedValue(undefined);
  vi.mocked(changePassword).mockReset().mockResolvedValue(me);
  vi.mocked(signIn)
    .mockReset()
    .mockResolvedValue({
      ...me,
      session: { absolute_expires_at: current.absolute_expires_at, idle_timeout_seconds: 1800 },
    });
});
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

function mount(locale: 'en' | 'zh-CN' = 'en', includeSignIn = false) {
  const client = createQueryClient({ onSignedOut: vi.fn() });
  client.setQueryData(sessionKey, me);
  client.setQueryData(['unrelated'], 'retained');
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <AccountPage />
        {includeSignIn ? <SignInPage audience="portal" onSignedIn={() => undefined} /> : null}
      </>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });
  const result = render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider locale={locale} catalog={locale === 'en' ? english : chinese}>
          <PreferencesProvider>
            <ToastProvider>
              <RouterProvider router={router} />
            </ToastProvider>
          </PreferencesProvider>
        </I18nProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
  return { ...result, client };
}

describe('shared Account screen integration', () => {
  it.each(['en', 'zh-CN'] as const)(
    'identifies the current row and requires the target ID in %s',
    async (locale) => {
      mount(locale);
      const text = locale === 'en' ? english : chinese;
      const section = await screen.findByRole('region', { name: text['account.sessions.title'] });
      await within(section).findByText(text['account.sessions.current']);
      const [firstRow, secondRow] = within(section).getAllByRole('listitem');
      if (!firstRow || !secondRow) throw new Error('Expected two session rows');
      expect(
        within(firstRow).getByRole('button', { name: text['account.sessions.end'] }),
      ).toHaveAttribute('aria-disabled', 'true');
      fireEvent.click(within(firstRow).getByRole('button'));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      await userEvent.click(within(secondRow).getByRole('button'));
      const dialog = screen.getByRole('dialog', { name: text['account.sessions.confirm.title'] });
      const confirm = within(dialog).getByRole('button', { name: text['account.sessions.end'] });
      expect(confirm).toBeDisabled();
      await userEvent.type(within(dialog).getByRole('textbox'), other.id);
      expect(confirm).toBeEnabled();
      expect(screen.getAllByRole('dialog')).toHaveLength(1);
      read.mockResolvedValue({ items: [current], next_cursor: null });
      await userEvent.click(confirm);
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(revokeBrowserSession).toHaveBeenCalledTimes(1);
      expect(
        within(section).queryByText(text['account.sessions.id'].replace('{id}', other.id)),
      ).not.toBeInTheDocument();
    },
  );

  it('starts a new actual Account session view after a same-account sign-in without clearing other cache', async () => {
    const { client } = mount();
    await screen.findByText(english['account.sessions.id'].replace('{id}', current.id));
    const pending = new Promise<never>(() => undefined);
    read.mockReturnValue(pending);
    act(() => {
      establishSession(client, me);
    });
    expect(
      screen.queryByText(english['account.sessions.id'].replace('{id}', current.id)),
    ).not.toBeInTheDocument();
    expect(client.getQueryData(['unrelated'])).toBe('retained');
  });

  it('replaces the current marker through the actual successful password form callback', async () => {
    const { client } = mount();
    await screen.findByText(english['account.sessions.id'].replace('{id}', current.id));
    read.mockResolvedValue({ items: [{ ...other, current: true }], next_cursor: null });
    await userEvent.type(screen.getByLabelText('Current password'), 'old sufficient password');
    await userEvent.type(screen.getByLabelText('New password'), 'new sufficient password');
    await userEvent.type(screen.getByLabelText('Confirm the password'), 'new sufficient password');
    await userEvent.click(screen.getByRole('button', { name: 'Change password' }));
    await waitFor(() =>
      expect(
        screen.queryByText(english['account.sessions.id'].replace('{id}', current.id)),
      ).not.toBeInTheDocument(),
    );
    await screen.findByText(english['account.sessions.id'].replace('{id}', other.id));
    expect(client.getQueryData(['unrelated'])).toBe('retained');
    expect(changePassword).toHaveBeenCalledWith(
      'old sufficient password',
      'new sufficient password',
    );
  });

  it('keeps the profile usable when session access is denied', async () => {
    read.mockRejectedValue(new ApiProblem('ACCESS_DENIED', { status: 403 }));
    mount();
    await screen.findByText(english['account.sessions.denied.title']);
    expect(screen.getByLabelText('Display name')).toBeEnabled();
    expect(
      screen.queryByText(english['account.sessions.id'].replace('{id}', current.id)),
    ).not.toBeInTheDocument();
  });

  it('runs the real same-account sign-in callback while an old Account view still exists', async () => {
    const { client } = mount('en', true);
    await screen.findByText(english['account.sessions.id'].replace('{id}', current.id));
    read.mockResolvedValue({ items: [{ ...other, current: true }], next_cursor: null });
    await userEvent.type(screen.getByLabelText('Email', { exact: true }), me.user.email);
    await userEvent.type(screen.getByLabelText('Password', { exact: true }), 'same account password');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => {
      expect(
        screen.queryByText(english['account.sessions.id'].replace('{id}', current.id)),
      ).not.toBeInTheDocument();
    });
    await screen.findByText(english['account.sessions.id'].replace('{id}', other.id));
    expect(signIn).toHaveBeenCalledWith({ email: me.user.email, password: 'same account password' });
    expect(client.getQueryData(['unrelated'])).toBe('retained');
  });

  it('ignores a late password result after sign-out and same-account remount', async () => {
    let finish: (value: Me) => void = () => {
      /* Assigned by the pending request. */
    };
    const pending = new Promise<Me>((resolve) => {
      finish = resolve;
    });
    vi.mocked(changePassword).mockReturnValue(pending);
    const { client } = mount();
    await screen.findByText(english['account.sessions.id'].replace('{id}', current.id));
    await userEvent.type(screen.getByLabelText('Current password'), 'old sufficient password');
    await userEvent.type(screen.getByLabelText('New password'), 'new sufficient password');
    await userEvent.type(screen.getByLabelText('Confirm the password'), 'new sufficient password');
    await userEvent.click(screen.getByRole('button', { name: 'Change password' }));
    await waitFor(() => {
      expect(changePassword).toHaveBeenCalledOnce();
    });
    act(() => {
      client.setQueryData(sessionKey, null);
    });
    await waitFor(() => {
      expect(screen.queryByLabelText('Display name')).not.toBeInTheDocument();
    });
    read.mockResolvedValue({ items: [{ ...other, current: true }], next_cursor: null });
    act(() => {
      establishSession(client, me);
    });
    await screen.findByText(english['account.sessions.id'].replace('{id}', other.id));
    await act(async () => {
      finish({ ...me, user: { ...me.user, version: 2 } });
      await pending;
    });
    expect(client.getQueryData(sessionKey)).toEqual(me);
    expect(
      screen.queryByText(english['account.sessions.id'].replace('{id}', current.id)),
    ).not.toBeInTheDocument();
  });

  it('keeps a failed confirmation and the failed fresh read separately actionable', async () => {
    mount();
    const section = await screen.findByRole('region', { name: 'Sessions' });
    await within(section).findByText(english['account.sessions.id'].replace('{id}', other.id));
    vi.mocked(revokeBrowserSession).mockRejectedValue(
      new ApiProblem('STORAGE_COMMIT_OUTCOME_UNKNOWN', { status: 409, requestId: 'end-unknown' }),
    );
    const otherRow = within(section).getAllByRole('listitem')[1];
    if (!otherRow) throw new Error('Expected the other session');
    await userEvent.click(within(otherRow).getByRole('button'));
    const dialog = screen.getByRole('dialog');
    await userEvent.type(within(dialog).getByRole('textbox'), other.id);
    await userEvent.click(within(dialog).getByRole('button', { name: 'End session' }));
    await within(section).findByText('end-unknown');
    read.mockRejectedValue(
      new ApiProblem('STORAGE_UNAVAILABLE', { status: 503, requestId: 'read-failed' }),
    );
    await userEvent.click(within(section).getByRole('button', { name: 'Read again' }));
    await within(section).findByText('read-failed');
    expect(within(section).getByText('end-unknown')).toBeInTheDocument();
    expect(within(section).getByRole('button', { name: 'Read again' })).toBeEnabled();
  });
});
