import { useI18n } from '@ownsolid/i18n';
import { useEffect, useId, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { Button } from './button.tsx';
import { Dialog } from './dialog.tsx';
import { Notice } from './notice.tsx';
import { Spinner } from './spinner.tsx';
import { TextField } from './text-field.tsx';

/** Props for {@link TypedConfirmation}. */
export interface TypedConfirmationProps {
  /** Names the action and its target, such as "Revoke this invitation". */
  title: string;
  /** What will happen and that it cannot be undone. */
  consequence: ReactNode;
  /** The target's own name, which the person types. */
  expected: string;
  /** The button, repeating the verb of the title. */
  confirmLabel: string;
  pending: boolean;
  onClose: () => void;
  onConfirm: () => void;
  /** Further fields, and the failure of the last attempt. */
  children?: ReactNode | undefined;
}

function matches(typed: string, expected: string): boolean {
  return typed.trim().toLowerCase() === expected.trim().toLowerCase();
}

/**
 * Confirms an irreversible action by having the person type its target. Use it only for what
 * cannot be undone and matters if wrong; a reversible action takes effect at once and offers
 * Undo instead, so this dialog keeps being read.
 */
export function TypedConfirmation({
  title,
  consequence,
  expected,
  confirmLabel,
  pending,
  onClose,
  onConfirm,
  children,
}: TypedConfirmationProps) {
  const { t } = useI18n();
  const formId = useId();
  const field = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState('');
  const ready = matches(typed, expected);
  // The dialog opened in a child's effect and focused its close button; start at the field.
  useEffect(() => {
    field.current?.focus();
  }, []);
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready && !pending) onConfirm();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form={formId} variant="danger" disabled={!ready || pending}>
            {pending ? <Spinner label={confirmLabel} /> : null}
            {confirmLabel}
          </Button>
        </>
      }
    >
      <form id={formId} noValidate onSubmit={submit} className="flex flex-col gap-4 py-2">
        <Notice tone="critical" title={t('confirm.irreversible')}>
          {consequence}
        </Notice>
        <TextField
          ref={field}
          label={t('confirm.type', { text: expected })}
          name="confirmation"
          autoComplete="off"
          spellCheck={false}
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        {children}
      </form>
    </Dialog>
  );
}
