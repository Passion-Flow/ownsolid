// The in-product help of both entrances: articles parsed at build time and the API reference
// reduced from the contracts (docs/research/in-product-help.md). Everything ships with the
// installation; an article or a contract loads only when it is opened.
import type { Root } from 'mdast';
import type { ApiDomain, Entrance, HelpLocale } from './types.ts';

export type * from './types.ts';
export * from './catalog.ts';
/** The syntax tree types of an article body, so a renderer needs no parser of its own. */
export type { Heading, PhrasingContent, Root, RootContent, Table } from 'mdast';

/** The directory each language's generated text lives in. */
const localeDirectories: Readonly<Record<HelpLocale, string>> = { en: 'en', 'zh-CN': 'zh-cn' };

const articleFiles = import.meta.glob<Root>('./gen/locales/*/*/*.json', { import: 'default' });
const apiFiles = import.meta.glob<ApiDomain>('./gen/api/*/*.json', { import: 'default' });
const contractFiles = import.meta.glob<string>('./gen/contracts/*/*.json', {
  query: '?url',
  import: 'default',
});

/** Loads an article's body; null when it does not exist. */
export async function loadArticle(
  entrance: Entrance,
  locale: HelpLocale,
  slug: string,
): Promise<Root | null> {
  const load = articleFiles[`./gen/locales/${localeDirectories[locale]}/${entrance}/${slug}.json`];
  return load ? load() : null;
}

/** Loads one contract's reference; null when the entrance serves nothing from it. */
export async function loadApiDomain(entrance: Entrance, domain: string): Promise<ApiDomain | null> {
  const load = apiFiles[`./gen/api/${entrance}/${domain}.json`];
  return load ? load() : null;
}

/** The address of a contract as the entrance serves it, as a file to save. */
export async function contractUrl(entrance: Entrance, domain: string): Promise<string | null> {
  const load = contractFiles[`./gen/contracts/${entrance}/${domain}.json`];
  return load ? load() : null;
}
