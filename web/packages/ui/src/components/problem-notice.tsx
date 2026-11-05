import { isTranslationKey, useI18n } from '@ownsolid/i18n';
import { Notice } from './notice.tsx';
import { Button } from './button.tsx';

/** The parts of an API failure the interface shows; `ApiProblem` satisfies it. */
export interface ProblemLike {
  code: string;
  requestId: string;
  retryable: boolean;
  retryAfterSeconds?: number | undefined;
}

/** Props for {@link ProblemNotice}. */
export interface ProblemNoticeProps {
  problem: ProblemLike;
  /** Offered as "Try again" when the failure may pass on its own. */
  onRetry?: (() => void) | undefined;
  /** The title's element: a heading when the notice is all the page shows. */
  titleAs?: 'p' | 'h1' | 'h2' | undefined;
  className?: string | undefined;
}

/**
 * Explains a failed request: what happened, the likely cause and the next step, with
 * the request ID for support. Unknown codes get a generic explanation.
 */
export function ProblemNotice({ problem, onRetry, titleAs, className }: ProblemNoticeProps) {
  const { t } = useI18n();
  const base = `problem.${problem.code}`;
  const title = `${base}.title`;
  const detail = `${base}.detail`;
  const known = isTranslationKey(title) && isTranslationKey(detail);
  return (
    <Notice
      tone="critical"
      titleAs={titleAs}
      className={className}
      title={known ? t(title) : t('problem.UNKNOWN.title')}
      actions={
        onRetry && problem.retryable ? (
          <Button size="sm" onClick={onRetry}>
            {t('action.retry')}
          </Button>
        ) : undefined
      }
    >
      <p>{known ? t(detail) : t('problem.UNKNOWN.detail', { code: problem.code })}</p>
      {problem.retryAfterSeconds ? (
        <p>{t('problem.retryAfter', { count: problem.retryAfterSeconds })}</p>
      ) : null}
      {problem.requestId ? (
        <p className="mt-1 font-mono text-[12px] text-text-3">
          {t('problem.requestId', { id: problem.requestId })}
        </p>
      ) : null}
    </Notice>
  );
}
