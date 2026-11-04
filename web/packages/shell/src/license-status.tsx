import { getLicenseStatus, type LicenseStatus as Status } from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { cn } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';

const labels: Record<Status['state'], TranslationKey> = {
  active: 'license.active',
  grace: 'license.grace',
  expired: 'license.expired',
  missing: 'license.missing',
  not_yet_valid: 'license.notYetValid',
  clock_suspect: 'license.clockSuspect',
};

/** The license condition at the foot of the sidebar. Shows nothing until it is known. */
export function LicenseStatus() {
  const { t } = useI18n();
  const { data } = useQuery({
    queryKey: ['license', 'status'],
    queryFn: ({ signal }) => getLicenseStatus({ signal }),
    staleTime: 60_000,
  });
  if (!data) return null;
  const healthy = data.state === 'active' && !data.warning;
  return (
    <div className="flex items-center gap-2.5 rounded-[12px] bg-surface-2 px-3 py-2.5">
      <span
        aria-hidden
        className={cn(
          'size-2 shrink-0 rounded-full',
          healthy ? 'bg-good' : data.new_business_allowed ? 'bg-warning' : 'bg-critical',
        )}
      />
      <div className="min-w-0">
        <b className="block text-[12.5px]">{t('license.heading')}</b>
        <small className="block text-[11.5px] text-text-3">{t(labels[data.state])}</small>
      </div>
    </div>
  );
}
