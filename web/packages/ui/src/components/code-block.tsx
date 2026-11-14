import { useI18n } from '@ownsolid/i18n';
import { Check, Copy } from 'lucide-react';
import { useEffect, useState } from 'react';
import { cn } from '../cn.ts';

const confirmationMilliseconds = 1_500;

/** Props for {@link CodeBlock}. */
export interface CodeBlockProps {
  /** The commands, shown as written and copied whole. */
  code: string;
  /** What the block is: it names the copy button. */
  label: string;
  className?: string | undefined;
}

/** Commands to run elsewhere, with one button that copies them all. */
export function CodeBlock({ code, label, className }: CodeBlockProps) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return undefined;
    const timer = setTimeout(() => {
      setCopied(false);
    }, confirmationMilliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);
  // The DOM type includes clipboard even when a browser does not expose it here.
  const { clipboard } = navigator as { clipboard?: Clipboard };
  const fallback = clipboard ? undefined : t('action.copyCommandBySelection', { name: label });
  return (
    <div className={cn('relative', className)}>
      {/* Long lines wrap, so a keyboard needs no way to scroll them. The button copies as written. */}
      <pre className="m-0 rounded-[10px] bg-log-bg py-3 pr-11 pl-3.5 font-mono text-[12px] leading-relaxed break-all whitespace-pre-wrap text-log-text">
        <code>{code}</code>
      </pre>
      <button
        type="button"
        aria-disabled={!clipboard || undefined}
        aria-label={
          fallback ?? t(copied ? 'action.copiedNamed' : 'action.copyNamed', { name: label })
        }
        title={fallback ?? t(copied ? 'action.copied' : 'action.copy')}
        onClick={() => {
          // A refusal leaves the text selectable.
          if (!clipboard) return;
          clipboard.writeText(code).then(
            () => {
              setCopied(true);
            },
            () => undefined,
          );
        }}
        className="absolute top-2 right-2 grid cursor-pointer rounded-[6px] border-0 bg-transparent p-1.5 text-log-text hover:bg-[color-mix(in_srgb,var(--log-text)_16%,transparent)] aria-disabled:cursor-not-allowed aria-disabled:opacity-55"
      >
        {copied ? (
          <Check aria-hidden className="size-4" />
        ) : (
          <Copy aria-hidden className="size-4" />
        )}
      </button>
    </div>
  );
}
