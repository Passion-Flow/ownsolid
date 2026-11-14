// What this release can do: every option, whether it can be chosen, why not, and what would change
// that. It is drawn from the declaration the installer also reads, so it cannot drift from what
// the product offers (docs/research/capability-registry.md, plan S-106).
import { isProblem, type CapabilityRegistry, type CapabilityState } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CopyText,
  EmptyState,
  FilterTiles,
  ProblemNotice,
  SearchField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  Th,
  Tr,
  type StatusTone,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { SearchX } from 'lucide-react';
import { useId, useMemo } from 'react';
import {
  capabilitiesQuery,
  capabilityDetail,
  capabilityName,
  optionMatches,
} from './capabilities.ts';
import type { ListDefinition } from './list/list-state.ts';
import { useListView } from './list/use-list-view.ts';
import { useTypedFilter } from './list/use-typed-filter.ts';
import { runtimeConfigQuery } from './routes.tsx';

type Filter = CapabilityState | 'all';

const states: readonly CapabilityState[] = ['ready', 'planned', 'licensed', 'unsupported'];

const tones: Readonly<Record<CapabilityState, StatusTone>> = {
  ready: 'good',
  planned: 'neutral',
  licensed: 'warning',
  unsupported: 'critical',
};

const definition: ListDefinition = {
  key: 'capabilities',
  filters: {
    state: { kind: 'choice', values: states },
    q: { kind: 'text', maximum: 100 },
  },
  columns: [],
};

/** One registry's options, as one table. */
function RegistryTable({
  registry,
  visible,
}: {
  registry: CapabilityRegistry;
  visible: ReadonlySet<string>;
}) {
  const { t } = useI18n();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="mb-6">
      <h2 id={headingId} className="m-0 mb-0.5 text-[17px] font-semibold">
        {t(`capability.${registry.id}.title` as TranslationKey)}
      </h2>
      <p className="m-0 mb-2.5 text-text-2">
        {t(`capability.${registry.id}.description` as TranslationKey)}
      </p>
      <Card className="overflow-x-auto">
        <Table aria-labelledby={headingId} className="min-w-[720px] table-fixed">
          <colgroup>
            <col className="w-[28%]" />
            <col className="w-[150px]" />
            <col />
            <col className="w-[130px]" />
          </colgroup>
          <thead>
            <tr>
              <Th>{t('capability.column.option')}</Th>
              <Th>{t('capability.column.state')}</Th>
              <Th>{t('capability.column.detail')}</Th>
              <Th>{t('capability.column.item')}</Th>
            </tr>
          </thead>
          <tbody>
            {registry.options
              .filter((option) => visible.has(`${registry.id}/${option.id}`))
              .map((option) => (
                <Tr key={option.id}>
                  <Td className="font-medium whitespace-normal">
                    {capabilityName(t, registry, option)}
                  </Td>
                  <Td>
                    <StatusPill tone={tones[option.state]}>
                      {t(`capability.state.${option.state}`)}
                    </StatusPill>
                  </Td>
                  <Td className="py-2 whitespace-normal text-text-2">
                    {capabilityDetail(t, registry, option)}
                  </Td>
                  <Td>
                    {option.roadmap_item ? (
                      <CopyText text={option.roadmap_item} className="text-[12px]" />
                    ) : null}
                  </Td>
                </Tr>
              ))}
          </tbody>
        </Table>
      </Card>
    </section>
  );
}

/** Lists every option the product offers, one table per registry. */
export function SupportPage() {
  const { t } = useI18n();
  const debounce = useQuery(runtimeConfigQuery()).data?.ui.searchDebounceMilliseconds ?? 0;
  const list = useListView(definition);
  const [typed, setTyped] = useTypedFilter(list, 'q', debounce);
  const declaration = useQuery(capabilitiesQuery());
  const state = list.state.filters.state as CapabilityState | undefined;
  const search = list.state.filters.q ?? '';

  const registries = useMemo(() => declaration.data?.registries ?? [], [declaration.data]);
  const counts = useMemo(() => {
    const found = new Map<CapabilityState, number>();
    for (const registry of registries) {
      for (const option of registry.options) {
        found.set(option.state, (found.get(option.state) ?? 0) + 1);
      }
    }
    return found;
  }, [registries]);
  const visible = useMemo(() => {
    const found = new Set<string>();
    for (const registry of registries) {
      for (const option of registry.options) {
        const shown = [capabilityName(t, registry, option), capabilityDetail(t, registry, option)];
        if ((!state || option.state === state) && optionMatches(search, option, shown)) {
          found.add(`${registry.id}/${option.id}`);
        }
      }
    }
    return found;
  }, [registries, state, search, t]);

  let content;
  if (declaration.isPending) {
    content = (
      <Card className="flex flex-col gap-3 p-5">
        <Skeleton className="h-5 w-1/3" />
        <Skeleton />
        <Skeleton />
        <Skeleton />
      </Card>
    );
  } else if (declaration.isError) {
    content = isProblem(declaration.error) ? (
      <ProblemNotice
        problem={declaration.error}
        onRetry={() => {
          void declaration.refetch();
        }}
      />
    ) : null;
  } else {
    const total = [...counts.values()].reduce((sum, count) => sum + count, 0);
    // A state with no options has nothing to filter to, so it takes no tile.
    const tiles = states.flatMap((value) => {
      const count = counts.get(value);
      return count ? [{ value, label: t(`capability.state.${value}`), count }] : [];
    });
    content = (
      <>
        <FilterTiles<Filter>
          label={t('capability.filter')}
          value={state ?? 'all'}
          tiles={[{ value: 'all', label: t('capability.filter.all'), count: total }, ...tiles]}
          onChange={(value) => {
            list.setFilter('state', value === 'all' ? '' : value);
          }}
        />
        <div className="mb-5">
          <SearchField
            label={t('capability.search')}
            placeholder={t('capability.search.placeholder')}
            className="w-full max-w-[320px]"
            value={typed}
            onChange={(event) => {
              setTyped(event.target.value);
            }}
          />
        </div>
        {visible.size === 0 ? (
          <Card>
            <EmptyState
              icon={SearchX}
              title={t('capability.empty.title')}
              action={<Button onClick={list.clearFilters}>{t('capability.empty.clear')}</Button>}
            >
              {t('capability.empty.body')}
            </EmptyState>
          </Card>
        ) : (
          registries
            .filter((registry) =>
              registry.options.some((option) => visible.has(`${registry.id}/${option.id}`)),
            )
            .map((registry) => (
              <RegistryTable key={registry.id} registry={registry} visible={visible} />
            ))
        )}
      </>
    );
  }

  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('capability.page.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('capability.page.subtitle')}</p>
      {content}
    </>
  );
}
