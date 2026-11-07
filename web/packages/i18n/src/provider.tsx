// React binding: the active locale, its translator and a way to change it.
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { createFormatter, type Formatter } from './format.ts';
import { defaultLocale, isLocale, loadCatalog, negotiateLocale, type Locale } from './locale.ts';
import {
  createTranslator,
  hasTranslation,
  type Catalog,
  type TranslationKey,
  type Translator,
} from './translate.ts';

const storageKey = 'ownsolid.locale';
// Set once the stored language matches the account. Without it, a stored language is a choice
// the account has not heard about yet, such as one made on the sign-in page.
const savedKey = 'ownsolid.locale.saved';

/** A language someone chose, or `auto` for none: the installation's, then the browser's. */
export type LocaleChoice = Locale | 'auto';

/** Receives a language the person chose, to save it beyond this browser. */
export type LocaleSaver = (choice: LocaleChoice) => void;

/** What {@link useI18n} returns. */
export interface I18n {
  /** The language showing now. */
  locale: Locale;
  /** What was chosen; `auto` when `locale` comes from the installation or the browser. */
  choice: LocaleChoice;
  t: Translator;
  /** Whether a key built at run time, such as one from an error code, has a translation. */
  knows: (key: string) => key is TranslationKey;
  /** Dates, numbers and units for the active language. */
  format: Formatter;
  /** Applies a language the person chose and saves it through the connected saver. */
  setLocale: (choice: LocaleChoice) => void;
  /** Applies the language saved on the account. Unlike `setLocale`, this saves nothing. */
  adoptLocale: (choice: LocaleChoice) => void;
  /** Sets the installation's language for people who have not chosen one. */
  setInstallationLocale: (choice: LocaleChoice) => void;
  /**
   * Sends later choices to `saver` and returns a function that stops. A choice the account
   * has not heard about goes out at once.
   */
  connectLocale: (saver: LocaleSaver) => () => void;
}

const I18nContext = createContext<I18n | null>(null);

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    // Storage can be disabled by browser policy; negotiation still works.
    return null;
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // The choice still applies to this page when storage is unavailable.
  }
}

function storedChoice(): LocaleChoice {
  const stored = read(storageKey);
  return isLocale(stored) ? stored : 'auto';
}

function resolve(choice: LocaleChoice, installation: LocaleChoice): Locale {
  if (choice !== 'auto') return choice;
  return installation === 'auto' ? negotiateLocale(navigator.languages) : installation;
}

/** The language a page opens in: stored, or negotiated with the browser. */
export function initialLocale(): Locale {
  return resolve(storedChoice(), 'auto');
}

/** Props for {@link I18nProvider}. */
export interface I18nProviderProps {
  children: ReactNode;
  /** Starting locale; defaults to {@link initialLocale}. */
  locale?: Locale;
}

// With no catalog there is nothing to translate with, so this is the one message in the code. A deployment
// replaced under an open page is the usual cause, and a reload finds the new files.
function CatalogFailure() {
  return (
    <div role="alert" style={{ padding: 24, fontFamily: 'system-ui, sans-serif' }}>
      <p>The console could not load its text. Check the connection and reload the page.</p>
      <button
        type="button"
        onClick={() => {
          window.location.reload();
        }}
      >
        Reload
      </button>
    </div>
  );
}

/**
 * Provides translations to the tree. Children render once the starting catalog has loaded. A language that
 * cannot load falls back to English; when English cannot load either, the tree says so and offers a reload.
 */
export function I18nProvider({ children, locale: start }: I18nProviderProps) {
  const [choice, setChoice] = useState<LocaleChoice>(() => start ?? storedChoice());
  const [installation, setInstallationLocale] = useState<LocaleChoice>('auto');
  const saver = useRef<LocaleSaver | null>(null);
  const requested = resolve(choice, installation);
  const [active, setActive] = useState<{ locale: Locale; catalog: Catalog } | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let current = true;
    loadCatalog(requested).then(
      (catalog) => {
        if (current) setActive({ locale: requested, catalog });
      },
      () =>
        loadCatalog(defaultLocale).then(
          (catalog) => {
            if (current) setActive({ locale: defaultLocale, catalog });
          },
          () => {
            if (current) setFailed(true);
          },
        ),
    );
    return () => {
      current = false;
    };
  }, [requested]);

  useEffect(() => {
    if (active) document.documentElement.lang = active.locale;
  }, [active]);

  const adoptLocale = useCallback((next: LocaleChoice) => {
    write(storageKey, next === 'auto' ? null : next);
    write(savedKey, 'true');
    setChoice(next);
  }, []);
  const setLocale = useCallback((next: LocaleChoice) => {
    write(storageKey, next === 'auto' ? null : next);
    write(savedKey, saver.current ? 'true' : null);
    setChoice(next);
    saver.current?.(next);
  }, []);
  const connectLocale = useCallback((next: LocaleSaver) => {
    saver.current = next;
    const stored = storedChoice();
    if (stored !== 'auto' && read(savedKey) === null) {
      write(savedKey, 'true');
      next(stored);
    }
    return () => {
      saver.current = null;
    };
  }, []);

  const value = useMemo<I18n | null>(
    () =>
      active && {
        locale: active.locale,
        choice,
        t: createTranslator(active.locale, active.catalog),
        knows: (key: string): key is TranslationKey => hasTranslation(active.catalog, key),
        format: createFormatter(active.locale),
        setLocale,
        adoptLocale,
        setInstallationLocale,
        connectLocale,
      },
    [active, choice, setLocale, adoptLocale, connectLocale],
  );

  if (!active) return failed ? <CatalogFailure /> : null;
  return <I18nContext value={value}>{children}</I18nContext>;
}

/** Returns the active locale, translator and formatter. Must be used inside {@link I18nProvider}. */
export function useI18n(): I18n {
  const value = use(I18nContext);
  if (!value) throw new Error('useI18n must be used inside I18nProvider');
  return value;
}
