// The models of a workspace, what is arriving, and what this release can import. The list follows
// the workspace switcher (docs/research/model-library.md).
import {
  getModelCatalog,
  isProblem,
  listModelImports,
  listModels,
  type ModelImport,
  type ModelVersion,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  runtimeConfigQuery,
  SavedViewsMenu,
  useListView,
  useTypedFilter,
  useWorkspaces,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  buttonVariants,
  Card,
  Drawer,
  EmptyState,
  FilterSelect,
  Notice,
  ProblemNotice,
  SearchField,
  Skeleton,
  StatusPill,
  Table,
  Tabs,
  Tag,
  Td,
  Th,
  Tr,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ArrowRight, Boxes, PackageOpen, Upload } from 'lucide-react';
import { useMemo } from 'react';
import { ModelFacts, modelSummary, totalBytes } from './model-details.tsx';
import { importState, modelKinds, modelKeys } from './model-state.ts';
import { useChosenTab } from './resource-page.tsx';

const tabs = ['models', 'imports', 'catalogue'] as const;

const kinds = ['approved_base', 'merged_lora'] as const;

const definition: ListDefinition = {
  key: 'models',
  filters: {
    kind: { kind: 'choice', values: kinds },
    name: { kind: 'text', maximum: 100 },
  },
  columns: [
    { id: 'name', label: 'models.column.name', locked: true },
    { id: 'license', label: 'models.column.license' },
    { id: 'size', label: 'models.column.size' },
    { id: 'context', label: 'models.column.context' },
    { id: 'added', label: 'models.column.added' },
  ],
};

function Waiting() {
  return (
    <div className="flex flex-col gap-3" aria-busy>
      <Skeleton className="h-11 w-full" />
      <Skeleton className="h-11 w-full" />
    </div>
  );
}

function ImportLink({ primary = false }: { primary?: boolean }) {
  const { t } = useI18n();
  return (
    <Link
      to="/library/import"
      className={buttonVariants({ variant: primary ? 'primary' : 'secondary' })}
    >
      <Upload aria-hidden />
      {t('models.import.open')}
    </Link>
  );
}

