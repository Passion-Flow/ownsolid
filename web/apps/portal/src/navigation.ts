import type { NavGroup } from '@ownsolid/shell';
import { Boxes, ClipboardCheck, Gauge, House, MessagesSquare } from 'lucide-react';

/** The portal's navigation. An item gets a `to` when its page ships. */
export const navigation: readonly NavGroup[] = [
  {
    items: [
      { label: 'nav.home', icon: House, to: '/' },
      { label: 'nav.apps', icon: Boxes },
      { label: 'nav.conversations', icon: MessagesSquare },
      { label: 'nav.resources', icon: Gauge },
      { label: 'nav.supported', icon: ClipboardCheck, to: '/support' },
    ],
  },
];
