// The administrator's view of a workspace's gateway keys: everyone's keys, with who holds each
// (docs/research/gateway-keys.md).
import { getUser } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { GatewayKeys, IssueKeyButton, useHashDialog, useWorkspaces } from '@ownsolid/shell';
import { Skeleton } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { PublishModelHint } from './publish-model-hint.tsx';
import { usersKey } from './user-surface.tsx';

/** A key's owner by name, or the first block of their identifier if it could not be read. */
function OwnerName({ id }: { id: string }) {
  const person = useQuery({
    queryKey: [...usersKey, 'detail', id],
    queryFn: ({ signal }) => getUser(id, { signal }),
  }).data;
  return <>{person ? person.display_name || person.email : id.slice(0, 8)}</>;
}

const ownerName = (id: string) => <OwnerName id={id} />;

/** `/gateway`: the keys of the workspace in the switcher. `#issue` opens the issue dialog. */
export function GatewayPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const workspace = workspaces.active?.id;
  const [issuing, setIssuing] = useHashDialog('issue');
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('gateway.title')}
          </h1>
          <p className="m-0 text-text-2">
            {workspaces.active
              ? t('gateway.subtitle', { workspace: workspaces.active.name })
              : t('gateway.subtitle.none')}
          </p>
        </div>
        <IssueKeyButton
          onClick={() => {
            setIssuing(true);
          }}
        />
      </div>
      {workspace ? (
        <GatewayKeys
          workspace={workspace}
          issuing={issuing}
          onIssue={setIssuing}
          owner={ownerName}
          noModel={<PublishModelHint />}
        />
      ) : (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-11 w-full" />
          <Skeleton className="h-11 w-full" />
        </div>
      )}
    </>
  );
}