function Rows({ items, list }: { items: readonly ModelVersion[]; list: ListView }) {
  const { t, format } = useI18n();
  const shows = list.isColumnVisible;
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('models.title')}</caption>
        <thead>
          <tr>
            <Th>{t('models.column.name')}</Th>
            {shows('license') ? <Th>{t('models.column.license')}</Th> : null}
            {shows('size') ? <Th>{t('models.column.size')}</Th> : null}
            {shows('context') ? <Th>{t('models.column.context')}</Th> : null}
            {shows('added') ? <Th>{t('models.column.added')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((model) => {
            const summary = modelSummary(model);
            return (
              <Tr key={model.id}>
                <Td>
                  <button
                    type="button"
                    aria-label={t('models.detail.open', { name: model.name })}
                    className="cursor-pointer rounded-control border-0 bg-transparent p-0 text-left font-semibold text-text hover:underline"
                    onClick={() => {
                      list.setOpenRow(model.id);
                    }}
                  >
                    {model.name}
                  </button>
                  {model.kind === 'merged_lora' ? (
                    <span className="ml-2">
                      <Tag>{t(modelKinds[model.kind])}</Tag>
                    </span>
                  ) : null}
                  <div className="font-mono text-[12px] text-text-2">{summary.repository}</div>
                </Td>
                {shows('license') ? <Td>{summary.license}</Td> : null}
                {shows('size') ? (
                  <Td className="whitespace-nowrap">{format.bytes(summary.bytes)}</Td>
                ) : null}
                {shows('context') ? (
                  <Td className="whitespace-nowrap">
                    {t('models.fact.context.tokens', { count: summary.contextTokens })}
                  </Td>
                ) : null}
                {shows('added') ? (
                  <Td className="text-text-2">{format.dateTime(model.created_at)}</Td>
                ) : null}
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

function ModelDrawer({ model, onClose }: { model: ModelVersion | null; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Drawer open={model !== null} onClose={onClose} title={model?.name ?? ''}>
      {model ? (
        <>
          <Link
            to="/library/$modelId"
            params={{ modelId: model.id }}
            className="mb-4 inline-flex items-center gap-1.5 font-medium text-accent-ink hover:underline"
          >
            {t('resource.openPage')}
            <ArrowRight aria-hidden className="size-4" />
          </Link>
          <ModelFacts summary={modelSummary(model)} />
        </>
      ) : null}
    </Drawer>
  );
}

function ModelsTab({ workspace }: { workspace: string }) {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const list = useListView(definition);
  const { kind, name } = list.state.filters;
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const models = useQuery({
    ...modelKeys.list(workspace),
    queryFn: ({ signal }) => listModels(workspace, { signal }),
  });
  // A workspace's directory comes whole, so filtering here sees every model.
  const items = useMemo(() => {
    const term = name?.toLowerCase() ?? '';
    return (models.data ?? []).filter(
      (model) =>
        (kind === undefined || model.kind === kind) && model.name.toLowerCase().includes(term),
    );
  }, [models.data, kind, name]);
  const filtered = Object.keys(list.state.filters).length > 0;
  let body;
  if (models.isPending) {
    body = <Waiting />;
  } else if (!models.data) {
    body = isProblem(models.error) ? (
      <ProblemNotice
        problem={models.error}
        onRetry={() => {
          void models.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={Boxes}
          title={filtered ? t('models.emptyFilter.title') : t('models.empty.title')}
          action={
            filtered ? (
              <Button onClick={list.clearFilters}>{t('models.emptyFilter.clear')}</Button>
            ) : (
              <ImportLink primary />
            )
          }
        >
          {filtered ? t('models.emptyFilter.detail') : t('models.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = <Rows items={items} list={list} />;
  }
  return (
    <>
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('models.search')}
          placeholder={t('models.search.placeholder')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <FilterSelect
          label={t('models.filter.kind')}
          value={kind ?? ''}
          options={[
            { value: '', label: t('models.filter.kind.all') },
            ...kinds.map((value) => ({ value, label: t(modelKinds[value]) })),
          ]}
          onChange={(event) => {
            list.setFilter('kind', event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      <ModelDrawer
        model={models.data?.find((model) => model.id === list.openRow) ?? null}
        onClose={() => {
          list.setOpenRow(null);
        }}
      />
    </>
  );
}

function ImportRows({ items }: { items: readonly ModelImport[] }) {
  const { t, format } = useI18n();
  return (
    <Card className="overflow-x-auto">
      <Table>
        <caption className="sr-only">{t('models.tab.imports')}</caption>
        <thead>
          <tr>
            <Th>{t('models.column.name')}</Th>
            <Th>{t('models.imports.column.state')}</Th>
            <Th>{t('models.imports.column.uploaded')}</Th>
            <Th>{t('models.imports.column.started')}</Th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const state = importState(item);
            return (
              <Tr key={item.id}>
                <Td className="font-semibold">
                  <Link
                    to="/library/imports/$importId"
                    params={{ importId: item.id }}
                    className="text-text underline-offset-2 hover:underline"
                  >
                    {item.name}
                  </Link>
                </Td>
                <Td>
                  <StatusPill tone={state.tone}>{t(state.label)}</StatusPill>
                </Td>
                <Td className="whitespace-nowrap text-text-2">
                  {t('models.import.progress', {
                    sent: format.bytes(item.uploaded_bytes),
                    total: format.bytes(item.expected_bytes),
                  })}
                </Td>
                <Td className="text-text-2">{format.dateTime(item.created_at)}</Td>
              </Tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

function ImportsTab({ workspace }: { workspace: string }) {
  const { t } = useI18n();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const imports = useQuery({
    ...modelKeys.imports(workspace),
    queryFn: ({ signal }) => listModelImports(workspace, { signal, background: true }),
    refetchInterval: poll,
  });
  if (imports.isPending) return <Waiting />;
  if (!imports.data) {
    return isProblem(imports.error) ? (
      <ProblemNotice
        problem={imports.error}
        onRetry={() => {
          void imports.refetch();
        }}
      />
    ) : null;
  }
  if (imports.data.length === 0) {
    return (
      <Card>
        <EmptyState
          icon={PackageOpen}
          title={t('models.imports.empty.title')}
          action={<ImportLink primary />}
        >
          {t('models.imports.empty.detail')}
        </EmptyState>
      </Card>
    );
  }
  return <ImportRows items={imports.data} />;
}

function CatalogueTab() {
  const { t } = useI18n();
  const catalog = useQuery({
    queryKey: modelKeys.catalog,
    queryFn: ({ signal }) => getModelCatalog({ signal }),
  });
  if (catalog.isPending) return <Waiting />;
  if (!catalog.data) {
    return isProblem(catalog.error) ? (
      <ProblemNotice
        problem={catalog.error}
        onRetry={() => {
          void catalog.refetch();
        }}
      />
    ) : null;
  }
  const entry = catalog.data.catalog;
  return (
    <>
      <Notice title={t('models.catalogue.notReady.title')} className="mb-3.5">
        {t('models.catalogue.notReady.detail')}
      </Notice>
      <Card className="p-[18px]">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 className="m-0 text-[15px] font-semibold">{entry.repository}</h2>
          <ImportLink />
        </div>
        <ModelFacts
          summary={{
            repository: entry.repository,
            revision: entry.revision,
            license: entry.license,
            format: entry.format,
            architecture: entry.architecture,
            modelType: entry.model_type,
            contextTokens: entry.max_context,
            files: entry.files,
            bytes: totalBytes(entry.files),
          }}
        />
      </Card>
    </>
  );
}

/** `/library`: the models of the workspace in the switcher. */
export function ModelLibraryPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const [tab, setTab] = useChosenTab(tabs);
  const workspace = workspaces.active?.id;
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('models.title')}
          </h1>
          <p className="m-0 text-text-2">
            {workspaces.active
              ? t('models.subtitle', { workspace: workspaces.active.name })
              : t('models.subtitle.none')}
          </p>
        </div>
        <ImportLink primary />
      </div>
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`models.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'catalogue' ? <CatalogueTab /> : null}
        {tab !== 'catalogue' && !workspace ? <Waiting /> : null}
        {tab === 'models' && workspace ? <ModelsTab workspace={workspace} /> : null}
        {tab === 'imports' && workspace ? <ImportsTab workspace={workspace} /> : null}
      </Tabs>
    </>
  );
}
