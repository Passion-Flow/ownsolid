import type { NavGroup, SingleKeyShortcut } from '@ownsolid/shell';
import { Boxes, ClipboardCheck, Gauge, House, KeyRound, MessagesSquare } from 'lucide-react';

/** The portal's navigation. An item gets a `to` when its page ships. */
export const navigation: readonly NavGroup[] = [
  {
    items: [
      { label: 'nav.home', icon: House, to: '/' },
      { label: 'nav.apps', icon: Boxes },
      { label: 'nav.conversations', icon: MessagesSquare },
      { label: 'nav.keys', icon: KeyRound, to: '/keys' },
      { label: 'nav.resources', icon: Gauge },
      { label: 'nav.supported', icon: ClipboardCheck, to: '/support' },
    ],
  },
];

/** The portal's single-key actions. */
export const shortcuts: readonly SingleKeyShortcut[] = [
  { key: 'k', label: 'shortcuts.apiKey', to: '/keys', hash: 'issue' },
];
