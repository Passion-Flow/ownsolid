// A person's own API keys in the workspace chosen in the switcher: issue one, see how to call with
// it, rotate or revoke it, and read its calls (S-306, docs/research/portal-api-keys.md). The server
// returns only the session's own keys on these paths.
import { useI18n } from '@ownsolid/i18n';
import { GatewayKeys, IssueKeyButton, useHashDialog, useWorkspaces } from '@ownsolid/shell';
import { WorkspaceBody } from './workspace-body.tsx';

/** `/keys`: the person's keys. `#issue` opens the issue dialog. */
export function KeysPage() {
  const { t } = useI18n();
  const workspaces = useWorkspaces();
  const active = workspaces.active;
  const [issuing, setIssuing] = useHashDialog('issue');
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('keys.title')}
          </h1>
          <p className="m-0 text-text-2">
            {active ? t('keys.subtitle', { workspace: active.name }) : t('keys.subtitle.none')}
          </p>
        </div>
        {active ? (
          <IssueKeyButton
            onClick={() => {
              setIssuing(true);
            }}
          />
        ) : null}
      </div>
      <WorkspaceBody workspaces={workspaces} noWorkspace={t('keys.noWorkspace.detail')}>
        {(workspace) => (
          <GatewayKeys
            key={workspace}
            workspace={workspace}
            issuing={issuing}
            onIssue={setIssuing}
            noModel={t('keys.noModel.detail')}
          />
        )}
      </WorkspaceBody>
    </>
  );
}
