// The navigation model an application hands to the frame.
import type { TranslationKey } from '@ownsolid/i18n';
import type { LucideIcon } from 'lucide-react';

/** One destination. Without `to` the page has not shipped, and the item shows disabled. */
export interface NavItem {
  label: TranslationKey;
  icon: LucideIcon;
  to?: string;
  /** Explains why a destination without a route is unavailable. */
  unavailableReason?: TranslationKey;
}

/** Items under a heading; the first group usually has none. */
export interface NavGroup {
  heading?: TranslationKey;
  items: readonly NavItem[];
}

/** Finds the item for a path, with its group. */
export function findNavItem(
  groups: readonly NavGroup[],
  pathname: string,
): { group: NavGroup; item: NavItem } | undefined {
  for (const group of groups) {
    for (const item of group.items) {
      if (!item.to) continue;
      if (item.to === pathname || (item.to !== '/' && pathname.startsWith(`${item.to}/`))) {
        return { group, item };
      }
    }
  }
  return undefined;
}
