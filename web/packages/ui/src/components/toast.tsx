// Short confirmations with an optional undo (design-system principle 4).
import { useI18n } from '@ownsolid/i18n';
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

const plainMilliseconds = 2_400;
const actionMilliseconds = 6_000;

const actionClasses =
  'h-7 cursor-pointer rounded-control border-0 bg-[color-mix(in_srgb,var(--bg)_14%,transparent)] px-2.5 font-semibold text-bg';

/** Options for a toast. */
export interface ToastOptions {
  message: string;
  /**
   * Reverses the action; the toast stays longer and offers an undo button. An undo that
   * reaches the server returns its promise, so a refusal is reported and can be retried.
   * Return a localized string when the result needs a more specific completion message.
   */
  undo?: (() => unknown) | undefined;
  /** Repeats what failed; the toast stays longer and offers a retry button. */
  retry?: (() => void) | undefined;
  /** Leads on from the message, such as to what it is about; the toast stays longer. */
  action?: { label: string; run: () => void } | undefined;
}

type ShowToast = (options: ToastOptions) => void;

const ToastContext = createContext<ShowToast | null>(null);

/** Shows toasts for the tree. One toast is visible at a time; a new one replaces it. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const [current, setCurrent] = useState<ToastOptions | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const show = useCallback<ShowToast>((options) => {
    clearTimeout(timer.current);
    setCurrent(options);
    timer.current = setTimeout(
      () => {
        setCurrent(null);
      },
      options.undo || options.retry || options.action ? actionMilliseconds : plainMilliseconds,
    );
  }, []);

  const undo = (action: ToastOptions['undo']) => {
    Promise.resolve(action?.()).then(
      (result) => {
        show({ message: typeof result === 'string' ? result : t('action.undone') });
      },
      () => {
        show({
          message: t('action.undoFailed'),
          retry: () => {
            undo(action);
          },
        });
      },
    );
  };

  useEffect(
    () => () => {
      clearTimeout(timer.current);
    },
    [],
  );

  return (
    <ToastContext value={show}>
      {children}
      <div
        role="status"
        className="pointer-events-none fixed bottom-7 left-1/2 z-40 -translate-x-1/2"
      >
        {current ? (
          <div className="pointer-events-auto flex items-center gap-3.5 rounded-[12px] bg-text py-2.5 pr-2.5 pl-4 text-[13.5px] text-bg shadow-3">
            <span>{current.message}</span>
            {current.undo ? (
              <button
                type="button"
                className={actionClasses}
                onClick={() => {
                  undo(current.undo);
                }}
              >
                {t('action.undo')}
              </button>
            ) : null}
            {current.retry ? (
              <button
                type="button"
                className={actionClasses}
                onClick={() => {
                  current.retry?.();
                  setCurrent(null);
                }}
              >
                {t('action.retry')}
              </button>
            ) : null}
            {current.action ? (
              <button
                type="button"
                className={actionClasses}
                onClick={() => {
                  current.action?.run();
                  setCurrent(null);
                }}
              >
                {current.action.label}
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
    </ToastContext>
  );
}

/** Returns the function that shows a toast. Must be used inside {@link ToastProvider}. */
export function useToast(): ShowToast {
  const value = use(ToastContext);
  if (!value) throw new Error('useToast must be used inside ToastProvider');
  return value;
}
