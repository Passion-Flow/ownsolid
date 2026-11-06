// What the command menu finds on the server. Each application names its sources, one per resource,
// and each asks that resource's own list, so a person finds only what its page would show and one
// slow or failed source holds back only itself (docs/research/global-search.md).
import { isProblem } from '@ownsolid/api';
import { useI18n, type TranslationKey, type Translator } from '@ownsolid/i18n';
import { useQueries, useQuery } from '@tanstack/react-query';
import type { LucideIcon } from 'lucide-react';
import { useEffect, useState } from 'react';
import { runtimeConfigQuery } from './routes.tsx';

/** One thing a source found. */
export interface SearchHit {
  id: string;
  label: string;
  /** A second, quieter piece of text, such as an address or a state. */
  detail?: string | undefined;
  /** Where choosing it goes. */
  href: string;
}

/** What a source answers. */
export interface SearchAnswer {
  hits: readonly SearchHit[];
  /** The page that lists everything matching, when there is more than `hits`. */
  more?: string | undefined;
}

/** One kind of thing the command menu can find. */
export interface SearchSource {
  /** Names the source, such as `nodes`. */
  key: string;
  icon: LucideIcon;
  /** What one result is called. */
  kind: TranslationKey;
  /** The row that opens the page of everything matching, with `{term}`. */
  seeAll: TranslationKey;
  /** The row that says the source failed and offers a retry. */
  failed: TranslationKey;
  find: (term: string, context: { signal: AbortSignal; t: Translator }) => Promise<SearchAnswer>;
}

/** What one source has for the menu now. */
export interface SourceResult {
  source: SearchSource;
  hits: readonly SearchHit[];
  more: string | undefined;
  failed: boolean;
  retry: () => void;
}

/** What the command menu shows of a search. */
export interface CommandSearch {
  results: readonly SourceResult[];
  /** The typed text has not been answered yet. */
  pending: boolean;
}

/** Follows a value after it has stopped changing for `milliseconds`. */
function useSettled(value: string, milliseconds: number): string {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => {
      setSettled(value);
    }, milliseconds);
    return () => {
      clearTimeout(timer);
    };
  }, [value, milliseconds]);
  return value === '' ? '' : settled;
}

/** Asks every source about what was typed, once typing pauses. */
export function useCommandSearch(sources: readonly SearchSource[], typed: string): CommandSearch {
  const { t, locale } = useI18n();
  const pause = useQuery(runtimeConfigQuery()).data?.ui.searchDebounceMilliseconds ?? 0;
  const wanted = typed.trim();
  const term = useSettled(wanted, pause);
  const queries = useQueries({
    queries: sources.map((source) => ({
      queryKey: ['command-search', source.key, term, locale],
      queryFn: ({ signal }: { signal: AbortSignal }) => source.find(term, { signal, t }),
      enabled: term !== '',
      // Rows stay until the next answer replaces them, so the list does not flash between keys.
      placeholderData: (previous: SearchAnswer | undefined) => previous,
    })),
  });
  if (wanted === '') return { results: [], pending: false };
  const results = sources.flatMap((source, index) => {
    const query = queries[index];
    // A source this person may not use is left out, as it is from the navigation.
    if (!query || (isProblem(query.error) && query.error.status === 403)) return [];
    // Rows of the previous search may stay on screen, but its link would open the wrong list.
    const current = wanted === term && !query.isPlaceholderData;
    return [
      {
        source,
        hits: query.data?.hits ?? [],
        more: current ? query.data?.more : undefined,
        failed: query.isError && !query.isFetching,
        retry: () => {
          void query.refetch();
        },
        answeredAt: query.isSuccess && !query.isPlaceholderData ? query.dataUpdatedAt : Infinity,
      },
    ];
  });
  // Rows go below those already answered, so nothing moves under a key about to be pressed.
  const inArrivalOrder = [...results].sort((first, second) =>
    first.answeredAt === second.answeredAt ? 0 : first.answeredAt < second.answeredAt ? -1 : 1,
  );
  return {
    results: inArrivalOrder,
    pending: wanted !== term || queries.some((query) => query.isFetching),
  };
}
