// `/help/api`: every operation this entrance serves, generated from the contracts of this version
// (plan S-108). The index lists operations without loading anything; a contract's details load
// when one of its operations is opened. The page sends no request to the API.
import {
  apiDomains,
  contractUrl,
  loadApiDescriptions,
  loadApiDomain,
  type ApiDomain,
  type ApiOperation,
  type ApiSchema,
  type ApiDomainSummary,
  type OperationSummary,
} from '@ownsolid/help';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Notice,
  SearchField,
  Skeleton,
  Table,
  Td,
  Th,
  Tr,
  cn,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link, useLocation, useNavigate } from '@tanstack/react-router';
import { ArrowLeft, ChevronDown, Download, SearchX } from 'lucide-react';
import { useMemo } from 'react';
import type { ListDefinition } from '../list/list-state.ts';
import { useListView } from '../list/use-list-view.ts';
import { useTypedFilter } from '../list/use-typed-filter.ts';
import { runtimeConfigQuery } from '../routes.tsx';
import { useHelp } from './entrance.ts';

const definition: ListDefinition = {
  key: 'help-api',
  filters: { q: { kind: 'text', maximum: 100 } },
  columns: [],
};

/** How deep nested object fields are listed before the reference stops expanding them. */
const fieldDepth = 4;

/** The address anchor of an operation, unique across the entrance's contracts. */
function operationAnchor(domain: string, id: string): string {
  return `${domain}.${id}`;
}

function operationMatches(operation: OperationSummary, term: string): boolean {
  const wanted = term.trim().toLowerCase();
  return (
    wanted === '' ||
    operation.path.toLowerCase().includes(wanted) ||
    operation.id.toLowerCase().includes(wanted) ||
    operation.method.includes(wanted)
  );
}

function Method({ method }: { method: string }) {
  return (
    <span
      className={cn(
        'inline-flex h-[22px] min-w-[58px] flex-none items-center justify-center rounded-[6px] font-mono text-[11.5px] font-semibold uppercase',
        method === 'get' ? 'bg-accent-soft text-accent-ink' : 'bg-surface-2 text-text',
      )}
    >
      {method}
    </span>
  );
}

/** A schema's type in a few words, following references by name. */
function typeLabel(schema: ApiSchema): string {
  if (schema.$ref) return schema.$ref;
  if (schema.const !== undefined) return JSON.stringify(schema.const);
  if (schema.enum) return schema.enum.map((value) => JSON.stringify(value)).join(' | ');
  const alternatives = schema.anyOf ?? schema.oneOf;
  if (alternatives) return alternatives.map(typeLabel).join(' | ');
  if (schema.type === 'array' && schema.items) return `${typeLabel(schema.items)}[]`;
  const type = Array.isArray(schema.type) ? schema.type.join(' | ') : (schema.type ?? 'any');
  return schema.format ? `${type} (${schema.format})` : type;
}

/** The object a schema describes, through a reference or an array of it. */
function objectOf(schema: ApiSchema, schemas: Record<string, ApiSchema>): ApiSchema | undefined {
  const resolved = schema.$ref ? schemas[schema.$ref] : schema;
  if (!resolved) return undefined;
  if (resolved.properties) return resolved;
  if (resolved.items) return objectOf(resolved.items, schemas);
  const alternative = (resolved.anyOf ?? resolved.oneOf)?.find((item) => objectOf(item, schemas));
  return alternative ? objectOf(alternative, schemas) : undefined;
}

interface FieldRow {
  name: string;
  depth: number;
  required: boolean;
  schema: ApiSchema;
}

/** Every field of an object, nested fields indented below their parent. */
function fieldRows(
  schema: ApiSchema,
  schemas: Record<string, ApiSchema>,
  depth = 0,
  seen: readonly string[] = [],
): FieldRow[] {
  const object = objectOf(schema, schemas);
  if (!object?.properties || depth >= fieldDepth) return [];
  return Object.entries(object.properties).flatMap(([name, property]) => {
    const row = {
      name,
      depth,
      required: object.required?.includes(name) ?? false,
      schema: property,
    };
    // A schema that contains itself is listed once.
    const reference = property.$ref ?? property.items?.$ref;
    if (reference && seen.includes(reference)) return [row];
    const next = reference ? [...seen, reference] : seen;
    return [row, ...fieldRows(property, schemas, depth + 1, next)];
  });
}

