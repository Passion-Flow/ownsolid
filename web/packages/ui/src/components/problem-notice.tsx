import { useI18n } from '@ownsolid/i18n';
import { useEffect, useState } from 'react';
import { Button } from './button.tsx';
import { CopyText } from './copy-text.tsx';
import { Notice } from './notice.tsx';

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
  /** A next step that depends on where the failure happened. */
  detail?: string | undefined;
  className?: string | undefined;
}

// Failures fixed by loading the current state: the page is stale, or the outcome of a save is
// unknown and must be checked before anything is sent again.
const fixedByReload = new Set([
  'CSRF_INVALID',
  'IDEMPOTENCY_CONFLICT',
  'INVALID_CURSOR',
  'NOTIFICATION_NOT_FOUND',
  'PREFERENCE_REVISION_CONFLICT',
  'SSO_BINDING_NOT_FOUND',
  'SSO_CONNECTOR_NOT_FOUND',
  'SSO_REVISION_CONFLICT',
  'STORAGE_COMMIT_OUTCOME_UNKNOWN',
  'UNSUPPORTED_MEDIA_TYPE',
  'VERSION_CONFLICT',
]);

/**
 * Seconds left before the server allows another try, counted down while shown. The notice is
 * keyed by its problem, so a new problem starts a new count.
 */
function useWait(seconds: number | undefined): number {
  const [deadline] = useState(() => Date.now() + (seconds ?? 0) * 1_000);
  const [left, setLeft] = useState(seconds ?? 0);
  useEffect(() => {
    if (left <= 0) return undefined;
    const timer = setInterval(() => {
      setLeft(Math.max(0, Math.ceil((deadline - Date.now()) / 1_000)));
    }, 1_000);
    return () => {
      clearInterval(timer);
    };
  }, [deadline, left]);
  return left;
}

/**
 * Explains a failed request: what happened, the likely cause and the next step, with the
 * action that fits it (UX-107): try again when it may pass, after the wait the server asked
 * for; reload when the page is stale; and the request ID to copy for support.
 */
export function ProblemNotice(props: ProblemNoticeProps) {
  const { code, requestId, retryAfterSeconds } = props.problem;
  return <Explained key={`${code}:${requestId}:${String(retryAfterSeconds)}`} {...props} />;
}

function Explained({ problem, onRetry, titleAs, detail, className }: ProblemNoticeProps) {
  const { t, format, knows } = useI18n();
  const wait = useWait(problem.retryAfterSeconds);
  const base = `problem.${problem.code}`;
  const title = `${base}.title`;
  const detailKey = `${base}.detail`;
  const known = knows(title) && knows(detailKey);
  const reload = fixedByReload.has(problem.code);
  let action = null;
  if (reload) {
    action = (
      <Button
        size="sm"
        onClick={() => {
          window.location.reload();
        }}
      >
        {t('action.reload')}
      </Button>
    );
  } else if (onRetry && problem.retryable) {
    action = (
      <Button size="sm" disabled={wait > 0} onClick={onRetry}>
        {t('action.retry')}
      </Button>
    );
  }
  return (
    <Notice
      tone="critical"
      titleAs={titleAs}
      className={className}
      title={known ? t(title) : t('problem.UNKNOWN.title')}
      actions={action ?? undefined}
    >
      <p>
        {detail ?? (known ? t(detailKey) : t('problem.UNKNOWN.detail', { code: problem.code }))}
      </p>
      {wait > 0 ? (
        <p role="timer">{t('problem.retryAfter', { time: format.duration(wait) })}</p>
      ) : null}
      {problem.requestId ? (
        <p className="mt-1 flex flex-wrap items-center gap-1 text-[12px] text-text-3">
          {t('problem.requestIdLabel')}
          <CopyText text={problem.requestId} className="text-[12px]" />
        </p>
      ) : null}
    </Notice>
  );
}
