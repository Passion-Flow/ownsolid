// The pages a person keeps at the top of the sidebar (UX-124).
import { useI18n } from '@ownsolid/i18n';
import { usePreferences, useToast } from '@ownsolid/ui';

// The server's limit: a section this long has stopped being a shortcut.
const maximumPins = 12;

/** The person's pins, and how to change them. */
export function usePins() {
  const { t } = useI18n();
  const toast = useToast();
  const { pinnedDestinations, setPinnedDestinations } = usePreferences();
  return {
    pinned: pinnedDestinations,
    /** Pins a page, or unpins it if it is pinned. */
    toggle: (path: string) => {
      if (pinnedDestinations.includes(path)) {
        setPinnedDestinations(pinnedDestinations.filter((item) => item !== path));
      } else if (pinnedDestinations.length >= maximumPins) {
        toast({ message: t('nav.pin.full', { count: maximumPins }) });
      } else {
        setPinnedDestinations([...pinnedDestinations, path]);
      }
    },
  };
}
