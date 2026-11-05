// Dialogs and drawers on the native <dialog> element, which gives the top layer, a
// focus trap, Escape and an inert page without injecting styles at runtime.
import { useI18n } from '@ownsolid/i18n';
import { X } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useRef, type ReactNode } from 'react';
import { cn } from '../cn.ts';
import { Button } from './button.tsx';

/** Props for {@link ModalSurface}. */
interface ModalSurfaceProps {
  open: boolean;
  onClose: () => void;
  className: string;
  /** Accessible name; defaults to the labelled title inside. */
  label?: string | undefined;
  labelledBy?: string | undefined;
  children: ReactNode;
}

/** Shows a native modal dialog while `open` is true. */
export function ModalSurface({
  open,
  onClose,
  className,
  label,
  labelledBy,
  children,
}: ModalSurfaceProps) {
  const element = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = element.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  // A dialog removed from the page while open gives focus back to nothing. Layout effects
  // clean up before the element leaves, so closing here returns focus to what opened it.
  useLayoutEffect(() => {
    const dialog = element.current;
    return () => {
      if (dialog?.open) dialog.close();
    };
  }, []);

  useEffect(() => {
    const dialog = element.current;
    if (!dialog) return;
    const cancel = (event: Event) => {
      event.preventDefault();
      onClose();
    };
    // A click on the element itself is a click on the backdrop.
    const click = (event: MouseEvent) => {
      if (event.target === dialog) onClose();
    };
    dialog.addEventListener('cancel', cancel);
    dialog.addEventListener('click', click);
    return () => {
      dialog.removeEventListener('cancel', cancel);
      dialog.removeEventListener('click', click);
    };
  }, [onClose]);

  return (
    <dialog
      ref={element}
      aria-label={label}
      aria-labelledby={labelledBy}
      className={cn(
        'border border-border bg-surface p-0 text-text shadow-3 backdrop:bg-[rgb(15_16_20/28%)]',
        className,
      )}
    >
      {open ? children : null}
    </dialog>
  );
}

/** Props for {@link Dialog} and {@link Drawer}. */
export interface DialogProps {
  open: boolean;
  /** Called on Escape, a backdrop click and the close button. */
  onClose: () => void;
  title: ReactNode;
  /** Short context under the title. */
  description?: ReactNode | undefined;
  /** Buttons at the bottom. */
  footer?: ReactNode | undefined;
  children: ReactNode;
}

function CloseButton({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Button
      variant="ghost"
      size="icon"
      className="ml-auto"
      aria-label={t('action.close')}
      title={t('action.close')}
      onClick={onClose}
    >
      <X aria-hidden />
    </Button>
  );
}

/** A centered dialog for a focused task or an irreversible confirmation. */
export function Dialog({ open, onClose, title, description, footer, children }: DialogProps) {
  const titleId = useId();
  return (
    <ModalSurface
      open={open}
      onClose={onClose}
      labelledBy={titleId}
      className="m-auto max-h-[88vh] w-[640px] max-w-[94vw] flex-col rounded-dialog open:flex"
    >
      <div className="flex items-center gap-3.5 px-6 pt-5 pb-1.5">
        <div className="min-w-0">
          <h2 id={titleId} className="m-0 text-[18px] font-bold tracking-[-0.02em]">
            {title}
          </h2>
          {description ? <p className="m-0 mt-0.5 text-text-2">{description}</p> : null}
        </div>
        <CloseButton onClose={onClose} />
      </div>
      <div className="overflow-y-auto px-6 py-2">{children}</div>
      {footer ? (
        <div className="mt-2.5 flex items-center justify-end gap-2 border-t border-border px-6 pt-3.5 pb-[18px]">
          {footer}
        </div>
      ) : null}
    </ModalSurface>
  );
}

/** A panel at the right edge for details of a list row. */
export function Drawer({ open, onClose, title, description, footer, children }: DialogProps) {
  const titleId = useId();
  return (
    <ModalSurface
      open={open}
      onClose={onClose}
      labelledBy={titleId}
      className="my-0 mr-0 ml-auto h-dvh max-h-none w-[560px] max-w-[94vw] flex-col border-y-0 border-r-0 open:flex"
    >
      <div className="flex items-start gap-3.5 border-b border-border px-6 pt-5 pb-3.5">
        <div className="min-w-0">
          <h2 id={titleId} className="m-0 text-[20px] font-bold tracking-[-0.02em]">
            {title}
          </h2>
          {description ? <p className="m-0 mt-0.5 text-text-2">{description}</p> : null}
        </div>
        <CloseButton onClose={onClose} />
      </div>
      <div className="flex-1 overflow-y-auto px-6 pt-[18px] pb-8">{children}</div>
      {footer ? (
        <div className="flex items-center justify-end gap-2 border-t border-border px-6 py-3.5">
          {footer}
        </div>
      ) : null}
    </ModalSurface>
  );
}
