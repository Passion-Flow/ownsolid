// What the console shows about an account and what can be done to it, in its drawer and on its own page.
import { isProblem, setUserStatus, type User, type UserStatus } from '@ownsolid/api';
import { isLocale, useI18n, type TranslationKey } from '@ownsolid/i18n';
import { useSession } from '@ownsolid/shell';
import {
  Button,
  CopyText,
  Detail,
  DetailList,
  Dialog,
  ProblemNotice,
  StatusPill,
  TextField,
  useToast,
  type StatusTone,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Ban, Check } from 'lucide-react';
import { useState, type ReactNode, type SubmitEvent } from 'react';

/** Everything under this key follows a change to an account. */
export const usersKey = ['users'] as const;

export const statuses: Record<UserStatus, { tone: StatusTone; label: TranslationKey }> = {
  active: { tone: 'good', label: 'users.status.active' },
  disabled: { tone: 'neutral', label: 'users.status.disabled' },
};

export function languageName(locale: string, t: (key: TranslationKey) => string): string {
  return isLocale(locale) ? t(`locale.${locale}`) : locale;
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

/** An account's details. */
export function UserFacts({ user }: { user: User }) {
  const { t, format } = useI18n();
  const state = statuses[user.status];
  return (
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
  );
}

/** The parts of an account's actions, for its surface to place. */
export interface UserActions {
  /** The buttons; nothing on a person's own account. */
  buttons: ReactNode;
  notices: ReactNode;
  dialogs: ReactNode;
  /** This is the signed-in person's own account. */
  self: boolean;
}

/** The actions on an account. `onChanged` runs before the toast that offers undo. */
export function useUserActions(user: User | null, onChanged: () => void): UserActions {
  const { t } = useI18n();
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
  /** Changes the status, then lets the surface make way for the undo toast. */
  const apply = (target: User, status: UserStatus, reason: string, done: string) => {
    change.mutate(
      { target, status, reason },
      {
        onSuccess: (updated) => {
          setDisabling(false);
          onChanged();
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
  return {
    buttons:
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
      ) : null,
    notices: (
      <>
        {!disabling && change.error && isProblem(change.error) ? (
          <ProblemNotice className="mb-4" problem={change.error} />
        ) : null}
      </>
    ),
    dialogs: (
      <>
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
    ),
    self,
  };
}
