import { isProblem, listAuditEvents, type AuditOutcome, type AuditPage } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  Card,
  EmptyState,
  SelectField,
  Skeleton,
  StatusPill,
  Table,
  Td,
  TextField,
  Th,
  Tr,
  type StatusTone,
} from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link, useRouter, useRouterState } from '@tanstack/react-router';
import { History, SearchX } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import {
  activitySearch,
  activitySelection,
  activityTimeFromField,
  customActivityWindow,
  localActivityTime,
  recentActivityWindow,
  type ActivitySelection,
  type ActivityWindow,
} from './activity-window.ts';
import { auditEventSearch } from './audit-link.ts';
import { ActionName } from './audit-names.tsx';
import { AuditReadFailure, useAuditRead } from './audit-read.tsx';

interface ActivityPage extends AuditPage {
  /** Client-only context: this is never decoded from or written to the API. */
  clientWindow: ActivityWindow;
}

interface ActivitySubject {
  /** Events about this resource. */
  target?: string | undefined;
  /** Events performed by this person. */
  actor?: string | undefined;
}

const outcomeTones: Record<AuditOutcome, StatusTone> = {
  success: 'good',
  accepted: 'neutral',
  failure: 'critical',
};

function ActivityLoading() {
  const { t } = useI18n();
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    // The interaction system avoids flashing a skeleton for reads under 300 ms.
    const timer = setTimeout(() => {
      setVisible(true);
    }, 300);
    return () => {
      clearTimeout(timer);
    };
  }, []);
  return visible ? (
    <div
      className="flex flex-col gap-3"
      role="status"
      aria-busy
      aria-label={t('audit.read.pending')}
    >
      <Skeleton className="h-10 w-full" />
      <Skeleton className="h-12 w-full" />
      <Skeleton className="h-12 w-full" />
    </div>
  ) : null;
}

/** A bounded, explicitly read history on a resource's own detail page. */
export function ResourceActivity(subject: ActivitySubject) {
  const location = useRouterState({ select: (state) => state.location });
  const selection = activitySelection(location.searchStr);
  return (
    <Activity
      key={JSON.stringify([subject.target, subject.actor, selection])}
      {...subject}
      selection={selection}
    />
  );
}

