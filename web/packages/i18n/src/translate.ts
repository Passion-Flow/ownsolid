// Message lookup and formatting. English is the source catalog: its keys define
// MessageKey, and every other catalog must provide the same keys and placeholders.
import en from '../locales/en.json';

/** A message catalog: message key to template. */
export type Catalog = Readonly<Record<MessageKey, string>>;

type PluralSuffix = '_zero' | '_one' | '_two' | '_few' | '_many' | '_other';
type StripPlural<K extends string> = K extends `${infer Base}${PluralSuffix}` ? Base : K;

/** Every key in the English catalog, including plural variants such as `x_one`. */
export type MessageKey = keyof typeof en;

/** Keys a caller may translate: plain keys and plural bases without their suffix. */
export type TranslationKey = StripPlural<MessageKey>;

/** Values substituted for `{name}` placeholders. A `count` value also selects the plural form. */
export type MessageParams = Readonly<Record<string, string | number>>;

/** Translates a key in one locale. */
export type Translator = (key: TranslationKey, params?: MessageParams) => string;

/** The English catalog, always bundled; other catalogs load on demand. */
export const englishCatalog: Catalog = en;

/** Returns true when the English catalog has the key, directly or as a plural base. */
export function isTranslationKey(key: string): key is TranslationKey {
  return Object.hasOwn(englishCatalog, key) || Object.hasOwn(englishCatalog, `${key}_other`);
}

const placeholder = /\{([a-zA-Z][a-zA-Z0-9_]*)\}/g;

/** Returns the placeholder names used in a template, in order of appearance. */
export function placeholders(template: string): string[] {
  return [...template.matchAll(placeholder)].map((match) => match[1] ?? '');
}

/**
 * Creates a translator for a locale and catalog. Numbers are formatted for the
 * locale. A missing key falls back to English, so a partly translated catalog never
 * shows raw keys; the catalog tests keep that from shipping.
 */
export function createTranslator(locale: string, catalog: Catalog): Translator {
  const rules = new Intl.PluralRules(locale);
  const numbers = new Intl.NumberFormat(locale);
  const lookup = (key: string): string | undefined =>
    (catalog as Record<string, string>)[key] ?? (englishCatalog as Record<string, string>)[key];

  return (key, params) => {
    let template: string | undefined;
    const count = params?.count;
    if (typeof count === 'number') {
      template = lookup(`${key}_${rules.select(count)}`) ?? lookup(`${key}_other`);
    }
    template ??= lookup(key) ?? key;
    if (!params) return template;
    return template.replace(placeholder, (whole, name: string) => {
      const value = params[name];
      if (value === undefined) return whole;
      return typeof value === 'number' ? numbers.format(value) : value;
    });
  };
}
