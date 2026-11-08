// The page a person who uses the platform lands on: what needs them, where they left off and what
// has been published to their workspace (S-301, docs/research/portal-home.md). Each section reads
// its own source and fails on its own; none shows a figure the server does not have.
import {
  isProblem,
  listApplications,
  listConversations,
  listNotifications,
  markNotificationRead,
  type Application,
  type RunNotification,
  type RunOutcome,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { RunFailure, runtimeConfigQuery, useSession, useWorkspaces } from '@ownsolid/shell';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  cn,
  EmptyState,
  ProblemNotice,
  Skeleton,
  StatusPill,
  type StatusTone,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Boxes, CircleAlert, CircleCheck, MessagesSquare, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import { WorkspaceBody, WorkspaceSkeleton } from './workspace-body.tsx';

/** How many runs that need the person are named; the rest are behind the bell. */
const needsShown = 5;

/** The plan shows the five newest conversations. */
const conversationsShown = 5;

/** How many published applications are named before the rest are counted. */
const applicationsShown = 6;

/** The roadmap item that brings usage figures to the portal. */
const usageItem = 'POR-111';

const outcomeTones: Readonly<Record<RunOutcome, StatusTone>> = {
  succeeded: 'good',
  failed: 'critical',
  interrupted: 'warning',
  cancelled: 'neutral',
};

const outcomeLabels: Readonly<Record<RunOutcome, TranslationKey>> = {
  succeeded: 'notifications.outcome.succeeded',
  failed: 'notifications.outcome.failed',
  cancelled: 'notifications.outcome.cancelled',
  interrupted: 'notifications.outcome.interrupted',
};

const titleClasses = 'm-0 text-[13.5px] font-semibold break-words';
const detailClasses = 'm-0 mt-0.5 text-[13px] text-text-2';
const rowClasses = 'flex items-start gap-3 py-3 first:pt-0 last:pb-0';

/** A run of the person's in this workspace that did not succeed and is still unread. */
function needsYou(notification: RunNotification, workspaceId: string): boolean {
  return (
    notification.workspace_id === workspaceId &&
    notification.read_at === null &&
    notification.outcome !== 'succeeded'
  );
}

/**
 * The sources of the page, polled as background reads so an open page keeps no session alive.
 * Their keys name the workspace, so switching it never shows the previous one's rows.
 */
function useSources(workspaceId: string) {
  const config = useQuery(runtimeConfigQuery()).data;
  const refetchInterval = config?.ui.pollMilliseconds ?? false;
  const lookup = config?.ui.lookupPageSize ?? 100;
  const notifications = useQuery({
    // Under the bell's key, so reading a notice in either place refreshes both.
    queryKey: ['notifications', 'home', lookup],
    queryFn: ({ signal }) => listNotifications(lookup, null, { signal, background: true }),
    refetchInterval,
  });
  const conversations = useQuery({
    queryKey: ['home', 'conversations', workspaceId],
    queryFn: ({ signal }) =>
      listConversations(workspaceId, { limit: conversationsShown }, { signal, background: true }),
    refetchInterval,
  });
  // Read a lookup page, not only what is shown, so a run that needs the person can be named.
  const applications = useQuery({
    queryKey: ['home', 'applications', workspaceId, lookup],
    queryFn: ({ signal }) =>
      listApplications(workspaceId, { limit: lookup }, { signal, background: true }),
    refetchInterval,
  });
  return { notifications, conversations, applications };
}

type Sources = ReturnType<typeof useSources>;

/** What a section shows while its source has nothing to show. */
function SourceState({ query, skeleton }: { query: UseQueryResult; skeleton: string }): ReactNode {
  const { t } = useI18n();
  if (query.isPending) {
    return (
      <WorkspaceSkeleton>
        <Skeleton className={skeleton} />
      </WorkspaceSkeleton>
    );
  }
  if (isProblem(query.error)) {
    return (
      <ProblemNotice
        problem={query.error}
        detail={
          isProblem(query.error, 'RESOURCE_NOT_FOUND') ? t('home.workspaceUnavailable') : undefined
        }
        onRetry={() => {
          void query.refetch();
        }}
      />
    );
  }
  return null;
}

function NeedsRow({
  notification,
  names,
}: {
  notification: RunNotification;
  names: ReadonlyMap<string, string>;
}) {
  const { t, format } = useI18n();
  const queryClient = useQueryClient();
  const read = useMutation({
    mutationFn: () => markNotificationRead(notification.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });
  const name = names.get(notification.workflow_id) ?? t('home.needs.unnamed');
  const failed = notification.outcome === 'failed';
  const Icon = failed ? CircleAlert : TriangleAlert;
  return (
    <li className={rowClasses}>
      <span
        className={cn(
          'grid size-[30px] flex-none place-items-center rounded-[9px] bg-surface-2',
          failed ? 'text-critical' : 'text-text-2',
        )}
      >
        <Icon aria-hidden className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className={titleClasses}>{name}</p>
        <p className="m-0 mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[13px] text-text-2">
          <StatusPill tone={outcomeTones[notification.outcome]}>
            {t(outcomeLabels[notification.outcome])}
          </StatusPill>
          <span>{format.dateTime(notification.occurred_at)}</span>
        </p>
        {notification.error_code ? <RunFailure code={notification.error_code} /> : null}
        {isProblem(read.error) ? (
          <ProblemNotice
            className="mt-2"
            problem={read.error}
            onRetry={() => {
              read.mutate();
            }}
          />
        ) : null}
      </div>
      <Button
        size="sm"
        disabled={read.isPending}
        aria-label={t('home.needs.markRead', { name })}
        onClick={() => {
          read.mutate();
        }}
      >
        {t('notifications.markRead')}
      </Button>
    </li>
  );
}

function NeedsYou({
  sources,
  workspaceId,
  names,
}: {
  sources: Sources;
  workspaceId: string;
  names: ReadonlyMap<string, string>;
}) {
  const { t } = useI18n();
  const page = sources.notifications.data;
  const needs = page?.items.filter((item) => needsYou(item, workspaceId)) ?? [];
  let body: ReactNode;
  if (!page) {
    body = <SourceState query={sources.notifications} skeleton="h-[64px] w-full" />;
  } else if (needs.length === 0) {
    body = (
      <EmptyState className="py-4" icon={CircleCheck} title={t('home.needs.clear')}>
        {t('home.needs.clear.detail')}
      </EmptyState>
    );
  } else {
    body = (
      <>
        <ul className="m-0 list-none divide-y divide-border p-0">
          {needs.slice(0, needsShown).map((notification) => (
            <NeedsRow key={notification.id} notification={notification} names={names} />
          ))}
        </ul>
        {needs.length > needsShown || page.next_cursor ? (
          <p className="m-0 mt-3 text-[12.5px] text-text-3">{t('home.needs.more')}</p>
        ) : null}
      </>
    );
  }
  return (
    <Card aria-labelledby="home-needs">
      <CardHeader title={<span id="home-needs">{t('home.needs')}</span>} />
      <CardBody>{body}</CardBody>
    </Card>
  );
}

function Conversations({ sources }: { sources: Sources }) {
  const { t, format } = useI18n();
  const page = sources.conversations.data;
  let body: ReactNode;
  if (!page) {
    body = <SourceState query={sources.conversations} skeleton="h-[150px] w-full" />;
  } else if (page.items.length === 0) {
    body = (
      <EmptyState className="py-4" icon={MessagesSquare} title={t('home.conversations.empty')}>
        {t('home.conversations.empty.detail')}
      </EmptyState>
    );
  } else {
    body = (
      <ul className="m-0 list-none divide-y divide-border p-0">
        {page.items.map((conversation) => (
          <li key={conversation.id} className={rowClasses}>
            <span className="grid size-[30px] flex-none place-items-center rounded-[9px] bg-accent-soft text-accent-ink">
              <MessagesSquare aria-hidden className="size-4" />
            </span>
            <div className="min-w-0 flex-1">
              <p className={titleClasses}>{conversation.workflow_name}</p>
              <p className={detailClasses}>
                {t('home.conversations.detail', {
                  count: conversation.turn_count,
                  date: format.dateTime(conversation.updated_at),
                })}
              </p>
            </div>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <Card aria-labelledby="home-conversations">
      <CardHeader
        title={<span id="home-conversations">{t('home.conversations')}</span>}
        subtitle={t('home.conversations.hint')}
      />
      <CardBody>{body}</CardBody>
    </Card>
  );
}

function PublishedRow({ application }: { application: Application }) {
  const { t, format } = useI18n();
  return (
    <li className={rowClasses}>
      <span className="grid size-[30px] flex-none place-items-center rounded-[9px] bg-accent-soft text-accent-ink">
        <Boxes aria-hidden className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className={titleClasses}>{application.name}</p>
        <p className={detailClasses}>
          {t('home.apps.detail', {
            version: application.version_number,
            date: format.date(application.published_at),
          })}
        </p>
      </div>
    </li>
  );
}

function Published({ sources }: { sources: Sources }) {
  const { t } = useI18n();
  const page = sources.applications.data;
  let body: ReactNode;
  if (!page) {
    body = <SourceState query={sources.applications} skeleton="h-[150px] w-full" />;
  } else if (page.items.length === 0) {
    body = (
      <EmptyState className="py-4" icon={Boxes} title={t('home.apps.empty')}>
        {t('home.apps.empty.detail')}
      </EmptyState>
    );
  } else {
    const rest = page.items.length - applicationsShown;
    body = (
      <>
        <ul className="m-0 list-none divide-y divide-border p-0">
          {page.items.slice(0, applicationsShown).map((application) => (
            <PublishedRow key={application.workflow_id} application={application} />
          ))}
        </ul>
        {rest > 0 ? (
          <p className="m-0 mt-3 text-[12.5px] text-text-3">
            {t(page.next_cursor ? 'home.apps.moreThan' : 'home.apps.more', { count: rest })}
          </p>
        ) : null}
      </>
    );
  }
  return (
    <Card aria-labelledby="home-apps">
      <CardHeader title={<span id="home-apps">{t('home.apps')}</span>} />
      <CardBody>{body}</CardBody>
    </Card>
  );
}

/** Usage is POR-111: visible, with its reason, and not pretending to have figures. */
function Usage() {
  const { t } = useI18n();
  return (
    <Card aria-labelledby="home-usage">
      <CardHeader
        title={<span id="home-usage">{t('home.usage')}</span>}
        actions={<StatusPill tone="neutral">{t('capability.state.planned')}</StatusPill>}
      />
      <CardBody>
        <div className="text-[13.5px] text-text-2">
          <p className="m-0">{t('home.usage.reason')}</p>
          <p className="m-0 mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
            <span className="font-mono text-[12px] text-text-3">{usageItem}</span>
            <Link to="/support" className="font-semibold text-accent-ink hover:underline">
              {t('home.usage.supported')}
            </Link>
          </p>
        </div>
      </CardBody>
    </Card>
  );
}

function WorkspaceHome({ workspaceId }: { workspaceId: string }) {
  const sources = useSources(workspaceId);
  const names = new Map(
    (sources.applications.data?.items ?? []).map((item) => [item.workflow_id, item.name]),
  );
  for (const conversation of sources.conversations.data?.items ?? []) {
    if (!names.has(conversation.workflow_id)) {
      names.set(conversation.workflow_id, conversation.workflow_name);
    }
  }
  return (
    <div className="flex flex-col gap-4">
      <NeedsYou sources={sources} workspaceId={workspaceId} names={names} />
      <div className="grid items-start gap-4 lg:grid-cols-2">
        <Conversations sources={sources} />
        <Published sources={sources} />
      </div>
      <Usage />
    </div>
  );
}

/** `/` in the portal: continue where the person left off, in the workspace they chose. */
export function HomePage() {
  const { t } = useI18n();
  const me = useSession();
  const workspaces = useWorkspaces();
  if (!me) return null;
  const name = me.user.display_name || me.user.email;
  const active = workspaces.active;
  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('welcome.title', { name })}
      </h1>
      {active && !workspaces.isPending ? (
        <p className="m-0 mb-6 text-text-2">{t('home.subtitle', { workspace: active.name })}</p>
      ) : null}
      <div className={active && !workspaces.isPending ? undefined : 'mt-3'}>
        <WorkspaceBody
          workspaces={workspaces}
          noWorkspace={t('home.noWorkspace.detail')}
          loading={
            <div className="grid gap-4 lg:grid-cols-2">
              <Skeleton className="h-28 lg:col-span-2" />
              <Skeleton className="h-32" />
              <Skeleton className="h-32" />
              <Skeleton className="h-24 lg:col-span-2" />
            </div>
          }
        >
          {(workspaceId) => <WorkspaceHome key={workspaceId} workspaceId={workspaceId} />}
        </WorkspaceBody>
      </div>
    </>
  );
}
