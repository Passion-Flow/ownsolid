// What the help pages list without loading anything: the articles of each entrance in each
// language, the page each explains, and the operations of each contract. It has no lazy loaders,
// so browser tests can read it directly.
import { helpIndex } from './gen/index.ts';
import type { ApiDomainSummary, ArticleMeta, ArticleText, Entrance, HelpLocale } from './types.ts';

/** An article as a list shows it. */
export type ArticleEntry = ArticleMeta & ArticleText;

/** The articles of an entrance in one language, in the order the help page lists them. */
export function articles(entrance: Entrance, locale: HelpLocale): ArticleEntry[] {
  const text = helpIndex.text[entrance][locale];
  return helpIndex.articles[entrance].flatMap((meta) => {
    const entry = text[meta.slug];
    return entry ? [{ ...meta, ...entry }] : [];
  });
}

/** One article's listing, or undefined when the entrance has no such article. */
export function article(
  entrance: Entrance,
  locale: HelpLocale,
  slug: string,
): ArticleEntry | undefined {
  return articles(entrance, locale).find((entry) => entry.slug === slug);
}

/** The article that explains a page: the one naming the longest prefix of its path. */
export function articleForPath(entrance: Entrance, pathname: string): string | undefined {
  let best: { slug: string; length: number } | undefined;
  for (const meta of helpIndex.articles[entrance]) {
    for (const page of meta.pages) {
      const matches =
        pathname === page || (page !== '/' && pathname.startsWith(`${page.replace(/\/$/, '')}/`));
      if (matches && (!best || page.length > best.length)) {
        best = { slug: meta.slug, length: page.length };
      }
    }
  }
  return best?.slug;
}

/** Whether an article matches a search, by its title, summary or a section title. */
export function articleMatches(entry: ArticleEntry, term: string): boolean {
  const wanted = term.trim().toLowerCase();
  if (wanted === '') return true;
  return [entry.title, entry.summary, ...entry.sections.map((section) => section.title)].some(
    (value) => value.toLowerCase().includes(wanted),
  );
}

/** The contracts an entrance serves, with their operations, without loading any. */
export function apiDomains(entrance: Entrance): readonly ApiDomainSummary[] {
  return helpIndex.api[entrance];
}
