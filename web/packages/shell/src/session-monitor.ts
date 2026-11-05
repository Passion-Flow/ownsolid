// Decides when to warn that a session is ending. The server's answer is the truth, so
// another tab's activity counts; this only chooses when to ask. Kept apart from the dialog
// so its timing can be tested without a browser.
import type { SessionStatus } from '@ownsolid/api';

/** What the person should be told. */
export interface SessionWarning {
  /** `idle` can be undone by staying signed in; `limit` only by signing in again. */
  reason: 'idle' | 'limit';
  /** When the session ends, in this browser's clock. */
  deadline: number;
}

/** What {@link createSessionMonitor} needs from its surroundings. */
export interface SessionMonitorOptions {
  /** Reads the session. `active` makes the read count as activity, which extends it. */
  read: (active: boolean) => Promise<SessionStatus>;
  /** Called when the warning to show changes; `null` hides it. */
  onWarning: (warning: SessionWarning | null) => void;
  /** How often to look again while a warning is showing or a read has failed. */
  pollMilliseconds: number;
}

/** A running monitor. */
export interface SessionMonitor {
  start: () => void;
  stop: () => void;
  /** Records that the person did something on this page. */
  noteActivity: () => void;
  /** Looks again now, for example when the tab becomes visible. */
  check: () => Promise<void>;
  /** The person chose to stay. Rejects when the server could not be reached. */
  stay: () => Promise<void>;
  /** The person read the time limit warning and carries on; it does not return. */
  dismissLimit: () => void;
}

// A warning's end moves by up to a second between reads; a steady countdown matters more.
const deadlineToleranceMilliseconds = 1_500;
const minimumWaitMilliseconds = 1_000;

/**
 * Creates a monitor. While the person is active it extends the session once per warning
 * window, so working in a page without requests does not end the session. Once a warning
 * shows, only an explicit choice extends it: a stray mouse movement must not.
 */
export function createSessionMonitor({
  read,
  onWarning,
  pollMilliseconds,
}: SessionMonitorOptions): SessionMonitor {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let running = false;
  let reading = false;
  let lastActivity = 0;
  let checkedAt = 0;
  // Zero until the first read, so the first check always asks the server.
  let idleDeadline = 0;
  let absoluteDeadline = 0;
  let warningWindow = 0;
  let current: SessionWarning | null = null;
  let limitDismissed = false;

  const show = (next: SessionWarning | null) => {
    const same =
      next?.reason === current?.reason &&
      Math.abs((next?.deadline ?? 0) - (current?.deadline ?? 0)) < deadlineToleranceMilliseconds;
    if (same) return;
    current = next;
    onWarning(next);
  };

  const schedule = () => {
    clearTimeout(timer);
    if (!running) return;
    const untilWarning = Math.min(idleDeadline, absoluteDeadline) - warningWindow - Date.now();
    const wait =
      current || untilWarning <= 0 ? pollMilliseconds : Math.min(warningWindow, untilWarning);
    timer = setTimeout(() => void check(), Math.max(wait, minimumWaitMilliseconds));
  };

  const apply = (status: SessionStatus) => {
    const now = Date.now();
    const idle = status.idle_remaining_seconds * 1_000;
    const absolute = status.absolute_remaining_seconds * 1_000;
    idleDeadline = now + idle;
    absoluteDeadline = now + absolute;
    warningWindow = status.warning_seconds * 1_000;
    if (absolute <= idle && absolute <= warningWindow) {
      show(limitDismissed ? null : { reason: 'limit', deadline: absoluteDeadline });
    } else if (idle <= warningWindow) {
      show({ reason: 'idle', deadline: idleDeadline });
    } else {
      show(null);
    }
  };

  const check = async () => {
    if (!running || reading) return;
    const now = Date.now();
    const active = current === null && lastActivity > checkedAt;
    const due = now >= Math.min(idleDeadline, absoluteDeadline) - warningWindow;
    checkedAt = now;
    if (!active && !due && !current) {
      schedule();
      return;
    }
    reading = true;
    try {
      apply(await read(active));
    } catch {
      // The session query reports a session that ended. Anything else may pass, and the
      // warning stays up meanwhile: an unreachable server has not signed anyone out.
    } finally {
      reading = false;
    }
    schedule();
  };

  return {
    start: () => {
      running = true;
      void check();
    },
    stop: () => {
      running = false;
      clearTimeout(timer);
    },
    noteActivity: () => {
      lastActivity = Date.now();
    },
    check,
    stay: async () => {
      apply(await read(true));
      schedule();
    },
    dismissLimit: () => {
      limitDismissed = true;
      show(null);
      schedule();
    },
  };
}