/**
 * A description in the reader's language. The translations load with the first operation opened;
 * until then a description shows as the contract wrote it.
 */
function useDescribe(): (text: string | undefined) => string {
  const { locale } = useI18n();
  const translations = useQuery({
    queryKey: ['help', 'api', 'descriptions', locale],
    queryFn: () => loadApiDescriptions(locale),
    staleTime: 'static',
  });
  return (text) => (text ? (translations.data?.[text] ?? text) : '');
}

function Fields({
  schema,
  schemas,
  caption,
}: {
  schema: ApiSchema;
  schemas: Record<string, ApiSchema>;
  caption: string;
}) {
  const { t } = useI18n();
  const describe = useDescribe();
  const rows = fieldRows(schema, schemas);
  if (rows.length === 0) {
    return (
      <p className="m-0 font-mono text-[12.5px] text-text-2">
        {caption}: {typeLabel(schema)}
      </p>
    );
  }
  return (
    <div className="overflow-x-auto rounded-card border border-border">
      <Table aria-label={caption}>
        <thead>
          <tr>
            <Th>{t('help.api.field')}</Th>
            <Th>{t('help.api.type')}</Th>
            <Th>{t('help.api.notes')}</Th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <Tr key={index}>
              <Td className="font-mono text-[12.5px]">
                <span style={{ paddingLeft: `${String(row.depth * 16)}px` }}>{row.name}</span>
              </Td>
              <Td className="font-mono text-[12px] whitespace-normal text-text-2">
                {typeLabel(row.schema)}
              </Td>
              <Td className="text-[12.5px] whitespace-normal text-text-2">
                {[
                  row.required ? t('help.api.required') : '',
                  row.schema.maxLength !== undefined
                    ? t('help.api.maxLength', { count: row.schema.maxLength })
                    : '',
                  describe(row.schema.description),
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </Td>
            </Tr>
          ))}
        </tbody>
      </Table>
    </div>
  );
}

function Errors({ errors }: { errors: Record<string, string[]> }) {
  const { t, knows } = useI18n();
  const entries = Object.entries(errors).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) {
    return <p className="m-0 text-[13px] text-text-2">{t('help.api.errors.undeclared')}</p>;
  }
  return (
    <ul className="m-0 flex list-none flex-col gap-2 p-0">
      {entries.flatMap(([status, codes]) =>
        codes.map((code) => {
          const title = `problem.${code}.title`;
          const detail = `problem.${code}.detail`;
          return (
            <li key={`${status}-${code}`} className="text-[13px]">
              <span className="font-mono text-[12px] text-text-3">{status}</span>{' '}
              <span className="font-mono text-[12.5px] font-semibold">{code}</span>
              {knows(title) && knows(detail) ? (
                <span className="text-text-2">
                  {' '}
                  — {t('help.api.meaning', { title: t(title), detail: t(detail) })}
                </span>
              ) : null}
            </li>
          );
        }),
      )}
    </ul>
  );
}

