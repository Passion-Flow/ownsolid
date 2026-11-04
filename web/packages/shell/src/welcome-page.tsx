import { useI18n } from '@ownsolid/i18n';
import { Brand, Button, LocaleSelect, ProblemNotice, Spinner } from '@ownsolid/ui';
import { isProblem } from '@ownsolid/api';
import { useRouter } from '@tanstack/react-router';
import { signInPath } from './redirect.ts';
import { useSession } from './session.ts';
import { useSignOut } from './sign-out.ts';

/** The signed-in landing page: who is signed in, the workspace, and sign-out. */
export function WelcomePage() {
  const { t } = useI18n();
  const router = useRouter();
  const me = useSession();
  const signOut = useSignOut(() => {
    void router.navigate({ to: signInPath, replace: true });
  });
  if (!me) return null;
  const name = me.user.display_name || me.user.email;
  return (
    <div className="min-h-full">
      <header className="flex h-[58px] items-center gap-3 border-b border-border px-6">
        <Brand />
        <span className="text-text-3">{t(`app.${me.audience}`)}</span>
        <div className="ml-auto flex items-center gap-2">
          <LocaleSelect />
          <Button
            variant="ghost"
            disabled={signOut.isPending}
            onClick={() => {
              signOut.mutate();
            }}
          >
            {signOut.isPending ? <Spinner label={t('signOut.pending')} /> : null}
            {t('signOut.action')}
          </Button>
        </div>
      </header>
      <main className="mx-auto max-w-[1320px] px-7 pt-7 pb-16">
        {signOut.error && isProblem(signOut.error) ? (
          <ProblemNotice
            className="mb-5"
            problem={signOut.error}
            onRetry={() => {
              signOut.mutate();
            }}
          />
        ) : null}
        <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
          {t('welcome.title', { name })}
        </h1>
        <p className="m-0 text-text-2">{t('welcome.signedInAs', { email: me.user.email })}</p>
        <dl className="mt-6 grid max-w-[560px] grid-cols-[130px_1fr] gap-y-2.5 rounded-card border border-border bg-surface p-5 text-[13.5px] shadow-1">
          <dt className="text-text-3">{t('welcome.workspace')}</dt>
          <dd className="m-0">{me.workspace ? me.workspace.name : t('welcome.noWorkspace')}</dd>
          {me.workspace ? (
            <>
              <dt className="text-text-3">{t('welcome.role')}</dt>
              <dd className="m-0">{t(`role.${me.workspace.role}`)}</dd>
            </>
          ) : null}
          <dt className="text-text-3">{t('welcome.access')}</dt>
          <dd className="m-0">
            {me.user.is_platform_admin ? t('welcome.platformAdmin') : t('welcome.member')}
          </dd>
        </dl>
      </main>
    </div>
  );
}
