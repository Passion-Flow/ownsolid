// Asking for a password reset, and setting the new password from the emailed link.
import { isProblem, requestPasswordReset, resetPassword } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, Notice, Spinner, TextField } from '@ownsolid/ui';
import { useMutation } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';
import { AuthLayout, FailureNotice } from './auth-layout.tsx';
import { useLinkToken } from './link-token.ts';
import { NewPasswordFields, useNewPassword } from './password-fields.tsx';
import { signInPath } from './redirect.ts';

function BackToSignIn() {
  const { t } = useI18n();
  return (
    <Button asChild className="mt-4 w-full">
      <Link to={signInPath}>{t('password.backToSignIn')}</Link>
    </Button>
  );
}

/** `/forgot-password`: the answer never reveals whether the address has an account. */
export function ForgotPasswordPage() {
  const { t } = useI18n();
  const [email, setEmail] = useState('');
  const [missing, setMissing] = useState(false);
  const request = useMutation({ mutationFn: () => requestPasswordReset(email.trim()) });

  if (request.isSuccess) {
    return (
      <AuthLayout title={t('password.forgot.title')}>
        <Notice title={t('password.forgot.sent.title')}>{t('password.forgot.sent.detail')}</Notice>
        <BackToSignIn />
      </AuthLayout>
    );
  }
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    const empty = email.trim() === '';
    setMissing(empty);
    if (!empty) request.mutate();
  };
  return (
    <AuthLayout title={t('password.forgot.title')} subtitle={t('password.forgot.subtitle')}>
      <form noValidate onSubmit={submit} className="flex flex-col gap-4">
        {request.error ? <FailureNotice error={request.error} /> : null}
        <TextField
          label={t('signIn.email')}
          type="email"
          name="email"
          autoComplete="username"
          inputMode="email"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={320}
          required
          value={email}
          error={missing ? t('signIn.emailRequired') : undefined}
          onChange={(event) => {
            setEmail(event.target.value);
          }}
        />
        <Button type="submit" variant="primary" className="w-full" disabled={request.isPending}>
          {request.isPending ? (
            <Spinner label={t('password.forgot.submit')} className="text-white" />
          ) : null}
          {t('password.forgot.submit')}
        </Button>
      </form>
      <BackToSignIn />
    </AuthLayout>
  );
}

function UnusableResetLink() {
  const { t } = useI18n();
  return (
    <>
      <Notice tone="critical" title={t('password.reset.unusable.title')}>
        {t('password.reset.unusable.detail')}
      </Notice>
      <Button asChild variant="primary" className="mt-4 w-full">
        <Link to="/forgot-password">{t('password.reset.requestNew')}</Link>
      </Button>
    </>
  );
}

/** `/reset-password#token=...`: sets a new password from the emailed link. */
export function ResetPasswordPage() {
  const { t } = useI18n();
  const token = useLinkToken();
  const fields = useNewPassword();
  const reset = useMutation({
    mutationFn: (value: string) => resetPassword(value, fields.password),
  });

  if (!token || isProblem(reset.error, 'TOKEN_UNUSABLE')) {
    return (
      <AuthLayout title={t('password.reset.title')}>
        <UnusableResetLink />
      </AuthLayout>
    );
  }
  if (reset.isSuccess) {
    return (
      <AuthLayout title={t('password.reset.title')}>
        <Notice title={t('password.reset.done.title')}>{t('password.reset.done.detail')}</Notice>
        <BackToSignIn />
      </AuthLayout>
    );
  }
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (fields.validate()) reset.mutate(token);
  };
  return (
    <AuthLayout title={t('password.reset.title')}>
      <form noValidate onSubmit={submit} className="flex flex-col gap-4">
        {reset.error ? <FailureNotice error={reset.error} /> : null}
        <NewPasswordFields state={fields} />
        <Button type="submit" variant="primary" className="mt-2 w-full" disabled={reset.isPending}>
          {reset.isPending ? (
            <Spinner label={t('password.reset.submit')} className="text-white" />
          ) : null}
          {t('password.reset.submit')}
        </Button>
      </form>
    </AuthLayout>
  );
}
