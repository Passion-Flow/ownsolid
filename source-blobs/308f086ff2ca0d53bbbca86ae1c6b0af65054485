import { isLocale, supportedLocales, useI18n } from '@ownsolid/i18n';
import { Languages } from 'lucide-react';
import { useId } from 'react';
import { cn } from '../cn.ts';

/** Chooses the interface language. Each language is named in its own language. */
export function LocaleSelect({ className }: { className?: string | undefined }) {
  const { locale, setLocale, t } = useI18n();
  const id = useId();
  return (
    <div className={cn('inline-flex items-center gap-1.5 text-text-3', className)}>
      <Languages aria-hidden className="size-4" />
      <label htmlFor={id} className="sr-only">
        {t('locale.label')}
      </label>
      <select
        id={id}
        value={locale}
        onChange={(event) => {
          if (isLocale(event.target.value)) setLocale(event.target.value);
        }}
        className="h-8 cursor-pointer rounded-control border-0 bg-transparent pr-1 text-[13px] text-text-2 hover:text-text"
      >
        {supportedLocales.map((value) => (
          <option key={value} value={value}>
            {t(`locale.${value}`)}
          </option>
        ))}
      </select>
    </div>
  );
}
