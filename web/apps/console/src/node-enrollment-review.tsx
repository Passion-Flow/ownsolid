import { useI18n } from '@ownsolid/i18n';
import { Button, CopyText, EmptyState, SelectField, StatusPill } from '@ownsolid/ui';
import { Link } from '@tanstack/react-router';
import { ListFilter } from 'lucide-react';
import { useState } from 'react';
import type { EnrollmentDraft, EnrollmentResult } from './node-enrollment-batch.ts';
import type { EnrollmentCsvRow } from './node-enrollment-csv.ts';

const filters = ['all', 'pending', 'consumed', 'issues'] as const;
type Filter = (typeof filters)[number];
/** Identifies the pending token to revoke and the row that owns it. */
export interface EnrollmentRevocation {
  line: number;
  name: string;
  id: string;
}

function EnrollmentRow({
  row,
  result,
  secret,
  onRevoke,
}: {
  row: EnrollmentCsvRow;
  result: EnrollmentResult | undefined;
  secret: string | undefined;
  onRevoke: ((target: EnrollmentRevocation) => void) | undefined;
}) {
  const { t, format } = useI18n();
  const state = row.issue ? 'invalid' : (result?.state ?? 'queued');
  const tone =
    state === 'consumed'
      ? 'good'
      : state === 'failed' || state === 'invalid'
        ? 'critical'
        : 'neutral';
  return (
    <li className="grid gap-2 border-t border-border py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div className="min-w-0">
        <p className="m-0 font-semibold wrap-anywhere">{row.name || t('nodes.bulk.unnamed')}</p>
        <p className="m-0 text-xs text-text-3 wrap-anywhere">
          {t('nodes.bulk.row', { line: row.line, rack: row.rack || t('nodes.bulk.noRack') })}
        </p>
      </div>
      <div className="min-w-0">
        <StatusPill tone={tone}>{t(`nodes.bulk.state.${state}`)}</StatusPill>
        {row.issue ? (
          <p className="mt-1 text-text-2">{t(`nodes.bulk.rowIssue.${row.issue}`)}</p>
        ) : null}
        {result?.state === 'failed' || result?.state === 'unknown' ? (
          <>
            <p className="mt-1 text-text-2">
              {t(
                result.state === 'unknown'
                  ? 'nodes.bulk.unknownDetail'
                  : `nodes.bulk.refusal.${result.reason}`,
              )}
            </p>
            {result.requestId ? (
              <p className="m-0 text-xs text-text-3 wrap-anywhere">
                {t('nodes.bulk.request', { id: result.requestId })}
              </p>
            ) : null}
          </>
        ) : null}
        {result?.state === 'pending' ? (
          <>
            <p className="mt-1 text-xs text-text-2">
              {t('nodes.bulk.expires', { time: format.dateTime(result.token.expires_at) })}
            </p>
            {secret ? (
              <CopyText text={secret} className="mt-1 [&>button]:min-h-7 [&>button]:min-w-7" />
            ) : (
              <p className="mt-1 text-xs text-text-2">{t('nodes.bulk.secretUnavailable')}</p>
            )}
            {onRevoke ? (
              <Button
                className="mt-1"
                size="sm"
                aria-label={t('nodes.bulk.revokeTitle', { name: row.name })}
                onClick={() => {
                  onRevoke({ line: row.line, name: row.name, id: result.token.id });
                }}
              >
                {t('nodes.pending.revoke')}
              </Button>
            ) : null}
          </>
        ) : null}
        {result?.state === 'consumed' && result.token.node_id ? (
          <Link
            to="/nodes/$nodeId"
            params={{ nodeId: result.token.node_id }}
            className="ml-2 text-accent-ink underline"
          >
            {t('nodes.bulk.openNode')}
          </Link>
        ) : null}
      </div>
    </li>
  );
}

/** Shows the CSV preview, individual results and per-rack enrollment counts. */
export function EnrollmentReview({
  draft,
  secrets,
  started,
  onRevoke,
}: {
  draft: EnrollmentDraft;
  secrets: Readonly<Partial<Record<number, string>>>;
  started: boolean;
  onRevoke: ((target: EnrollmentRevocation) => void) | undefined;
}) {
  const { t } = useI18n();
  const [filter, setFilter] = useState<Filter>('all');
  const racks = new Map<string, { total: number; pending: number; joined: number }>();
  for (const row of draft.rows) {
    if (row.issue) continue;
    const rack = racks.get(row.rack) ?? { total: 0, pending: 0, joined: 0 };
    const state = draft.results[row.line]?.state;
    rack.total++;
    if (state === 'pending') rack.pending++;
    if (state === 'consumed') rack.joined++;
    racks.set(row.rack, rack);
  }
  const rows = draft.rows.filter((row) => {
    const state = draft.results[row.line]?.state;
    if (filter === 'all') return true;
    if (filter === 'issues')
      return (
        Boolean(row.issue) || ['failed', 'unknown', 'revoked', 'expired'].includes(state ?? '')
      );
    return state === filter;
  });
  return (
    <>
      {started && racks.size > 0 ? (
        <div className="mt-3">
          <h3 className="m-0 font-semibold">{t('nodes.bulk.rackTitle')}</h3>
          <p className="mt-1 text-text-2">{t('nodes.bulk.rackSource')}</p>
          <ul aria-live="polite" className="mt-2 grid gap-2 p-0 sm:grid-cols-2 lg:grid-cols-3">
            {[...racks].map(([rack, counts]) => (
              <li
                key={rack}
                className="list-none rounded-field border border-border bg-surface-2 px-3 py-2"
              >
                <p className="m-0 font-semibold wrap-anywhere">{rack || t('nodes.bulk.noRack')}</p>
                <p className="m-0 text-text-2 tabular-nums">{t('nodes.bulk.rackCount', counts)}</p>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div>
        <h3 className="mb-2 text-sm font-semibold">
          {t('nodes.bulk.preview', { count: draft.rows.length })}
        </h3>
        <div className="mb-3 max-w-56">
          <SelectField
            label={t('nodes.bulk.filter')}
            value={filter}
            options={filters.map((value) => ({ value, label: t(`nodes.bulk.filter.${value}`) }))}
            onChange={(event) => {
              setFilter(filters.find((value) => value === event.target.value) ?? 'all');
            }}
          />
        </div>
        {rows.length === 0 ? (
          <EmptyState
            icon={ListFilter}
            title={t('nodes.bulk.emptyFilter')}
            action={
              <Button
                onClick={() => {
                  setFilter('all');
                }}
              >
                {t('nodes.bulk.clearFilter')}
              </Button>
            }
          >
            {t('nodes.bulk.emptyFilterDetail', { filter: t(`nodes.bulk.filter.${filter}`) })}
          </EmptyState>
        ) : (
          <ul
            className="m-0 max-h-[28rem] list-none overflow-y-auto p-0"
            aria-label={t('nodes.bulk.rowsLabel')}
          >
            {rows.map((row) => (
              <EnrollmentRow
                key={row.line}
                row={row}
                result={draft.results[row.line]}
                secret={secrets[row.line]}
                onRevoke={onRevoke}
              />
            ))}
          </ul>
        )}
      </div>
    </>
  );
}
