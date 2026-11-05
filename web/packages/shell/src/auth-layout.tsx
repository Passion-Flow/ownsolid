// What the pages before sign-in share: the centered card, and how they show failures.
import { isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Brand, LocaleSelect, Notice, ProblemNotice } from '@ownsolid/ui';
import type { ReactNode } from 'react';
import { useDocumentTitle } from './page-focus.ts';

/** Props for {@link AuthLayout}. */
export interface AuthLayoutProps {
  /** The page's heading and the browser tab's name. */
  title: string;
  subtitle?: ReactNode | undefined;
  children: ReactNode;
}

/** The centered card of the sign-in, registration and password pages. */
export function AuthLayout({ title, subtitle, children }: AuthLayoutProps) {
  useDocumentTitle(title);
  return (
    <main className="flex min-h-full flex-col items-center justify-center px-5 py-10">
      <div className="w-full max-w-[400px]">
        <Brand className="mb-8 justify-center" />
        <div className="rounded-card border border-border bg-surface p-7 shadow-2">
          <h1 className="m-0 text-[22px] font-bold tracking-[-0.02em]">{title}</h1>
          {subtitle ? <p className="mt-1 mb-0 text-text-2">{subtitle}</p> : null}
          <div className="mt-6">{children}</div>
        </div>
        <div className="mt-5 flex justify-center">
          <LocaleSelect />
        </div>
      </div>
    </main>
  );
}

/** Explains any failure: an API problem with its next step, or a generic message. */
export function FailureNotice({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useI18n();
  if (isProblem(error)) return <ProblemNotice problem={error} onRetry={onRetry} />;
  return <Notice tone="critical" title={t('problem.UNKNOWN.title')} />;
}
