// A log that grows while a task runs (design-system long-running tasks,
// docs/research/long-task-pattern.md).
import { useI18n } from '@ownsolid/i18n';
import { Check, Copy, Download, Pause, Play } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '../cn.ts';
import { saveFile } from '../save-file.ts';
import { Button } from './button.tsx';

const confirmationMilliseconds = 1_500;

// How near the end still counts as being at the end, in pixels.
const followTolerance = 8;

/** One line of a {@link LiveLog}. */
export interface LiveLogLine {
  id: string;
  /** When it happened, as it should read. */
  time: string;
  level: 'info' | 'warning' | 'error';
  message: string;
  /** Sets the line apart, such as where a new attempt begins. */
  marker?: boolean | undefined;
}

/** Props for {@link LiveLog}. */
export interface LiveLogProps {
  /** Names the log for screen readers. */
  label: string;
  lines: readonly LiveLogLine[];
  /** True while more lines can arrive; a finished log has nothing to pause. */
  live: boolean;
  /** The name a download gets. */
  fileName: string;
  /** What shows before the first line. */
  empty: string;
  className?: string | undefined;
}

const levels: Record<LiveLogLine['level'], string> = {
  info: '',
  warning: 'text-log-warning',
  error: 'text-log-error',
};

function asText(lines: readonly LiveLogLine[]): string {
  return lines.map((line) => `${line.time} ${line.message}`).join('\n');
}

/**
 * A log that follows its end while it is scrolled to the end, so reading earlier lines is not
 * interrupted. Pausing holds new lines back until it resumes.
 */
export function LiveLog({ label, lines, live, fileName, empty, className }: LiveLogProps) {
  const { t, format } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const [held, setHeld] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);
  // A log that has finished has nothing left to hold back.
  const paused = live && held !== null;
  const shown = paused ? lines.slice(0, held) : lines;
  const waiting = lines.length - shown.length;

  useLayoutEffect(() => {
    const element = box.current;
    if (element && following.current) element.scrollTop = element.scrollHeight;
  }, [shown.length]);

  useEffect(() => {
    if (!copied) return undefined;
    const timer = setTimeout(() => {
      setCopied(false);
    }, confirmationMilliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);

  const download = () => {
    saveFile(new Blob([asText(lines)], { type: 'text/plain' }), fileName);
  };

  return (
    <div className={className}>
      <div className="mb-2 flex flex-wrap items-center justify-end gap-1.5">
        {live ? (
          <Button
            size="sm"
            aria-pressed={paused}
            onClick={() => {
              setHeld(paused ? null : lines.length);
            }}
          >
            {paused ? <Play aria-hidden /> : <Pause aria-hidden />}
            {paused ? t('log.resume', { count: format.number(waiting) }) : t('log.pause')}
          </Button>
        ) : null}
        <Button
          size="sm"
          disabled={lines.length === 0}
          onClick={() => {
            navigator.clipboard.writeText(asText(lines)).then(
              () => {
                setCopied(true);
              },
              () => {
                setCopied(false);
              },
            );
          }}
        >
          {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
          {copied ? t('action.copied') : t('log.copy')}
        </Button>
        <Button size="sm" disabled={lines.length === 0} onClick={download}>
          <Download aria-hidden />
          {t('log.download')}
        </Button>
      </div>
      <div
        ref={box}
        role="log"
        aria-label={label}
        // eslint-disable-next-line jsx-a11y-x/no-noninteractive-tabindex -- it scrolls, so a keyboard must reach it
        tabIndex={0}
        onScroll={(event) => {
          const { scrollTop, clientHeight, scrollHeight } = event.currentTarget;
          following.current = scrollTop + clientHeight >= scrollHeight - followTolerance;
        }}
        className={cn(
          'h-[280px] overflow-y-auto rounded-[10px] bg-log-bg px-4 py-3.5 font-mono text-[12px]',
          'leading-[1.65] text-log-text outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        {shown.length === 0 ? <div className="text-log-muted">{empty}</div> : null}
        {shown.map((line) => (
          <div
            key={line.id}
            className={cn(
              'break-words whitespace-pre-wrap',
              levels[line.level],
              line.marker ? 'mt-2 border-t border-log-muted/40 pt-2 font-semibold' : undefined,
            )}
          >
            <span className="mr-2.5 text-log-muted">{line.time}</span>
            {line.message}
          </div>
        ))}
      </div>
    </div>
  );
}
