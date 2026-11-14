// Full-page states shared by every route: loading, not found and failure.
import { isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Brand, Button, Notice, ProblemNotice, Spinner } from '@ownsolid/ui';
import { Link, useRouter, type ErrorComponentProps } from '@tanstack/react-router';
import type { ReactNode } from 'react';
import { useDocumentTitle } from './page-focus.ts';

function FullPage({ title, children }: { title: string; children: ReactNode }) {
  useDocumentTitle(title);
  return (
    <main className="flex min-h-full flex-col items-center justify-center gap-6 px-5 py-10">
      <Brand />
      <div className="w-full max-w-[480px]">{children}</div>
    </main>
  );
}

/** Shown while a route's data loads for longer than a moment. */
export function PendingPage() {
  const { t } = useI18n();
  return (
    <div className="flex min-h-full items-center justify-center">
      <Spinner label={t('status.loading')} />
    </div>
  );
}

/** Shown for a path no route matches. */
export function NotFoundPage() {
  const { t } = useI18n();
  const title = t('status.notFound.title');
  return (
    <FullPage title={title}>
      <Notice
        title={title}
        titleAs="h1"
        actions={
          <Button asChild size="sm">
            <Link to="/">{t('status.notFound.home')}</Link>
          </Button>
        }
      >
        {t('status.notFound.detail')}
      </Notice>
    </FullPage>
  );
}

/** Shown when a route fails to load; retrying reloads the route's data. */
export function RouteErrorPage({ error }: ErrorComponentProps) {
  const { t } = useI18n();
  const router = useRouter();
  const retry = () => {
    void router.invalidate();
  };
  return (
    <FullPage title={t('status.crashed.title')}>
      {isProblem(error) ? (
        <ProblemNotice problem={error} onRetry={retry} titleAs="h1" />
      ) : (
        <Notice
          tone="critical"
          title={t('status.crashed.title')}
          titleAs="h1"
          actions={
            <Button size="sm" onClick={retry}>
              {t('action.retry')}
            </Button>
          }
        >
          {t('status.crashed.detail')}
        </Notice>
      )}
    </FullPage>
  );
}
