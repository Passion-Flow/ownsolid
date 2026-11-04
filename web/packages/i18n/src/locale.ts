// Supported locales, negotiation and catalog loading.
import { englishCatalog, type Catalog } from './translate.ts';

/** Locales the product ships, as BCP 47 tags. */
export const supportedLocales = ['en', 'zh-CN'] as const;

/** A supported locale. */
export type Locale = (typeof supportedLocales)[number];

/** The locale used when nothing else matches. */
export const defaultLocale: Locale = 'en';

/** Returns true when the value names a supported locale exactly. */
export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && (supportedLocales as readonly string[]).includes(value);
}

/**
 * Picks the first supported locale for a list of preferred language tags, such as
 * `navigator.languages`. Matching ignores case and falls back from a region
 * (`zh-Hans-CN`, `zh-TW`) to the language when the exact tag is not shipped.
 */
export function negotiateLocale(preferred: readonly string[]): Locale {
  for (const tag of preferred) {
    const lower = tag.toLowerCase();
    const exact = supportedLocales.find((locale) => locale.toLowerCase() === lower);
    if (exact) return exact;
    const language = lower.split('-')[0];
    const partial = supportedLocales.find(
      (locale) => locale.toLowerCase().split('-')[0] === language,
    );
    if (partial) return partial;
  }
  return defaultLocale;
}

// Catalogs other than English are separate chunks, loaded only when selected.
const loaders: Record<Locale, () => Promise<Catalog>> = {
  en: () => Promise.resolve(englishCatalog),
  'zh-CN': () => import('../locales/zh-cn.json').then((module) => module.default as Catalog),
};

/** Loads the catalog for a locale. */
export function loadCatalog(locale: Locale): Promise<Catalog> {
  return loaders[locale]();
}
