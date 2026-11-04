import { isProblem, register, validateInvitation } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, Notice, Spinner, TextField } from '@ownsolid/ui';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';
import { AuthLayout, FailureNotice } from './auth-layout.tsx';
import { useLinkToken } from './link-token.ts';
import { NewPasswordFields, useNewPassword } from './password-fields.tsx';
import { signInPath } from './redirect.ts';

function Unusable() {
  const { t } = useI18n();
  return (
    <Notice tone="critical" title={t('register.unusable.title')}>
      {t('register.unusable.detail')}
    </Notice>
  );
}

function RegisterForm({ token, email }: { token: string; email: string }) {
  const { t } = useI18n();
  const [name, setName] = useState('');
  const [nameMissing, setNameMissing] = useState(false);
  const fields = useNewPassword();
  const create = useMutation({
    mutationFn: () => register({ token, display_name: name.trim(), password: fields.password }),
  });

  if (isProblem(create.error, 'TOKEN_UNUSABLE')) return <Unusable />;
  if (create.isSuccess) {
    return (
      <>
        <Notice title={t('register.done.title')}>{t('register.done.detail')}</Notice>
        <Button asChild variant="primary" className="mt-4 w-full">
          <Link to={signInPath}>{t('signIn.submit')}</Link>
        </Button>
      </>
    );
  }
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    const empty = name.trim() === '';
    setNameMissing(empty);
    if (fields.validate() && !empty) create.mutate();
  };
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      {create.error ? <FailureNotice error={create.error} /> : null}
      <TextField
        label={t('signIn.email')}
        type="email"
        autoComplete="username"
        readOnly
        value={email}
      />
      <TextField
        label={t('register.name')}
        name="name"
        autoComplete="name"
        required
        maxLength={100}
        value={name}
        error={nameMissing ? t('register.nameRequired') : undefined}
        onChange={(event) => {
          setName(event.target.value);
        }}
      />
      <NewPasswordFields state={fields} />
      <Button type="submit" variant="primary" className="mt-2 w-full" disabled={create.isPending}>
        {create.isPending ? <Spinner label={t('register.submit')} className="text-white" /> : null}
        {t('register.submit')}
      </Button>
    </form>
  );
}

/** `/register#token=...`: creates the account from an emailed invitation. */
export function RegisterPage() {
  const { t } = useI18n();
  const token = useLinkToken();
  const invitation = useQuery({
    queryKey: ['invitation', token],
    queryFn: ({ signal }) => validateInvitation(token ?? '', { signal }),
    enabled: token !== null,
    // The token must not outlive this page in the cache.
    gcTime: 0,
    refetchOnWindowFocus: false,
  });

  let content;
  if (!token || isProblem(invitation.error, 'TOKEN_UNUSABLE')) {
    content = <Unusable />;
  } else if (invitation.error) {
    content = (
      <FailureNotice
        error={invitation.error}
        onRetry={() => {
          void invitation.refetch();
        }}
      />
    );
  } else if (invitation.data) {
    content = <RegisterForm token={token} email={invitation.data.email} />;
  } else {
    content = <Spinner label={t('status.loading')} />;
  }
  return (
    <AuthLayout title={t('register.title')} subtitle={t('register.subtitle')}>
      {content}
    </AuthLayout>
  );
}