function OperationDetail({ operation, domain }: { operation: ApiOperation; domain: ApiDomain }) {
  const { t } = useI18n();
  const describe = useDescribe();
  const parameters = operation.parameters;
  return (
    <div className="flex flex-col gap-4 border-t border-border px-5 pt-4 pb-5">
      <section>
        <h4 className="m-0 mb-2 text-[13px] font-semibold">{t('help.api.parameters')}</h4>
        {parameters.length === 0 ? (
          <p className="m-0 text-[13px] text-text-2">{t('help.api.parameters.none')}</p>
        ) : (
          <div className="overflow-x-auto rounded-card border border-border">
            <Table aria-label={t('help.api.parameters')}>
              <thead>
                <tr>
                  <Th>{t('help.api.field')}</Th>
                  <Th>{t('help.api.in')}</Th>
                  <Th>{t('help.api.type')}</Th>
                  <Th>{t('help.api.notes')}</Th>
                </tr>
              </thead>
              <tbody>
                {parameters.map((parameter) => (
                  <Tr key={`${parameter.in}-${parameter.name}`}>
                    <Td className="font-mono text-[12.5px]">
                      {parameter.name}
                      {parameter.required ? (
                        <span className="ml-1.5 font-sans text-[12px] text-text-3">
                          {t('help.api.required')}
                        </span>
                      ) : null}
                    </Td>
                    <Td className="text-[12.5px]">{t(`help.api.in.${parameter.in}`)}</Td>
                    <Td className="font-mono text-[12px] whitespace-normal text-text-2">
                      {typeLabel(parameter.schema)}
                    </Td>
                    <Td className="text-[12.5px] whitespace-normal text-text-2">
                      {describe(parameter.description ?? parameter.schema.description)}
                    </Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          </div>
        )}
      </section>
      {operation.body ? (
        <section>
          <h4 className="m-0 mb-2 text-[13px] font-semibold">{t('help.api.body')}</h4>
          <Fields schema={operation.body} schemas={domain.schemas} caption={t('help.api.body')} />
        </section>
      ) : null}
      {operation.responses.map((response) => (
        <section key={response.status}>
          <h4 className="m-0 mb-2 text-[13px] font-semibold">
            {t('help.api.response', { status: response.status })}
          </h4>
          {response.schema ? (
            <Fields
              schema={response.schema}
              schemas={domain.schemas}
              caption={t('help.api.response', { status: response.status })}
            />
          ) : (
            <p className="m-0 text-[13px] text-text-2">{t('help.api.response.empty')}</p>
          )}
        </section>
      ))}
      <section>
        <h4 className="m-0 mb-2 text-[13px] font-semibold">{t('help.api.errors')}</h4>
        <Errors errors={operation.errors} />
      </section>
    </div>
  );
}

function Operation({
  summary,
  domain,
  open,
}: {
  summary: OperationSummary;
  domain: string;
  open: boolean;
}) {
  const { t } = useI18n();
  const { entrance } = useHelp();
  const navigate = useNavigate();
  const reference = useQuery({
    queryKey: ['help', 'api', entrance, domain],
    queryFn: () => loadApiDomain(entrance, domain),
    enabled: open,
    staleTime: 'static',
  });
  const operation = reference.data?.operations.find((item) => item.id === summary.id);
  // Operation IDs repeat across contracts, so the anchor names the contract too.
  const anchor = operationAnchor(domain, summary.id);
  const detailId = `${anchor}-detail`;
  return (
    <li id={anchor} className="scroll-mt-20">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={detailId}
        onClick={() => {
          // The open operation is the address's anchor, so a link opens it for a colleague.
          void navigate({ to: '.', search: true, hash: open ? '' : anchor });
        }}
        className="flex w-full cursor-pointer items-center gap-3 px-5 py-3 text-left transition-colors duration-150 hover:bg-surface-2"
      >
        <Method method={summary.method} />
        <span className="min-w-0 flex-1">
          <span className="block font-mono text-[13px] break-all text-text">{summary.path}</span>
          <span className="block text-[12px] text-text-3">{summary.id}</span>
        </span>
        <ChevronDown
          aria-hidden
          className={cn('size-4 flex-none text-text-3 transition-transform', open && 'rotate-180')}
        />
      </button>
      {open ? (
        <div id={detailId}>
          {reference.data && operation ? (
            <OperationDetail operation={operation} domain={reference.data} />
          ) : reference.isError ? (
            <div className="px-5 pb-4">
              <Notice
                tone="critical"
                title={t('help.loadFailed.title')}
                actions={
                  <Button
                    onClick={() => {
                      void reference.refetch();
                    }}
                  >
                    {t('action.retry')}
                  </Button>
                }
              >
                {t('help.loadFailed.body')}
              </Notice>
            </div>
          ) : (
            <div aria-busy className="flex flex-col gap-2 px-5 pb-4">
              <Skeleton />
              <Skeleton className="h-4 w-1/2" />
            </div>
          )}
        </div>
      ) : null}
    </li>
  );
}

function SaveContract({ domain }: { domain: string }) {
  const { t } = useI18n();
  const { entrance } = useHelp();
  const url = useQuery({
    queryKey: ['help', 'contract', entrance, domain],
    queryFn: () => contractUrl(entrance, domain),
    staleTime: 'static',
  });
  if (!url.data) return null;
  return (
    <a
      href={url.data}
      download={`${domain}.openapi.json`}
      aria-label={t('help.api.save.label', { domain })}
      className="inline-flex items-center gap-1.5 text-[13px] font-semibold text-accent-ink hover:underline"
    >
      <Download aria-hidden className="size-4" />
      {t('help.api.save')}
    </a>
  );
}

function Domain({
  summary,
  search,
  hash,
}: {
  summary: ApiDomainSummary;
  search: string;
  hash: string;
}) {
  const { t, knows } = useI18n();
  const domainName = `help.api.domain.${summary.domain}`;
  const operations = summary.operations.filter((operation) => operationMatches(operation, search));
  if (operations.length === 0) return null;
  return (
    <Card aria-labelledby={`domain-${summary.domain}`}>
      <CardHeader
        title={
          <span id={`domain-${summary.domain}`}>
            {knows(domainName) ? t(domainName) : summary.domain}
          </span>
        }
        subtitle={t('help.api.count', { count: operations.length })}
        actions={<SaveContract domain={summary.domain} />}
      />
      <CardBody className="px-0 pb-1">
        <ul className="m-0 list-none divide-y divide-border p-0">
          {operations.map((operation) => (
            <Operation
              key={operation.id}
              summary={operation}
              domain={summary.domain}
              open={hash === operationAnchor(summary.domain, operation.id)}
            />
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}

/** Every operation of this entrance, from the contracts of this version. */
export function ApiReferencePage() {
  const { t } = useI18n();
  const { entrance } = useHelp();
  const hash = useLocation({ select: (location) => location.hash });
  const debounce = useQuery(runtimeConfigQuery()).data?.ui.searchDebounceMilliseconds ?? 0;
  const list = useListView(definition);
  const [typed, setTyped] = useTypedFilter(list, 'q', debounce);
  const search = list.state.filters.q ?? '';
  const domains = apiDomains(entrance);
  const matching = useMemo(
    () =>
      domains.some((domain) =>
        domain.operations.some((operation) => operationMatches(operation, search)),
      ),
    [domains, search],
  );

  return (
    <>
      <Link
        to="/help"
        className="mb-4 inline-flex items-center gap-1.5 text-[13px] font-semibold text-accent-ink hover:underline"
      >
        <ArrowLeft aria-hidden className="size-4" />
        {t('help.back')}
      </Link>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('help.api.title')}
      </h1>
      <p className="m-0 mb-6 max-w-[760px] text-text-2">{t('help.api.subtitle')}</p>
      <div className="mb-5">
        <SearchField
          label={t('help.api.search')}
          placeholder={t('help.api.search.placeholder')}
          className="w-full max-w-[420px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
      </div>
      {matching ? (
        <div className="flex max-w-[980px] flex-col gap-4">
          {domains.map((domain) => (
            <Domain key={domain.domain} summary={domain} search={search} hash={hash} />
          ))}
        </div>
      ) : (
        <Card>
          <EmptyState
            icon={SearchX}
            title={t('help.api.empty.title')}
            action={<Button onClick={list.clearFilters}>{t('help.empty.clear')}</Button>}
          >
            {t('help.empty.body', { term: search })}
          </EmptyState>
        </Card>
      )}
    </>
  );
}
