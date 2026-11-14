// What a license's state means now and what to do next, for the License page and the overview.
import { licenseDaysUntil, renewalNoticeDays, type License } from '@ownsolid/api';
import type { useI18n } from '@ownsolid/i18n';

/** The query that holds the installation's license. */
export const licenseKey = ['license', 'admin'] as const;

/** What to tell a person about a license. */
export interface LicenseStanding {
  tone: 'info' | 'critical';
  title: string;
  next: string;
}

/** The standing of a license, or null when there is nothing to say. */
export function licenseStanding(
  license: License,
  { t, format }: Pick<ReturnType<typeof useI18n>, 't' | 'format'>,
): LicenseStanding | null {
  const { current, pending } = license;
  switch (license.state) {
    case 'missing':
      return {
        tone: 'critical',
        title: t('license.standing.missing'),
        next: t('license.standing.missing.next'),
      };
    case 'not_yet_valid':
      return {
        tone: 'info',
        title: t('license.standing.notYetValid', { date: format.date(pending?.not_before ?? '') }),
        next: t('license.standing.notYetValid.next'),
      };
    case 'grace':
      return {
        tone: 'info',
        title: t('license.standing.grace', { date: format.date(current?.expires_at ?? '') }),
        next: t('license.standing.grace.next', {
          date: format.dateTime(current?.grace_until ?? ''),
        }),
      };
    case 'expired':
      return {
        tone: 'critical',
        title: t('license.standing.expired'),
        next: t('license.standing.expired.next', {
          date: format.dateTime(current?.grace_until ?? ''),
        }),
      };
    case 'clock_suspect':
      return {
        tone: 'critical',
        title: t('license.standing.clock'),
        next: t('license.standing.clock.next', { date: format.dateTime(license.observed_at) }),
      };
    case 'active': {
      if (!current || pending) return null;
      const days = licenseDaysUntil(current.expires_at, license);
      if (days > renewalNoticeDays) return null;
      return {
        tone: 'info',
        title: t('license.standing.renewal', { count: days }),
        next: t('license.standing.renewal.next', { date: format.date(current.expires_at) }),
      };
    }
  }
}
