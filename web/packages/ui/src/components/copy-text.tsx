import { useI18n } from '@ownsolid/i18n';
import { Check, Copy } from 'lucide-react';
import { useEffect, useState } from 'react';
import { cn } from '../cn.ts';

const confirmationMilliseconds = 1_500;

/** Props for {@link CopyText}. */
export interface CopyTextProps {
  /** The text shown and copied. */
  text: string;
  /** Shows the text in the proportional font instead of monospace. */
  proportional?: boolean | undefined;
  className?: string | undefined;
}

/** An identifier, command or URL with a copy button next to it. */
export function CopyText({ text, proportional = false, className }: CopyTextProps) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => {
      setCopied(false);
    }, confirmationMilliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);

  const label = copied ? t('action.copied') : t('action.copy');
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <span className={proportional ? undefined : 'font-mono text-[12.5px]'}>{text}</span>
      <button
        type="button"
        aria-label={label}
        title={label}
        onClick={() => {
          // Clipboard access needs a secure context; a refusal leaves the text selectable.
          navigator.clipboard.writeText(text).then(
            () => {
              setCopied(true);
            },
            () => undefined,
          );
        }}
        className="grid cursor-pointer rounded-[6px] border-0 bg-transparent p-1 text-text-3 hover:bg-surface-2 hover:text-text"
      >
        {copied ? <Check className="size-[15px] text-good" /> : <Copy className="size-[15px]" />}
      </button>
    </span>
  );
}
