import { useI18n } from '@ownsolid/i18n';
import { X } from 'lucide-react';
import { useEffect, type ComponentProps, type ReactNode } from 'react';
import { Button } from './button.tsx';

/** Props for {@link BulkBar}. */
export interface BulkBarProps {
  /** How many rows are selected. The bar shows nothing at zero. */
  count: number;
  /** Ends the selection; Escape does the same. */
  onClear: () => void;
  /** Actions for every selected row. */
  children: ReactNode;
}

/** The bar at the foot of a list while rows are selected. */
export function BulkBar({ count, onClear, children }: BulkBarProps) {
  const { t } = useI18n();
  const shown = count > 0;
  useEffect(() => {
    if (!shown) return undefined;
    const escape = (event: KeyboardEvent) => {
      // A layer on top of the page closes first: it is the one Escape belongs to.
      if (event.key === 'Escape' && !document.querySelector('dialog[open]')) onClear();
    };
    document.addEventListener('keydown', escape);
    return () => {
      document.removeEventListener('keydown', escape);
    };
  }, [shown, onClear]);
  if (!shown) return null;
  return (
    <div
      role="toolbar"
      aria-label={t('list.bulk.label')}
      className="sticky bottom-5 z-10 mx-auto mt-3.5 flex w-max max-w-full flex-wrap items-center gap-2 rounded-[14px] bg-text py-2 pr-2 pl-4 text-bg shadow-3"
    >
      <span role="status" className="font-semibold">
        {t('list.bulk.selected', { count })}
      </span>
      {children}
      <Button
        size="icon"
        variant="ghost"
        aria-label={t('list.bulk.clear')}
        title={t('list.bulk.clear')}
        className="size-[30px] text-bg hover:bg-[color-mix(in_srgb,var(--bg)_14%,transparent)] hover:text-bg"
        onClick={onClear}
      >
        <X aria-hidden />
      </Button>
    </div>
  );
}

/** An action in the {@link BulkBar}, drawn for its dark background. */
export function BulkAction(props: Omit<ComponentProps<typeof Button>, 'size' | 'variant'>) {
  return (
    <Button
      size="sm"
      variant="ghost"
      className="border-[color-mix(in_srgb,var(--bg)_30%,transparent)] text-bg hover:bg-[color-mix(in_srgb,var(--bg)_14%,transparent)] hover:text-bg"
      {...props}
    />
  );
}
