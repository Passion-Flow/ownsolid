import { useI18n } from '@ownsolid/i18n';
import { TextField } from '@ownsolid/ui';
import { useState } from 'react';

/** State of a new password and its confirmation. */
export interface NewPassword {
  password: string;
  setPassword: (value: string) => void;
  confirmation: string;
  setConfirmation: (value: string) => void;
  /** Shows errors from now on, and returns true when the two entries can be sent. */
  validate: () => boolean;
  showErrors: boolean;
}

/** Holds a new password. Policy is the server's; this only catches empty and mistyped entries. */
export function useNewPassword(): NewPassword {
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [showErrors, setShowErrors] = useState(false);
  return {
    password,
    setPassword,
    confirmation,
    setConfirmation,
    showErrors,
    validate: () => {
      setShowErrors(true);
      return password !== '' && password === confirmation;
    },
  };
}

/** The new password and confirmation inputs. */
export function NewPasswordFields({ state }: { state: NewPassword }) {
  const { t } = useI18n();
  const { password, confirmation, showErrors } = state;
  return (
    <>
      <TextField
        label={t('password.new')}
        type="password"
        name="new-password"
        autoComplete="new-password"
        maxLength={4096}
        required
        hint={t('password.hint')}
        value={password}
        error={showErrors && password === '' ? t('password.required') : undefined}
        onChange={(event) => {
          state.setPassword(event.target.value);
        }}
      />
      <TextField
        label={t('password.confirm')}
        type="password"
        name="confirm-password"
        autoComplete="new-password"
        maxLength={4096}
        required
        value={confirmation}
        error={showErrors && password !== confirmation ? t('password.mismatch') : undefined}
        onChange={(event) => {
          state.setConfirmation(event.target.value);
        }}
      />
    </>
  );
}
