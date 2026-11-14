// One account on its own page: its details, what was done to it, and what it did
// (docs/research/resource-detail-pages.md).
import { getUser, isProblem, type User } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Card, Notice, StatusPill, Tabs } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import {
  ResourceActivity,
  ResourceHeader,
  ResourceUnavailable,
  useChosenTab,
} from './resource-page.tsx';
import { statuses, UserFacts, usersKey, useUserActions } from './user-surface.tsx';

const route = getRouteApi('/authenticated/frame/users/$userId');

const tabs = ['overview', 'activity', 'actions'] as const;

function Loaded({ user }: { user: User }) {
  const { t } = useI18n();
  const [tab, setTab] = useChosenTab(tabs);
  const actions = useUserActions(user, () => undefined);
  const state = statuses[user.status];
  return (
    <>
      <ResourceHeader
        backTo="/users"
        backLabel={t('users.page.back')}
        title={user.display_name || user.email}
        description={user.display_name ? user.email : undefined}
        status={<StatusPill tone={state.tone}>{t(state.label)}</StatusPill>}
        actions={actions.buttons}
      />
      {actions.notices}
      {actions.self ? <Notice className="mb-4" title={t('users.detail.self')} /> : null}
      <Tabs
        label={t('resource.tabs')}
        value={tab}
        tabs={tabs.map((value) => ({ value, label: t(`resource.tab.${value}`) }))}
        onChange={setTab}
      >
        {tab === 'overview' ? (
          <Card className="p-[18px]">
            <UserFacts user={user} />
          </Card>
        ) : null}
        {tab === 'activity' ? <ResourceActivity target={user.id} since={user.created_at} /> : null}
        {tab === 'actions' ? <ResourceActivity actor={user.id} since={user.created_at} /> : null}
      </Tabs>
      {actions.dialogs}
    </>
  );
}

/** `/users/$userId`: one account, reachable by a link that names it. */
export function UserPage() {
  const { t } = useI18n();
  const { userId } = route.useParams();
  const user = useQuery({
    queryKey: [...usersKey, 'detail', userId],
    queryFn: ({ signal }) => getUser(userId, { signal }),
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  if (user.data) return <Loaded user={user.data} />;
  return (
    <ResourceUnavailable
      pending={user.isPending}
      error={user.error}
      onRetry={() => {
        void user.refetch();
      }}
      backTo="/users"
      backLabel={t('users.page.back')}
      missingTitle={t('users.page.missing.title')}
      missingDetail={t('users.page.missing.detail')}
      failedTitle={t('users.detail.missing')}
    />
  );
}
