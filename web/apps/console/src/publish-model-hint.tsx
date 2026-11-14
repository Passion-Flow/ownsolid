import { useI18n } from '@ownsolid/i18n';
import { Link } from '@tanstack/react-router';

/** What to do when no model can take a key: publish one, from a page an administrator has. */
export function PublishModelHint() {
  const { t } = useI18n();
  return (
    <>
      {t('gateway.issue.noModel.detail')}{' '}
      <Link to="/services" className="text-accent-ink underline">
        {t('gateway.issue.noModel.link')}
      </Link>
    </>
  );
}
