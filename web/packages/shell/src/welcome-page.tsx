import { useI18n } from '@ownsolid/i18n';
import { Card, CardBody, CopyText } from '@ownsolid/ui';
import { useSession } from './session.ts';

/** The signed-in start page: who is signed in, the workspace and the access level. */
export function WelcomePage() {
  const { t } = useI18n();
  const me = useSession();
  if (!me) return null;
  const name = me.user.display_name || me.user.email;
  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('welcome.title', { name })}
      </h1>
      <p className="m-0 text-text-2">{t('welcome.signedInAs', { email: me.user.email })}</p>
      <Card className="mt-6 max-w-[560px]">
        <CardBody className="pt-[18px]">
          <dl className="m-0 grid grid-cols-[130px_1fr] gap-y-2.5 text-[13.5px]">
            <dt className="text-text-3">{t('welcome.workspace')}</dt>
            <dd className="m-0">{me.workspace ? me.workspace.name : t('welcome.noWorkspace')}</dd>
            {me.workspace ? (
              <>
                <dt className="text-text-3">{t('welcome.role')}</dt>
                <dd className="m-0">{t(`role.${me.workspace.role}`)}</dd>
                <dt className="text-text-3">{t('welcome.workspaceId')}</dt>
                <dd className="m-0">
                  <CopyText text={me.workspace.id} />
                </dd>
              </>
            ) : null}
            <dt className="text-text-3">{t('welcome.access')}</dt>
            <dd className="m-0">
              {me.user.is_platform_admin ? t('welcome.platformAdmin') : t('welcome.member')}
            </dd>
          </dl>
        </CardBody>
      </Card>
    </>
  );
}
