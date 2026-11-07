// Help articles in the command menu. They ship with the page, so the search runs in the browser
// and works without the network.
import { articleMatches, articles, type Entrance, type HelpLocale } from '@ownsolid/help';
import { CircleHelp } from 'lucide-react';
import type { SearchSource } from '../command-search.ts';

/** How many articles the menu names before offering the help page's search. */
const shown = 5;

/** The command menu's source for this entrance's articles in one language. */
export function helpSearchSource(entrance: Entrance, locale: HelpLocale): SearchSource {
  return {
    key: 'help',
    icon: CircleHelp,
    kind: 'help.search.kind',
    seeAll: 'help.search.seeAll',
    failed: 'help.search.failed',
    find: (term) => {
      const found = articles(entrance, locale).filter((entry) => articleMatches(entry, term));
      return Promise.resolve({
        hits: found.slice(0, shown).map((entry) => ({
          id: entry.slug,
          label: entry.title,
          detail: entry.summary,
          href: `/help/${entry.slug}`,
        })),
        ...(found.length > shown ? { more: `/help?q=${encodeURIComponent(term)}` } : {}),
      });
    },
  };
}
