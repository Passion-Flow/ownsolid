// Revoking an invitation. Its link cannot be brought back, so the address must be typed. The email
// page and the Users page share it.
import { isProblem, revokeInvitation } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { ProblemNotice, TextField, TypedConfirmation, useToast } from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

/** Lists that show an invitation, so each catches up after a revocation. */
const invitationLists = [['mail-deliveries'], ['invitations']] as const;

/** Asks for the address and a reason, then revokes the invitation at the version read. */
export function RevokeInvitation({
  invitation,
  email,
  defaultReason,
  onClose,
  onDone,
}: {
  invitation: { id: string; version: number };
  email: string;
  defaultReason: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState(defaultReason);
  const refresh = () =>
    Promise.all(invitationLists.map((queryKey) => queryClient.invalidateQueries({ queryKey })));
  const revoke = useMutation({
    mutationFn: () => revokeInvitation(invitation.id, invitation.version, reason.trim()),
    // The dialog closes at once; the lists catch up behind it.
    onSuccess: () => {
      onDone();
      toast({ message: t('mail.revoke.done', { email }) });
      void refresh();
    },
    // Someone acted on it first: the lists show what is true now.
    onError: refresh,
  });
  return (
    <TypedConfirmation
      title={t('mail.revoke.title')}
      consequence={t('mail.revoke.consequence', { email })}
      expected={email}
      confirmLabel={t('mail.revoke.submit')}
      pending={revoke.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') revoke.mutate();
      }}
    >
      <TextField
        label={t('mail.revoke.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        hint={t('mail.revoke.reason.hint')}
        value={reason}
        error={reason.trim() === '' ? t('mail.revoke.reason.required') : undefined}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {revoke.error && isProblem(revoke.error) ? <ProblemNotice problem={revoke.error} /> : null}
    </TypedConfirmation>
  );
}
