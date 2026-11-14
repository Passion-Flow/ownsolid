// Which help a page shows: the entrance comes from the session, the language from the interface.
import type { Entrance, HelpLocale } from '@ownsolid/help';
import { useI18n } from '@ownsolid/i18n';
import { useSession } from '../session.ts';

/** The help of the entrance the person signed in to, in their language. */
export function useHelp(): { entrance: Entrance; locale: HelpLocale } {
  const me = useSession();
  const { locale } = useI18n();
  return { entrance: me?.audience === 'admin' ? 'console' : 'portal', locale };
}
