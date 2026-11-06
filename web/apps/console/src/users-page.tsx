// The accounts of the installation and the invitations that will become accounts. Built on the shared
// list pattern (docs/research/console-list-pattern.md) and docs/research/users-page.md.
import {
  createInvitation,
  getUser,
  isProblem,
  listInvitations,
  listUsers,
  resendInvitation,
  setUserStatus,
  type Invitation,
  type User,
  type UserStatus,
} from '@ownsolid/api';
import { isLocale, supportedLocales, useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  SavedViewsMenu,
  runtimeConfigQuery,
  useListView,
  useSession,
  useHashAction,
  useTypedFilter,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  buttonVariants,
  Card,
  CopyText,
  Detail,
  DetailList,
  Dialog,
  Drawer,
  EmptyState,
  Notice,
  ProblemNotice,
  SearchField,
  Segmented,
  SelectField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  TextField,
  Th,
  Tr,
  useToast,
  type StatusTone,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Ban, Building2, Check, RotateCw, UserPlus, Users } from 'lucide-react';
import { useRef, useState, type SubmitEvent } from 'react';
import { RevokeInvitation } from './revoke-invitation.tsx';

type Filter = 'all' | UserStatus;

const filters: readonly Filter[] = ['all', 'active', 'disabled'];

const definition: ListDefinition = {
  key: 'users',
  filters: {
    status: { kind: 'choice', values: ['active', 'disabled'] },
    email: { kind: 'text', maximum: 254 },
  },
  columns: [
    { id: 'name', label: 'users.column.name', locked: true },
    { id: 'status', label: 'users.column.status' },
    { id: 'access', label: 'users.column.access' },
    { id: 'language', label: 'users.column.language' },
    { id: 'created', label: 'users.column.created' },
  ],
};

const statuses: Record<UserStatus, { tone: StatusTone; label: TranslationKey }> = {
  active: { tone: 'good', label: 'users.status.active' },
  disabled: { tone: 'neutral', label: 'users.status.disabled' },
};

const usersKey = ['users'] as const;
const invitationsKey = ['invitations'] as const;

function languageName(locale: string, t: (key: TranslationKey) => string): string {
  return isLocale(locale) ? t(`locale.${locale}`) : locale;
}

/** One key per request until it succeeds, so a retry cannot act twice. */
function useKeys() {
  const keys = useRef(new Map<string, string>());
  return {
    take: (id: string) => {
      const key = keys.current.get(id) ?? crypto.randomUUID();
      keys.current.set(id, key);
      return key;
    },
    done: (id: string) => {
      keys.current.delete(id);
    },
  };
}

