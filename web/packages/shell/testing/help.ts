// Checks an application's router against its help: every page an article explains or links to
// must exist, or the help sends people to a page that is not there.
import { articles, loadArticle, type Entrance, type RootContent } from '@ownsolid/help';

function links(node: RootContent | { children?: RootContent[] }): string[] {
  const own = 'url' in node ? [node.url] : [];
  const children = 'children' in node ? (node.children ?? []) : [];
  return [...own, ...children.flatMap(links)];
}

/** The page paths the entrance's help names that are not among `routes`, in any language. */
export async function missingHelpPages(
  entrance: Entrance,
  routes: readonly string[],
): Promise<string[]> {
  const known = new Set(routes);
  const missing = new Set<string>();
  for (const locale of ['en', 'zh-CN'] as const) {
    for (const entry of articles(entrance, locale)) {
      const tree = await loadArticle(entrance, locale, entry.slug);
      const pages = [...entry.pages, ...links(tree ?? { children: [] })].filter((url) =>
        url.startsWith('/'),
      );
      for (const page of pages) if (!known.has(page)) missing.add(`${entry.slug}: ${page}`);
    }
  }
  return [...missing].sort();
}
