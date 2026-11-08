import type { NavGroup, SingleKeyShortcut } from '@ownsolid/shell';
import { Boxes, ClipboardCheck, Cpu, Gauge, House, KeyRound, MessagesSquare } from 'lucide-react';

/** The portal's navigation. An item gets a `to` when its page ships. */
export const navigation: readonly NavGroup[] = [
  {
    items: [{ label: 'nav.home', icon: House, to: '/' }],
  },
  {
    heading: 'nav.portalGroup.tools',
    items: [
      { label: 'nav.apps', icon: Boxes, unavailableReason: 'nav.portalUnavailable.apps' },
      {
        label: 'nav.conversations',
        icon: MessagesSquare,
        unavailableReason: 'nav.portalUnavailable.conversations',
      },
      { label: 'nav.portalModels', icon: Cpu, to: '/models' },
    ],
  },
  {
    heading: 'nav.portalGroup.workspace',
    items: [
      { label: 'nav.keys', icon: KeyRound, to: '/keys' },
      { label: 'nav.resources', icon: Gauge, unavailableReason: 'nav.portalUnavailable.usage' },
      { label: 'nav.supported', icon: ClipboardCheck, to: '/support' },
    ],
  },
];

/** The portal's single-key actions. */
export const shortcuts: readonly SingleKeyShortcut[] = [
  { key: 'k', label: 'shortcuts.apiKey', to: '/keys', hash: 'issue' },
];
