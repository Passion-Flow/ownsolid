// React binding: the active locale, its translator and a way to change it.
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { defaultLocale, isLocale, loadCatalog, negotiateLocale, type Locale } from './locale.ts';
import { createTranslator, englishCatalog, type Catalog, type Translator } from './translate.ts';

const storageKey = 'ownsolid.locale';

/** What {@link useI18n} returns. */
export interface I18n {
  locale: Locale;
  t: Translator;
  /** Switches the interface language and remembers the choice in this browser. */
  setLocale: (locale: Locale) => void;
}

const I18nContext = createContext<I18n | null>(null);

/** The locale stored in this browser, or the browser's preferred languages. */
export function initialLocale(): Locale {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(storageKey);
  } catch {
    // Storage can be disabled by browser policy; negotiation still works.
  }
  return isLocale(stored) ? stored : negotiateLocale(navigator.languages);
}

/** Props for {@link I18nProvider}. */
export interface I18nProviderProps {
  children: ReactNode;
  /** Starting locale; defaults to {@link initialLocale}. */
  locale?: Locale;
}

/**
 * Provides translations to the tree. Children render once the starting catalog has
 * loaded; English is bundled, so an English start renders immediately.
 */
export function I18nProvider({ children, locale: start }: I18nProviderProps) {
  const [requested, setRequested] = useState<Locale>(() => start ?? initialLocale());
  const [active, setActive] = useState<{ locale: Locale; catalog: Catalog } | null>(() =>
    requested === defaultLocale ? { locale: defaultLocale, catalog: englishCatalog } : null,
  );

  useEffect(() => {
    let current = true;
    loadCatalog(requested).then(
      (catalog) => {
        if (current) setActive({ locale: requested, catalog });
      },
      () => {
        // A catalog chunk that cannot load leaves the interface usable in English.
        if (current) setActive({ locale: defaultLocale, catalog: englishCatalog });
      },
    );
    return () => {
      current = false;
    };
  }, [requested]);

  useEffect(() => {
    if (active) document.documentElement.lang = active.locale;
  }, [active]);

  const setLocale = useCallback((locale: Locale) => {
    try {
      localStorage.setItem(storageKey, locale);
    } catch {
      // The choice still applies to this page when storage is unavailable.
    }
    setRequested(locale);
  }, []);

  const value = useMemo<I18n | null>(
    () =>
      active && {
        locale: active.locale,
        t: createTranslator(active.locale, active.catalog),
        setLocale,
      },
    [active, setLocale],
  );

  if (!value) return null;
  return <I18nContext value={value}>{children}</I18nContext>;
}

/** Returns the active locale and translator. Must be used inside {@link I18nProvider}. */
export function useI18n(): I18n {
  const value = use(I18nContext);
  if (!value) throw new Error('useI18n must be used inside I18nProvider');
  return value;
}