function Activity({
  target,
  actor,
  selection,
}: ActivitySubject & { selection: ActivitySelection }) {
  const { t, format } = useI18n();
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const applied = selection.kind === 'custom' ? customActivityWindow(selection) : null;
  const initialFrom = selection.kind === 'custom' ? localActivityTime(selection.from) : '';
  const initialTo = selection.kind === 'custom' ? localActivityTime(selection.to) : '';
  const [mode, setMode] = useState(selection.kind);
  const [from, setFrom] = useState(initialFrom);
  const [to, setTo] = useState(initialTo);
  const [submitted, setSubmitted] = useState(false);
  const fromField = useRef<HTMLInputElement>(null);
  const focusDates = useRef(false);
  useEffect(() => {
    if (mode === 'custom' && focusDates.current) {
      fromField.current?.focus();
      focusDates.current = false;
    }
  }, [mode]);
  const events = useAuditRead<ActivityPage>({
    queryKey: ['resource-activity', target, actor, selection, limit],
    enabled: selection.kind === 'recent' || applied !== null,
    read: async (cursor, signal, firstPage) => {
      // The cursor fingerprint covers the original query strings as well as its
      // pinned window. Every next page must reuse the successful first read's bounds.
      const window = cursor
        ? firstPage?.clientWindow
        : selection.kind === 'recent'
          ? recentActivityWindow(Date.now())
          : applied;
      if (!window) throw new Error('Activity read has no valid window');
      const page = await listAuditEvents(
        { limit, cursor: cursor ?? undefined, targetId: target, actorUserId: actor, ...window },
        { signal },
      );
      return { ...page, clientWindow: window };
    },
  });
  const snapshot = events.data?.pages[0]?.clientWindow;
  const dirty =
    mode !== selection.kind || (mode === 'custom' && (from !== initialFrom || to !== initialTo));
  const start = activityTimeFromField(from);
  const end = activityTimeFromField(to);
  const invalid = mode === 'custom' && (!start || !end || Date.parse(start) >= Date.parse(end));
  const showInvalid = invalid && (submitted || (selection.kind === 'custom' && !applied && !dirty));
  const items = events.data?.pages.flatMap((page) => page.items) ?? [];
  const choose = (next: ActivitySelection) => {
    void router.navigate({
      href: `${location.pathname}${activitySearch(location.searchStr, next)}`,
      replace: true,
    });
  };
  const chooseDates = () => {
    const window = snapshot ?? recentActivityWindow(Date.now());
    setFrom(localActivityTime(window.from));
    setTo(localActivityTime(window.to));
    setMode('custom');
    setSubmitted(false);
  };
  return (
    <>
      <form
        aria-label={t('resource.activity.controls')}
        noValidate
        className="mb-4 flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          if (events.denied || events.isFetching || (!dirty && events.error)) return;
          setSubmitted(true);
          if (invalid) {
            fromField.current?.focus();
            return;
          }
          if (!dirty) events.readAgain();
          else if (mode === 'recent') choose({ kind: 'recent' });
          else if (start && end) choose({ kind: 'custom', from: start, to: end });
        }}
      >
        <div className="grid items-end gap-3 sm:grid-cols-2 xl:grid-cols-[minmax(160px,1fr)_minmax(220px,1.5fr)_minmax(220px,1.5fr)_auto]">
          <SelectField
            label={t('resource.activity.period')}
            value={mode}
            disabled={events.denied}
            aria-disabled={events.denied || undefined}
            options={[
              { value: 'recent', label: t('resource.activity.recent') },
              { value: 'custom', label: t('resource.activity.custom') },
            ]}
            onChange={(event) => {
              if (event.target.value === 'custom') chooseDates();
              else setMode('recent');
            }}
          />
          {mode === 'custom' ? (
            <>
              <TextField
                ref={fromField}
                label={t('audit.window.from')}
                type="datetime-local"
                min="1000-01-01T00:00"
                max="9999-12-31T23:59:59.999"
                step="0.001"
                value={from}
                disabled={events.denied}
                aria-disabled={events.denied || undefined}
                error={
                  showInvalid
                    ? t(start && end ? 'audit.window.reversed' : 'audit.window.incomplete')
                    : undefined
                }
                onChange={(event) => {
                  setFrom(event.target.value);
                }}
              />
              <TextField
                label={t('audit.window.to')}
                type="datetime-local"
                min="1000-01-01T00:00"
                max="9999-12-31T23:59:59.999"
                step="0.001"
                value={to}
                disabled={events.denied}
                aria-disabled={events.denied || undefined}
                onChange={(event) => {
                  setTo(event.target.value);
                }}
              />
            </>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <Button
              type="submit"
              variant="primary"
              disabled={events.denied || events.isFetching || (!dirty && !!events.error)}
              aria-disabled={events.denied || undefined}
            >
              {t(events.isFetching ? 'resource.activity.reading' : 'resource.activity.read')}
            </Button>
            {dirty ? (
              <Button
                onClick={() => {
                  setMode(selection.kind);
                  setFrom(initialFrom);
                  setTo(initialTo);
                  setSubmitted(false);
                }}
              >
                {t('action.cancel')}
              </Button>
            ) : null}
          </div>
        </div>
        {mode === 'custom' ? (
          <p className="m-0 text-[12.5px] text-text-3">
            {t('resource.activity.zone', { zone: format.timeZone })}
          </p>
        ) : null}
        {events.denied ? <p className="m-0 text-text-2">{t('resource.activity.denied')}</p> : null}
      </form>
      {snapshot ? (
        <p className="mb-4 text-[13px] text-text-2" role="status">
          {t('resource.activity.showing', {
            from: format.dateTime(snapshot.from),
            to: format.dateTime(snapshot.to),
          })}
        </p>
      ) : null}
      {events.isPending && events.fetchStatus !== 'idle' ? <ActivityLoading /> : null}
      {events.error ? (
        <AuditReadFailure
          error={events.error}
          partial={!!events.data}
          busy={events.isFetching}
          onRetry={events.readAgain}
          detail={
            isProblem(events.error, 'VALIDATION_FAILED')
              ? t('resource.activity.refused')
              : undefined
          }
        />
      ) : null}
      {events.data && items.length === 0 && !events.error ? (
        <Card>
          <EmptyState
            icon={selection.kind === 'recent' ? History : SearchX}
            title={t(
              selection.kind === 'recent'
                ? 'resource.activity.empty.title'
                : 'resource.activity.filtered.title',
            )}
            action={
              <Button
                onClick={
                  selection.kind === 'recent'
                    ? () => {
                        if (mode === 'custom') fromField.current?.focus();
                        else focusDates.current = true;
                        chooseDates();
                      }
                    : () => {
                        choose({ kind: 'recent' });
                      }
                }
              >
                {t(
                  selection.kind === 'recent'
                    ? 'resource.activity.chooseDates'
                    : 'resource.activity.clearDates',
                )}
              </Button>
            }
          >
            {t(
              selection.kind === 'recent'
                ? 'resource.activity.empty.detail'
                : 'resource.activity.filtered.detail',
            )}
          </EmptyState>
        </Card>
      ) : null}
      {items.length > 0 ? (
        <Card className="overflow-hidden">
          <Table>
            <thead>
              <tr>
                <Th>{t('audit.column.time')}</Th>
                <Th>{t('audit.column.person')}</Th>
                <Th>{t('audit.column.action')}</Th>
                <Th>{t('audit.column.outcome')}</Th>
              </tr>
            </thead>
            <tbody>
              {items.map((event) => (
                <Tr key={event.id}>
                  <Td className="font-medium whitespace-nowrap">
                    <Link
                      to="/audit"
                      search={auditEventSearch(event)}
                      className="text-text underline-offset-2 hover:underline"
                    >
                      {format.dateTime(event.occurred_at)}
                    </Link>
                  </Td>
                  <Td className="text-text-2">
                    {event.actor_email ?? event.actor_user_id ?? t('audit.system')}
                  </Td>
                  <Td>
                    <ActionName action={event.action} />
                  </Td>
                  <Td>
                    <StatusPill tone={outcomeTones[event.outcome]}>
                      {t(`audit.outcome.${event.outcome}`)}
                    </StatusPill>
                  </Td>
                </Tr>
              ))}
            </tbody>
          </Table>
        </Card>
      ) : null}
      {events.hasNextPage && !events.error ? (
        <Button
          className="mt-4"
          disabled={events.isFetching}
          onClick={() => {
            void events.fetchNextPage({ cancelRefetch: false });
          }}
        >
          {t('audit.loadMore')}
        </Button>
      ) : null}
    </>
  );
}
