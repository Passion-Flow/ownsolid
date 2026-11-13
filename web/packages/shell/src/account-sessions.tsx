import { isProblem, type Me } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Notice,
  ProblemNotice,
  Skeleton,
  TypedConfirmation,
} from '@ownsolid/ui';
import { LogIn } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useAccountSessions } from './use-account-sessions.ts';
import { useSessionGeneration } from './session.ts';

function LoadingRows() {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => {
      setVisible(true);
    }, 300);
    return () => {
      clearTimeout(timer);
    };
  }, []);
  return visible ? (
    <div className="flex flex-col gap-3 py-4">
      <Skeleton className="h-20 w-full" />
      <Skeleton className="h-20 w-full" />
    </div>
  ) : null;
}

/** Account security on both entrances; all facts and the current marker come from the server. */
export function AccountSessions({ me }: { me: Me }) {
  const generation = useSessionGeneration();
  return <SessionsView key={`${me.audience}:${me.user.id}:${generation}`} me={me} />;
}

function SessionsView({ me }: { me: Me }) {
  const { t, format } = useI18n();
  const sessions = useAccountSessions(me);
  const error = sessions.endError ?? sessions.readError;
  const uncertain = sessions.mustRead && !sessions.ended;
  const target = sessions.target;
  const failure = error ? (
    isProblem(error) ? (
      <ProblemNotice
        problem={error}
        title={
          sessions.denied
            ? t('account.sessions.denied.title')
            : uncertain
              ? t('account.sessions.unknown.title')
              : undefined
        }
        detail={
          sessions.denied
            ? t('account.sessions.denied.detail')
            : uncertain
              ? t('account.sessions.unknown.detail')
              : undefined
        }
        recovery={
          target || (sessions.readError && sessions.endError)
            ? null
            : {
                label: t(
                  isProblem(error, 'INVALID_CURSOR')
                    ? 'account.sessions.restart'
                    : 'account.sessions.readAgain',
                ),
                onClick: sessions.readAgain,
                disabled: sessions.busy || sessions.waiting,
              }
        }
      />
    ) : (
      <Notice
        tone="critical"
        title={t('account.sessions.unknown.title')}
        actions={
          <Button disabled={sessions.busy} onClick={sessions.readAgain}>
            {t('account.sessions.readAgain')}
          </Button>
        }
      >
        {t('account.sessions.unknown.detail')}
      </Notice>
    )
  ) : null;
  return (
    <Card className="mt-4" aria-label={t('account.sessions.title')}>
      <CardHeader
        title={t('account.sessions.title')}
        actions={
          <Button
            size="sm"
            disabled={sessions.busy || sessions.waiting || !!target || !!error}
            onClick={sessions.readAgain}
          >
            {t('account.sessions.refresh')}
          </Button>
        }
      />
      <CardBody>
        <p className="mt-0 text-text-2">{t(`account.sessions.scope.${me.audience}`)}</p>
        {sessions.ended ? <p role="status">{t('account.sessions.ended')}</p> : null}
        {!target ? failure : null}
        {!target && sessions.endError && isProblem(sessions.readError) ? (
          <ProblemNotice
            problem={sessions.readError}
            title={t('account.sessions.readFailed')}
            recovery={{
              label: t('account.sessions.readAgain'),
              onClick: sessions.readAgain,
              disabled: sessions.busy || sessions.waiting,
            }}
          />
        ) : null}
        {sessions.readError && sessions.partial && !sessions.denied && !sessions.notReady ? (
          <p className="text-text-2">{t('account.sessions.partial')}</p>
        ) : null}
        {sessions.notReady ? (
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <Button aria-disabled="true">{t('account.sessions.end')}</Button>
            <span>{t('account.sessions.notReady')}</span>
          </div>
        ) : null}
        {sessions.pending ? (
          <div aria-busy="true">
            <LoadingRows />
          </div>
        ) : null}
        {!sessions.pending && !error && sessions.rows.length === 0 ? (
          <EmptyState
            icon={LogIn}
            title={t('account.sessions.empty.title')}
            action={<Button onClick={sessions.readAgain}>{t('account.sessions.refresh')}</Button>}
          >
            {t('account.sessions.empty.detail')}
          </EmptyState>
        ) : null}
        {sessions.rows.length > 0 ? (
          <ul className="m-0 list-none divide-y divide-border p-0" aria-busy={sessions.busy}>
            {sessions.rows.map((row) => (
              <li
                key={row.id}
                className="flex flex-col gap-3 py-4 sm:flex-row sm:items-start sm:justify-between"
              >
                <div className="min-w-0 flex-1">
                  <p className="m-0 flex flex-wrap gap-2 font-semibold">
                    {t(`account.sessions.method.${row.authentication_method}`)}
                    {row.current ? (
                      <span className="rounded-control bg-accent-soft px-2 text-accent-ink">
                        {t('account.sessions.current')}
                      </span>
                    ) : null}
                  </p>
                  <dl className="mb-0 mt-2 grid grid-cols-1 gap-x-6 gap-y-2 text-[13px] sm:grid-cols-3">
                    <div>
                      <dt className="text-text-3">{t('account.sessions.created')}</dt>
                      <dd className="m-0">
                        <time dateTime={row.created_at}>{format.dateTime(row.created_at)}</time>
                      </dd>
                    </div>
                    <div>
                      <dt className="text-text-3">{t('account.sessions.seen')}</dt>
                      <dd className="m-0">
                        <time dateTime={row.last_seen_at}>{format.dateTime(row.last_seen_at)}</time>
                      </dd>
                    </div>
                    <div>
                      <dt className="text-text-3">{t('account.sessions.expires')}</dt>
                      <dd className="m-0">
                        <time dateTime={row.absolute_expires_at}>
                          {format.dateTime(row.absolute_expires_at)}
                        </time>
                      </dd>
                    </div>
                  </dl>
                  <p className="mb-0 mt-2 break-all text-[12px] text-text-3">
                    {t('account.sessions.id', { id: row.id })}
                  </p>
                </div>
                <div className="flex shrink-0 flex-col items-start gap-1 sm:max-w-48 sm:items-end">
                  <Button
                    size="sm"
                    variant="danger"
                    aria-disabled={row.current || sessions.blocked}
                    aria-describedby={row.current ? `session-${row.id}-reason` : undefined}
                    onClick={() => {
                      sessions.select(row);
                    }}
                  >
                    {t('account.sessions.end')}
                  </Button>
                  {row.current ? (
                    <p
                      id={`session-${row.id}-reason`}
                      className="m-0 text-[12px] text-text-3 sm:text-right"
                    >
                      {t('account.sessions.signOut')}
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        ) : null}
        {!sessions.pending &&
        !error &&
        !sessions.hasMore &&
        sessions.rows.length === 1 &&
        sessions.rows[0]?.current ? (
          <p className="mb-0 text-text-2">{t('account.sessions.onlyCurrent')}</p>
        ) : null}
        {sessions.hasMore ? (
          <Button disabled={sessions.blocked || !!target} onClick={sessions.loadMore}>
            {t('account.sessions.more')}
          </Button>
        ) : null}
        {target ? (
          <TypedConfirmation
            key={target.id}
            title={t('account.sessions.confirm.title')}
            consequence={t('account.sessions.confirm.detail', {
              method: t(`account.sessions.method.${target.authentication_method}`),
              time: format.dateTime(target.created_at),
            })}
            expected={target.id}
            confirmLabel={t('account.sessions.end')}
            pending={sessions.ending}
            confirmDisabled={sessions.waiting || sessions.mustRead}
            onClose={sessions.close}
            onConfirm={sessions.confirm}
          >
            {failure}
          </TypedConfirmation>
        ) : null}
      </CardBody>
    </Card>
  );
}
