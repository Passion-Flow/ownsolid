// The signed-in person's own account: profile, password and sign-in providers.
import { changePassword, isProblem, updateProfile, type User } from '@ownsolid/api';
import { isLocale, supportedLocales, useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  SelectField,
  Spinner,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';
import { ProvidersCard } from './account-providers.tsx';
import { FailureNotice } from './auth-layout.tsx';
import { NewPasswordFields, useNewPassword } from './password-fields.tsx';
import { sessionKey, useSession } from './session.ts';

function ProfileCard({ user }: { user: User }) {
  const { t, format } = useI18n();
  const toast = useToast();
  const client = useQueryClient();
  const saved = user.display_name;
  const [name, setName] = useState(saved);
  const [baseline, setBaseline] = useState(saved);
  const [missing, setMissing] = useState(false);
  // A rename saved here or in another browser replaces what the field shows.
  if (saved !== baseline) {
    setBaseline(saved);
    setName(saved);
  }
  const trimmed = name.trim();
  const save = useMutation({
    mutationFn: () =>
      updateProfile({ display_name: trimmed, locale: user.locale, version: user.version }),
    onSuccess: (me) => {
      client.setQueryData(sessionKey, me);
      toast({ message: t('account.profile.saved') });
    },
    // The next save needs the account's current version.
    onError: (error) =>
      isProblem(error, 'VERSION_CONFLICT')
        ? client.invalidateQueries({ queryKey: sessionKey })
        : undefined,
  });
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMissing(trimmed === '');
    if (trimmed !== '') save.mutate();
  };
  return (
    <Card>
      <CardHeader title={t('account.profile.title')} />
      <CardBody>
        <dl className="m-0 mb-4 grid grid-cols-[130px_1fr] gap-y-2.5 text-[13.5px]">
          <dt className="text-text-3">{t('account.email')}</dt>
          <dd className="m-0 min-w-0 break-words">{user.email}</dd>
          <dt className="text-text-3">{t('account.created')}</dt>
          <dd className="m-0">{format.date(user.created_at)}</dd>
        </dl>
        <form noValidate onSubmit={submit} className="flex flex-col gap-4">
          {save.error ? (
            <FailureNotice
              error={save.error}
              onRetry={() => {
                save.mutate();
              }}
            />
          ) : null}
          <TextField
            label={t('account.profile.name')}
            name="display-name"
            autoComplete="name"
            maxLength={100}
            required
            value={name}
            error={missing ? t('account.profile.nameRequired') : undefined}
            onChange={(event) => {
              setName(event.target.value);
            }}
          />
          <div>
            <Button type="submit" variant="primary" disabled={save.isPending || trimmed === saved}>
              {save.isPending ? (
                <Spinner label={t('account.profile.save')} className="text-white" />
              ) : null}
              {t('account.profile.save')}
            </Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}

/** Chooses the language, which applies and saves at once, like the theme. */
function LanguageCard() {
  const { t, choice, setLocale } = useI18n();
  return (
    <Card>
      <CardHeader title={t('account.language.title')} />
      <CardBody>
        <SelectField
          label={t('account.language.label')}
          name="language"
          value={choice}
          hint={t('account.language.hint')}
          options={[
            { value: 'auto', label: t('account.language.auto') },
            ...supportedLocales.map((value) => ({ value, label: t(`locale.${value}`) })),
          ]}
          onChange={(event) => {
            const next = event.target.value;
            setLocale(isLocale(next) ? next : 'auto');
          }}
        />
      </CardBody>
    </Card>
  );
}

function PasswordCard({ email }: { email: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const client = useQueryClient();
  const fields = useNewPassword();
  const [current, setCurrent] = useState('');
  const [missing, setMissing] = useState(false);
  const change = useMutation({
    mutationFn: () => changePassword(current, fields.password),
    onSuccess: (me) => {
      client.setQueryData(sessionKey, me);
      setCurrent('');
      setMissing(false);
      fields.reset();
      toast({ message: t('account.password.changed') });
    },
  });
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMissing(current === '');
    if (fields.validate() && current !== '') change.mutate();
  };
  // A wrong current password belongs to its field; the rest get a notice with a next step.
  const wrongCurrent = isProblem(change.error, 'PASSWORD_CURRENT_INVALID');
  let currentError: string | undefined;
  if (missing) currentError = t('account.password.currentRequired');
  else if (wrongCurrent) currentError = t('problem.PASSWORD_CURRENT_INVALID.title');
  return (
    <Card>
      <CardHeader title={t('account.password.title')} subtitle={t('account.password.subtitle')} />
      <CardBody>
        <form noValidate onSubmit={submit} className="flex flex-col gap-4">
          {change.error && !wrongCurrent ? (
            <FailureNotice
              error={change.error}
              onRetry={() => {
                change.mutate();
              }}
            />
          ) : null}
          {/* Tells password managers which account this form changes. */}
          <input hidden readOnly type="text" autoComplete="username" value={email} />
          <TextField
            label={t('account.password.current')}
            type="password"
            name="current-password"
            autoComplete="current-password"
            maxLength={4096}
            required
            value={current}
            error={currentError}
            hint={
              <Link to="/forgot-password" className="font-[550] text-accent-ink hover:underline">
                {t('signIn.forgot')}
              </Link>
            }
            onChange={(event) => {
              setCurrent(event.target.value);
            }}
          />
          <NewPasswordFields state={fields} />
          <div>
            <Button type="submit" variant="primary" disabled={change.isPending}>
              {change.isPending ? (
                <Spinner label={t('account.password.submit')} className="text-white" />
              ) : null}
              {t('account.password.submit')}
            </Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}

/** `/account`: what a signed-in person changes about their own account. */
export function AccountPage() {
  const { t } = useI18n();
  const me = useSession();
  if (!me) return null;
  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('account.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('account.subtitle')}</p>
      <div className="grid items-start gap-4 xl:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-4">
          <ProfileCard user={me.user} />
          <PasswordCard email={me.user.email} />
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <LanguageCard />
          <ProvidersCard />
        </div>
      </div>
    </>
  );
}
