import { getLicenseStatus, type LicenseStatus as Status } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { cn } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

/** The query the sidebar's license condition reads; a page that changes the license refreshes it. */
export const licenseStatusKey = ['license', 'status'] as const;

/** The name of each license condition. */
export const licenseStateLabels: Record<Status['state'], TranslationKey> = {
  active: 'license.active',
  grace: 'license.grace',
  expired: 'license.expired',
  missing: 'license.missing',
  not_yet_valid: 'license.notYetValid',
  clock_suspect: 'license.clockSuspect',
};

/** The license condition at the foot of the sidebar. Shows nothing until it is known. */
export function LicenseStatus({
  to,
  onNavigate,
}: {
  to: string;
  onNavigate?: (() => void) | undefined;
}) {
  const { t } = useI18n();
  const { data } = useQuery({
    queryKey: licenseStatusKey,
    queryFn: ({ signal }) => getLicenseStatus({ signal }),
    staleTime: 60_000,
  });
  if (!data) return null;
  const healthy = data.state === 'active' && !data.warning;
  return (
    <Link
      to={to}
      onClick={onNavigate}
      className="flex items-center gap-2.5 rounded-[12px] bg-surface-2 px-3 py-2.5 transition-colors duration-150 hover:bg-surface-3"
    >
      <span
        aria-hidden
        className={cn(
          'size-2 shrink-0 rounded-full',
          healthy ? 'bg-good' : data.new_business_allowed ? 'bg-warning' : 'bg-critical',
        )}
      />
      <div className="min-w-0">
        <b className="block text-[12.5px]">{t('license.heading')}</b>
        <small className="block text-[11.5px] text-text-3">
          {t(licenseStateLabels[data.state])}
        </small>
      </div>
    </Link>
  );
}