function InviteDialog({ onClose }: { onClose: () => void }) {
  const { t, locale } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const keys = useKeys();
  const [email, setEmail] = useState('');
  const [language, setLanguage] = useState<string>(locale);
  const invite = useMutation({
    mutationFn: () => {
      const address = email.trim();
      return createInvitation({ email: address, locale: language }, keys.take(address));
    },
    onSuccess: (invitation) => {
      keys.done(invitation.email);
      onClose();
      toast({ message: t('users.invite.done', { email: invitation.email }) });
      void queryClient.invalidateQueries({ queryKey: invitationsKey });
    },
  });
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (email.trim() !== '' && !invite.isPending) invite.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('users.invite.title')}
      description={t('users.invite.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button
            type="submit"
            form="invite"
            variant="primary"
            disabled={email.trim() === '' || invite.isPending}
          >
            {t('users.invite.submit')}
          </Button>
        </>
      }
    >
      <form id="invite" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
        {invite.error && isProblem(invite.error) ? (
          <ProblemNotice
            problem={invite.error}
            onRetry={() => {
              invite.mutate();
            }}
          />
        ) : null}
        <TextField
          label={t('users.invite.email')}
          name="email"
          type="email"
          autoComplete="off"
          maxLength={254}
          required
          value={email}
          onChange={(event) => {
            setEmail(event.target.value);
          }}
        />
        <SelectField
          label={t('users.invite.language')}
          hint={t('users.invite.language.hint')}
          value={language}
          options={supportedLocales.map((value) => ({ value, label: t(`locale.${value}`) }))}
          onChange={(event) => {
            setLanguage(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

const invitationTones: Record<Invitation['status'], StatusTone> = {
  pending: 'live',
  expired: 'warning',
  accepted: 'good',
  revoked: 'neutral',
};

/** Invitations not yet accepted, with their email's state, a resend and a revocation. */
function Invitations() {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.lookupPageSize ?? 100;
  const keys = useKeys();
  const [revoking, setRevoking] = useState<Invitation | null>(null);
  const read = (status: 'pending' | 'expired') =>
    ({
      queryKey: [...invitationsKey, status, limit],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        listInvitations({ limit, status }, { signal, background: true }),
      refetchInterval: config?.pollMilliseconds ?? false,
    }) as const;
  const pending = useQuery(read('pending'));
  const expired = useQuery(read('expired'));
  const resend = useMutation({
    mutationFn: (invitation: Invitation) =>
      resendInvitation(invitation.id, invitation.version, keys.take(invitation.id)),
    onSuccess: (_, invitation) => {
      keys.done(invitation.id);
      toast({ message: t('users.invitations.resent', { email: invitation.email }) });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: invitationsKey }),
  });
  const items = [...(pending.data?.items ?? []), ...(expired.data?.items ?? [])];
  const problem = pending.error ?? expired.error;
  if (items.length === 0 && !problem && !resend.error) return null;
  return (
    <section aria-labelledby="invitations" className="mb-3.5">
      {isProblem(problem) ? (
        <ProblemNotice
          className="mb-3"
          problem={problem}
          onRetry={() => {
            void pending.refetch();
            void expired.refetch();
          }}
        />
      ) : null}
      {resend.error && isProblem(resend.error) ? (
        <ProblemNotice
          className="mb-3"
          problem={resend.error}
          onRetry={() => {
            resend.mutate(resend.variables);
          }}
        />
      ) : null}
      {items.length > 0 ? (
        <Card className="px-4 py-3">
          <h2 id="invitations" className="m-0 mb-1.5 text-[13px] font-semibold text-text-2">
            {t('users.invitations.title')}
          </h2>
          <ul className="m-0 list-none p-0">
            {items.map((invitation) => (
              <li
                key={invitation.id}
                className="flex flex-wrap items-center justify-between gap-3 border-b border-border py-2 last:border-b-0"
              >
                <span className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="font-medium">{invitation.email}</span>
                  <StatusPill tone={invitationTones[invitation.status]}>
                    {t(`users.invitation.${invitation.status}`)}
                  </StatusPill>
                  <span className="text-[12.5px] text-text-2">
                    {invitation.status === 'expired'
                      ? t('users.invitations.expired', {
                          time: format.dateTime(invitation.expires_at),
                        })
                      : t('users.invitations.expires', {
                          time: format.dateTime(invitation.expires_at),
                        })}
                    {invitation.delivery?.state === 'failed'
                      ? ` · ${t('users.invitations.emailFailed')}`
                      : ''}
                  </span>
                </span>
                <span className="flex gap-2">
                  <Button
                    size="sm"
                    disabled={resend.isPending}
                    onClick={() => {
                      resend.mutate(invitation);
                    }}
                  >
                    <RotateCw aria-hidden />
                    {t('users.invitations.resend')}
                    <span className="sr-only">{invitation.email}</span>
                  </Button>
                  {invitation.status === 'pending' ? (
                    <Button
                      size="sm"
                      variant="danger"
                      onClick={() => {
                        setRevoking(invitation);
                      }}
                    >
                      {t('users.invitations.revoke')}
                      <span className="sr-only">{invitation.email}</span>
                    </Button>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
      {revoking ? (
        <RevokeInvitation
          invitation={revoking}
          email={revoking.email}
          defaultReason={t('users.invitations.revokeReason')}
          onClose={() => {
            setRevoking(null);
          }}
          onDone={() => {
            setRevoking(null);
          }}
        />
      ) : null}
    </section>
  );
}

function DisableDialog({
  user,
  pending,
  problem,
  onClose,
  onDisable,
}: {
  user: User;
  pending: boolean;
  problem: unknown;
  onClose: () => void;
  onDisable: (reason: string) => void;
}) {
  const { t } = useI18n();
  const [reason, setReason] = useState('');
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (reason.trim() !== '' && !pending) onDisable(reason.trim());
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('users.disable.title', { email: user.email })}
      description={t('users.disable.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button
            type="submit"
            form="disable-user"
            variant="danger"
            disabled={reason.trim() === '' || pending}
          >
            {t('users.disable.submit')}
          </Button>
        </>
      }
    >
      <form id="disable-user" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
        {isProblem(problem) ? <ProblemNotice problem={problem} /> : null}
        <TextField
          label={t('users.reason')}
          name="reason"
          autoComplete="off"
          maxLength={500}
          required
          hint={t('users.reason.hint')}
          value={reason}
          onChange={(event) => {
            setReason(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function UserDrawer({
  user,
  problem,
  onClose,
  onRetry,
}: {
  user: User | null;
  problem: unknown;
  onClose: () => void;
  onRetry: () => void;
}) {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const me = useSession();
  const [disabling, setDisabling] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: usersKey });
  const change = useMutation({
    mutationFn: (input: { target: User; status: UserStatus; reason: string }) =>
      setUserStatus(input.target.id, {
        status: input.status,
        version: input.target.version,
        reason: input.reason,
      }),
    onSettled: refresh,
  });
  /** Changes the status, then closes, since the undo toast sits behind this drawer. */
  const apply = (target: User, status: UserStatus, reason: string, done: string) => {
    change.mutate(
      { target, status, reason },
      {
        onSuccess: (updated) => {
          setDisabling(false);
          onClose();
          toast({
            message: done,
            undo: () =>
              setUserStatus(updated.id, {
                status: target.status,
                version: updated.version,
                reason: t('users.reason.undo'),
              }).finally(refresh),
          });
        },
      },
    );
  };
  const self = user !== null && me?.user.id === user.id;
  const state = user ? statuses[user.status] : null;
  return (
    <>
      <Drawer
        open={user !== null || Boolean(problem)}
        onClose={onClose}
        title={user ? user.display_name || user.email : t('users.detail.missing')}
        description={user?.display_name ? user.email : undefined}
        footer={
          user && !self ? (
            user.status === 'active' ? (
              <Button
                variant="danger"
                onClick={() => {
                  change.reset();
                  setDisabling(true);
                }}
              >
                <Ban aria-hidden />
                {t('users.disable.open')}
              </Button>
            ) : (
              <Button
                variant="primary"
                disabled={change.isPending}
                onClick={() => {
                  apply(
                    user,
                    'active',
                    t('users.reason.enable'),
                    t('users.enable.done', { email: user.email }),
                  );
                }}
              >
                <Check aria-hidden />
                {t('users.enable.open')}
              </Button>
            )
          ) : undefined
        }
      >
        {isProblem(problem) ? (
          <ProblemNotice className="mb-4" problem={problem} onRetry={onRetry} />
        ) : null}
        {!disabling && change.error && isProblem(change.error) ? (
          <ProblemNotice className="mb-4" problem={change.error} />
        ) : null}
        {self ? <Notice className="mb-4" title={t('users.detail.self')} /> : null}
        {user && state ? (
          <DetailList>
            <Detail label={t('users.column.status')}>
              <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
            </Detail>
            <Detail label={t('users.detail.email')}>
              <CopyText text={user.email} proportional />
            </Detail>
            <Detail label={t('users.column.access')}>
              {user.is_platform_admin ? t('users.access.admin') : t('users.access.member')}
            </Detail>
            <Detail label={t('users.detail.verified')}>
              {user.email_verified_at
                ? format.dateTime(user.email_verified_at)
                : t('users.detail.notVerified')}
            </Detail>
            <Detail label={t('users.column.language')}>{languageName(user.locale, t)}</Detail>
            <Detail label={t('users.column.created')}>{format.dateTime(user.created_at)}</Detail>
            <Detail label={t('users.detail.id')}>
              <CopyText text={user.id} />
            </Detail>
          </DetailList>
        ) : null}
      </Drawer>
      {user && disabling ? (
        <DisableDialog
          user={user}
          pending={change.isPending}
          problem={change.error}
          onClose={() => {
            setDisabling(false);
          }}
          onDisable={(reason) => {
            apply(user, 'disabled', reason, t('users.disable.done', { email: user.email }));
          }}
        />
      ) : null}
    </>
  );
}

function Rows({ items, list }: { items: readonly User[]; list: ListView }) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('users.title')}</caption>
        <thead>
          <tr>
            <Th>{t('users.column.name')}</Th>
            {shows('status') ? <Th>{t('users.column.status')}</Th> : null}
            {shows('access') ? <Th>{t('users.column.access')}</Th> : null}
            {shows('language') ? <Th>{t('users.column.language')}</Th> : null}
            {shows('created') ? <Th>{t('users.column.created')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((user) => {
            const state = statuses[user.status];
            return (
              <Tr key={user.id}>
                <Td>
                  <button
                    type="button"
                    aria-label={t('users.detail.open', { name: user.display_name || user.email })}
                    className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                    onClick={() => {
                      list.setOpenRow(user.id);
                    }}
                  >
                    {user.display_name || user.email}
                  </button>
                  <div className="text-[12px] text-text-2">{user.email}</div>
                </Td>
                {shows('status') ? (
                  <Td>
                    <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                  </Td>
                ) : null}
                {shows('access') ? (
                  <Td>
                    {user.is_platform_admin ? t('users.access.admin') : t('users.access.member')}
                  </Td>
                ) : null}
                {shows('language') ? <Td>{languageName(user.locale, t)}</Td> : null}
                {shows('created') ? (
                  <Td className="text-text-2">{format.dateTime(user.created_at)}</Td>
                ) : null}
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

/** `/users`: every account, newest first, and the invitations still waiting. */
export function UsersPage() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const list = useListView(definition);
  const status = list.state.filters.status as UserStatus | undefined;
  const email = list.state.filters.email;
  const [typed, setTyped] = useTypedFilter(list, 'email', config?.searchDebounceMilliseconds ?? 0);
  const [inviting, setInviting] = useState(useHashAction('invite'));
  const users = useInfiniteQuery({
    queryKey: [...usersKey, status, email],
    queryFn: ({ pageParam, signal }) =>
      listUsers(
        { limit, cursor: pageParam ?? undefined, status, emailPrefix: email?.toLowerCase() },
        { signal },
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = users.data?.pages.flatMap((page) => page.items) ?? [];
  // A link can name an account beyond the pages on screen, so it is read on its own.
  const opened = items.find((user) => user.id === list.openRow);
  const detail = useQuery({
    queryKey: [...usersKey, 'detail', list.openRow],
    queryFn: ({ signal }) => getUser(list.openRow ?? '', { signal }),
    enabled: list.openRow !== null && users.isSuccess && !opened,
    retry: false,
  });
  const openUser = opened ?? (list.openRow !== null ? detail.data : undefined) ?? null;
  const filtered = Object.keys(list.state.filters).length > 0;

  let body;
  if (users.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (!users.data) {
    body = isProblem(users.error) ? (
      <ProblemNotice
        problem={users.error}
        onRetry={() => {
          void users.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={Users}
          title={t('users.emptyFilter.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('users.emptyFilter.clear')}</Button>
            ) : undefined
          }
        >
          {t('users.emptyFilter.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        <Rows items={items} list={list} />
        {users.hasNextPage ? (
          <Button
            className="mt-4"
            disabled={users.isFetchingNextPage}
            onClick={() => {
              void users.fetchNextPage();
            }}
          >
            {t('users.loadMore')}
          </Button>
        ) : null}
      </>
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('users.title')}
          </h1>
          <p className="m-0 text-text-2">{t('users.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link to="/users/workspaces" className={buttonVariants()}>
            <Building2 aria-hidden />
            {t('users.workspaces')}
          </Link>
          <Button
            variant="primary"
            onClick={() => {
              setInviting(true);
            }}
          >
            <UserPlus aria-hidden />
            {t('users.invite.open')}
          </Button>
        </div>
      </div>
      <Invitations />
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('users.search')}
          placeholder={t('users.search')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <Segmented
          label={t('users.filter')}
          value={status ?? 'all'}
          options={filters.map((value) => ({ value, label: t(`users.filter.${value}`) }))}
          onChange={(value) => {
            list.setFilter('status', value === 'all' ? '' : value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      <UserDrawer
        user={openUser}
        problem={list.openRow !== null && !opened ? detail.error : null}
        onClose={() => {
          list.setOpenRow(null);
        }}
        onRetry={() => {
          void detail.refetch();
        }}
      />
      {inviting ? (
        <InviteDialog
          onClose={() => {
            setInviting(false);
          }}
        />
      ) : null}
    </>
  );
}
