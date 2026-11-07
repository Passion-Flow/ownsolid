import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  apiDomains,
  articleForPath,
  articleMatches,
  articles,
  contractUrl,
  loadApiDomain,
  loadArticle,
  type Entrance,
  type HelpLocale,
  type RootContent,
} from './index.ts';

const entrances: readonly Entrance[] = ['console', 'portal'];
const locales: readonly HelpLocale[] = ['en', 'zh-CN'];
const audiences: Readonly<Record<Entrance, string>> = { console: 'admin', portal: 'portal' };
const root = join(import.meta.dirname, '..', '..', '..', '..');

function links(node: RootContent | { children?: RootContent[] }): string[] {
  const own = 'url' in node ? [node.url] : [];
  const children = 'children' in node ? (node.children ?? []) : [];
  return [...own, ...children.flatMap(links)];
}

describe.each(entrances)('%s articles', (entrance) => {
  it('exist in every language, in the same order', () => {
    const slugs = locales.map((locale) => articles(entrance, locale).map((entry) => entry.slug));
    expect(slugs[0]?.length).toBeGreaterThan(0);
    for (const list of slugs) expect(list).toEqual(slugs[0]);
  });

  it('never take the API reference address', () => {
    expect(articles(entrance, 'en').map((entry) => entry.slug)).not.toContain('api');
  });

  it.each(locales)(
    'link only to articles of this entrance, and their sections, in %s',
    async (locale) => {
      const known = new Map(
        articles(entrance, locale).map((entry) => [
          entry.slug,
          new Set(entry.sections.map((section) => section.anchor)),
        ]),
      );
      for (const entry of articles(entrance, locale)) {
        const tree = await loadArticle(entrance, locale, entry.slug);
        expect(tree, entry.slug).not.toBeNull();
        for (const url of links(tree ?? { children: [] }).filter((link) =>
          link.startsWith('help:'),
        )) {
          const [slug = '', anchor] = url.slice('help:'.length).split('#');
          expect(known.has(slug), `${entry.slug} links to ${url}`).toBe(true);
          if (anchor)
            expect(known.get(slug)?.has(anchor), `${entry.slug} links to ${url}`).toBe(true);
        }
      }
    },
  );
});

describe('articleForPath', () => {
  it('finds the article of a page, its own pages and nothing for a page without one', () => {
    expect(articleForPath('console', '/')).toBe('getting-started');
    expect(articleForPath('console', '/nodes')).toBe('nodes');
    expect(articleForPath('console', '/nodes/pacing')).toBe('nodes');
    expect(articleForPath('console', '/users/workspaces/1')).toBe('people-and-workspaces');
    expect(articleForPath('console', '/nodesx')).toBeUndefined();
    expect(articleForPath('portal', '/account')).toBe('account');
    expect(articleForPath('portal', '/nodes')).toBeUndefined();
  });
});

describe('articleMatches', () => {
  it('matches the title, summary and section titles in any case', () => {
    const [nodes] = articles('console', 'en').filter((entry) => entry.slug === 'nodes');
    expect(nodes).toBeDefined();
    if (!nodes) return;
    expect(articleMatches(nodes, 'NODES')).toBe(true);
    expect(articleMatches(nodes, 'agent pacing')).toBe(true);
    expect(articleMatches(nodes, 'nothing-has-this')).toBe(false);
    expect(articleMatches(nodes, '  ')).toBe(true);
  });
});

describe.each(entrances)('%s API reference', (entrance) => {
  it('lists exactly the operations the contracts give this entrance', async () => {
    const contracts = join(root, 'api', 'openapi', 'v1');
    const expected = new Set<string>();
    for (const domain of readdirSync(contracts)) {
      if (domain === 'knowledge') continue;
      let raw: string;
      try {
        raw = readFileSync(join(contracts, domain, 'openapi.json'), 'utf8');
      } catch {
        continue;
      }
      const contract = JSON.parse(raw) as {
        paths: Record<string, Record<string, { 'x-audiences'?: string[] }>>;
      };
      for (const [path, item] of Object.entries(contract.paths)) {
        for (const [method, operation] of Object.entries(item)) {
          if (operation['x-audiences']?.includes(audiences[entrance])) {
            expected.add(`${domain} ${method} ${path}`);
          }
        }
      }
    }
    const listed = new Set(
      apiDomains(entrance).flatMap((domain) =>
        domain.operations.map(({ method, path }) => `${domain.domain} ${method} ${path}`),
      ),
    );
    expect(listed).toEqual(expected);
    expect(apiDomains(entrance).map((domain) => domain.domain)).not.toContain('knowledge');
    for (const domain of apiDomains(entrance)) {
      expect(await loadApiDomain(entrance, domain.domain)).not.toBeNull();
      expect(await contractUrl(entrance, domain.domain)).toBeTruthy();
    }
  });

  it.each(['en', 'zh-cn'])('explains every error code it shows in %s', async (catalog) => {
    const messages = JSON.parse(
      readFileSync(join(root, 'web', 'packages', 'i18n', 'locales', `${catalog}.json`), 'utf8'),
    ) as Record<string, string>;
    const missing = new Set<string>();
    for (const summary of apiDomains(entrance)) {
      const domain = await loadApiDomain(entrance, summary.domain);
      for (const operation of domain?.operations ?? []) {
        for (const code of Object.values(operation.errors).flat()) {
          if (!messages[`problem.${code}.title`] || !messages[`problem.${code}.detail`]) {
            missing.add(code);
          }
        }
      }
    }
    expect([...missing].sort()).toEqual([]);
  });
});
