import { isProblem, signIn, type Audience, type Me } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, Notice, Spinner, TextField } from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useRef, useState, type SubmitEvent } from 'react';
import { AuthLayout, FailureNotice } from './auth-layout.tsx';
import { sessionKey } from './session.ts';
import { SsoButtons } from './sso.tsx';

/** Props for {@link SignInPage}. */
export interface SignInPageProps {
  /** The entrance being signed in to; it selects the page's wording. */
  audience: Audience;
  /** The visitor was sent here because their session ended, so the page says so. */
  sessionEnded?: boolean | undefined;
  /** Called after the session is established, to continue to the requested page. */
  onSignedIn: (me: Me) => void;
}

interface FieldErrors {
  email?: string;
  password?: string;
}

/** Email and password sign-in for the console or the portal. */
export function SignInPage({ audience, sessionEnded = false, onSignedIn }: SignInPageProps) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const passwordInput = useRef<HTMLInputElement>(null);

  const mutation = useMutation({
    mutationFn: () => signIn({ email: email.trim(), password }),
    onSuccess: (result) => {
      const me: Me = { user: result.user, workspace: result.workspace, audience: result.audience };
      queryClient.setQueryData(sessionKey, me);
      onSignedIn(me);
    },
    onError: (error) => {
      if (isProblem(error, 'AUTH_INVALID_CREDENTIALS')) {
        setPassword('');
        passwordInput.current?.focus();
      }
    },
  });

  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    const errors: FieldErrors = {};
    if (email.trim() === '') errors.email = t('signIn.emailRequired');
    if (password === '') errors.password = t('signIn.passwordRequired');
    setFieldErrors(errors);
    if (errors.email ?? errors.password) return;
    mutation.mutate();
  };

  const error = mutation.error;
  return (
    <AuthLayout title={t('signIn.title')} subtitle={t(`signIn.subtitle.${audience}`)}>
      <form noValidate onSubmit={submit} className="flex flex-col gap-4">
        {sessionEnded && !error ? (
          <Notice title={t('problem.AUTH_SESSION_EXPIRED.title')}>
            {t('signIn.sessionEnded')}
          </Notice>
        ) : null}
        {error ? (
          isProblem(error, 'AUTH_INVALID_CREDENTIALS') ? (
            <Notice tone="critical" title={t('signIn.invalidCredentials.title')}>
              {t('signIn.invalidCredentials.detail')}
            </Notice>
          ) : (
            <FailureNotice error={error} />
          )
        ) : null}
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
          error={fieldErrors.email}
          onChange={(event) => {
            setEmail(event.target.value);
          }}
        />
        <TextField
          ref={passwordInput}
          label={t('signIn.password')}
          type="password"
          name="password"
          autoComplete="current-password"
          maxLength={4096}
          required
          value={password}
          error={fieldErrors.password}
          onChange={(event) => {
            setPassword(event.target.value);
          }}
        />
        <Button
          type="submit"
          variant="primary"
          className="mt-2 w-full"
          disabled={mutation.isPending}
        >
          {mutation.isPending ? (
            <Spinner label={t('signIn.submitting')} className="text-white" />
          ) : null}
          {mutation.isPending ? t('signIn.submitting') : t('signIn.submit')}
        </Button>
      </form>
      <p className="m-0 mt-4 text-center text-[13px]">
        <Link to="/forgot-password" className="font-[550] text-accent-ink hover:underline">
          {t('signIn.forgot')}
        </Link>
      </p>
      <SsoButtons />
    </AuthLayout>
  );
}
