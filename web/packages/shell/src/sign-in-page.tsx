import { isProblem, signIn, type Audience, type Me } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Brand,
  Button,
  LocaleSelect,
  Notice,
  ProblemNotice,
  Spinner,
  TextField,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useRef, useState, type SubmitEvent } from 'react';
import { sessionKey } from './session.ts';

/** Props for {@link SignInPage}. */
export interface SignInPageProps {
  /** The entrance being signed in to; it selects the page's wording. */
  audience: Audience;
  /** Called after the session is established, to continue to the requested page. */
  onSignedIn: (me: Me) => void;
}

interface FieldErrors {
  email?: string;
  password?: string;
}

/** Email and password sign-in for the console or the portal. */
export function SignInPage({ audience, onSignedIn }: SignInPageProps) {
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
    <main className="flex min-h-full flex-col items-center justify-center px-5 py-10">
      <div className="w-full max-w-[400px]">
        <Brand className="mb-8 justify-center" />
        <div className="rounded-card border border-border bg-surface p-7 shadow-2">
          <h1 className="m-0 text-[22px] font-bold tracking-[-0.02em]">{t('signIn.title')}</h1>
          <p className="mt-1 mb-6 text-text-2">{t(`signIn.subtitle.${audience}`)}</p>
          <form noValidate onSubmit={submit} className="flex flex-col gap-4">
            {error ? (
              isProblem(error, 'AUTH_INVALID_CREDENTIALS') ? (
                <Notice tone="critical" title={t('signIn.invalidCredentials.title')}>
                  {t('signIn.invalidCredentials.detail')}
                </Notice>
              ) : isProblem(error) ? (
                <ProblemNotice problem={error} />
              ) : (
                <Notice tone="critical" title={t('problem.UNKNOWN.title')} />
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
        </div>
        <div className="mt-5 flex justify-center">
          <LocaleSelect />
        </div>
      </div>
    </main>
  );
}
