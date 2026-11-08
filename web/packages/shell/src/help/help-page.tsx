// `/help`: the articles of this entrance, grouped by topic and searchable in the browser, and the
// way to the API reference (plan S-107, docs/research/in-product-help.md).
import { articleMatches, articles, type ArticleEntry } from '@ownsolid/help';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { Button, Card, EmptyState, SearchField } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Braces, ChevronRight, SearchX } from 'lucide-react';
import { useMemo } from 'react';
import type { ListDefinition } from '../list/list-state.ts';
import { useListView } from '../list/use-list-view.ts';
import { useTypedFilter } from '../list/use-typed-filter.ts';
import { runtimeConfigQuery } from '../routes.tsx';
import { useHelp } from './entrance.ts';

const definition: ListDefinition = {
  key: 'help',
  filters: { q: { kind: 'text', maximum: 100 } },
  columns: [],
};

const cardLinkClasses =
  'flex items-center gap-3 px-5 py-3.5 transition-colors duration-150 hover:bg-surface-2';

function ArticleRow({ entry }: { entry: ArticleEntry }) {
  return (
    <li>
      <Link to="/help/$slug" params={{ slug: entry.slug }} className={cardLinkClasses}>
        <span className="min-w-0 flex-1">
          <b className="block text-[14px] font-semibold text-text">{entry.title}</b>
          <span className="mt-0.5 block text-[13px] text-text-2">{entry.summary}</span>
        </span>
        <ChevronRight aria-hidden className="size-4 flex-none text-text-3" />
      </Link>
    </li>
  );
}

/** Every article of this entrance, and the API reference. */
export function HelpPage() {
  const { t } = useI18n();
  const { entrance, locale } = useHelp();
  const debounce = useQuery(runtimeConfigQuery()).data?.ui.searchDebounceMilliseconds ?? 0;
  const list = useListView(definition);
  const [typed, setTyped] = useTypedFilter(list, 'q', debounce);
  const search = list.state.filters.q ?? '';
  const found = useMemo(
    () => articles(entrance, locale).filter((entry) => articleMatches(entry, search)),
    [entrance, locale, search],
  );
  const topics = [...new Set(found.map((entry) => entry.topic))];

  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('help.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('help.subtitle')}</p>
      <div className="mb-5">
        <SearchField
          label={t('help.search')}
          placeholder={t('help.search.placeholder')}
          className="w-full max-w-[420px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
      </div>
      {found.length === 0 ? (
        <Card className="max-w-[860px]">
          <EmptyState
            icon={SearchX}
            title={t('help.empty.title')}
            action={<Button onClick={list.clearFilters}>{t('help.empty.clear')}</Button>}
          >
            {t('help.empty.body', { term: search })}
          </EmptyState>
        </Card>
      ) : (
        <div className="flex max-w-[860px] flex-col gap-5">
          {topics.map((topic) => (
            <section key={topic} aria-labelledby={`help-topic-${topic}`}>
              <h2
                id={`help-topic-${topic}`}
                className="m-0 mb-2 text-[13px] font-semibold tracking-[0.02em] text-text-3 uppercase"
              >
                {t(`help.topic.${topic}` as TranslationKey)}
              </h2>
              <Card>
                <ul className="m-0 list-none divide-y divide-border p-0">
                  {found
                    .filter((entry) => entry.topic === topic)
                    .map((entry) => (
                      <ArticleRow key={entry.slug} entry={entry} />
                    ))}
                </ul>
              </Card>
            </section>
          ))}
        </div>
      )}
      <Card className="mt-6 max-w-[860px]">
        <Link to="/help/api" className={cardLinkClasses}>
          <span className="grid size-9 flex-none place-items-center rounded-[10px] bg-accent-soft text-accent-ink">
            <Braces aria-hidden />
          </span>
          <span className="min-w-0 flex-1">
            <b className="block text-[14px] font-semibold text-text">{t('help.api.title')}</b>
            <span className="mt-0.5 block text-[13px] text-text-2">{t('help.api.summary')}</span>
          </span>
          <ChevronRight aria-hidden className="size-4 flex-none text-text-3" />
        </Link>
      </Card>
    </>
  );
}
