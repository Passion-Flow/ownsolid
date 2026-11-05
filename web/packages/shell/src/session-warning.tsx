// The warning before a session ends, and the one action that keeps it going.
import { getSessionStatus, isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, Dialog, ProblemNotice } from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState } from 'react';
import { runtimeConfigQuery } from './routes.tsx';
import {
  createSessionMonitor,
  type SessionMonitor,
  type SessionWarning,
} from './session-monitor.ts';

// What counts as someone being here. Listeners only store a time, so frequent events cost
// nothing; capture sees scrolling inside panels, which does not bubble.
const activityEvents = ['pointerdown', 'pointermove', 'keydown', 'wheel', 'scroll'] as const;

function formatRemaining(seconds: number): string {
  return `${String(Math.floor(seconds / 60))}:${String(seconds % 60).padStart(2, '0')}`;
}

/** Counts down to the end. It is a timer for screen readers: not read aloud every second. */
function Countdown({ deadline }: { deadline: number }) {
  const { t } = useI18n();
  const left = () => Math.max(0, Math.ceil((deadline - Date.now()) / 1_000));
  const [seconds, setSeconds] = useState(left);
  useEffect(() => {
    const timer = setInterval(() => {
      setSeconds(left());
    }, 1_000);
    return () => {
      clearInterval(timer);
    };
    // `left` reads only the deadline.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deadline]);
  return (
    <p className="m-0 flex items-baseline gap-2 py-2">
      <span className="text-text-2">{t('session.remaining')}</span>
      <span role="timer" className="text-[22px] font-bold tabular-nums">
        {formatRemaining(seconds)}
      </span>
    </p>
  );
}

function useSessionMonitor(pollMilliseconds: number) {
  const client = useQueryClient();
  const [warning, setWarning] = useState<SessionWarning | null>(null);
  // Through the query client, so a read that finds the session over sends the person to
  // sign in like any other request.
  const monitor = useMemo(
    () =>
      createSessionMonitor({
        pollMilliseconds,
        onWarning: setWarning,
        read: (active) =>
          client.query({
            queryKey: ['session-status', active],
            queryFn: ({ signal }) => getSessionStatus({ signal, background: !active }),
            staleTime: 0,
            gcTime: 0,
            retry: false,
          }),
      }),
    [client, pollMilliseconds],
  );
  useEffect(() => {
    const active = () => {
      monitor.noteActivity();
    };
    const visible = () => {
      if (document.visibilityState === 'visible') void monitor.check();
    };
    for (const name of activityEvents) {
      window.addEventListener(name, active, { capture: true, passive: true });
    }
    document.addEventListener('visibilitychange', visible);
    monitor.start();
    return () => {
      monitor.stop();
      for (const name of activityEvents) window.removeEventListener(name, active, true);
      document.removeEventListener('visibilitychange', visible);
    };
  }, [monitor]);
  return { monitor, warning };
}

interface ContentProps {
  monitor: SessionMonitor;
  warning: SessionWarning | null;
  onSignOut: () => void;
}

function Content({ monitor, warning, onSignOut }: ContentProps) {
  const { t } = useI18n();
  const primary = useRef<HTMLButtonElement>(null);
  const stay = useMutation({ mutationFn: () => monitor.stay() });
  const limit = warning?.reason === 'limit';
  const shown = warning !== null;
  const carryOn = () => {
    if (limit) monitor.dismissLimit();
    else stay.mutate();
  };
  // The dialog opened in a child's effect and focused its close button. Start on the
  // action almost everyone wants, so one key keeps the session.
  useEffect(() => {
    if (shown) primary.current?.focus();
  }, [shown]);
  return (
    <Dialog
      open={shown}
      onClose={carryOn}
      title={limit ? t('session.limit.title') : t('session.idle.title')}
      description={limit ? t('session.limit.detail') : t('session.idle.detail')}
      footer={
        <>
          <Button onClick={onSignOut}>
            {limit ? t('session.limit.signIn') : t('signOut.action')}
          </Button>
          <Button ref={primary} variant="primary" disabled={stay.isPending} onClick={carryOn}>
            {limit ? t('session.limit.continue') : t('session.idle.stay')}
          </Button>
        </>
      }
    >
      {stay.error && isProblem(stay.error) ? (
        <ProblemNotice className="mb-2" problem={stay.error} onRetry={carryOn} />
      ) : null}
      {warning ? <Countdown deadline={warning.deadline} /> : null}
    </Dialog>
  );
}

function Monitored({
  pollMilliseconds,
  onSignOut,
}: {
  pollMilliseconds: number;
  onSignOut: () => void;
}) {
  const { monitor, warning } = useSessionMonitor(pollMilliseconds);
  return <Content monitor={monitor} warning={warning} onSignOut={onSignOut} />;
}

/** Watches the session and warns before it ends. Shows nothing until then. */
export function SessionWarningDialog({ onSignOut }: { onSignOut: () => void }) {
  const pollMilliseconds = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds;
  return pollMilliseconds ? (
    <Monitored pollMilliseconds={pollMilliseconds} onSignOut={onSignOut} />
  ) : null;
}
