import type { NavGroup, SingleKeyShortcut } from '@ownsolid/shell';
import {
  Activity,
  BadgeCheck,
  Bell,
  Boxes,
  ClipboardCheck,
  Cpu,
  Database,
  DatabaseBackup,
  FlaskConical,
  Gauge,
  KeyRound,
  LayoutDashboard,
  Library,
  ListChecks,
  Mail,
  Network,
  Package,
  Plug,
  Route,
  ScrollText,
  Server,
  Settings,
  ShieldCheck,
  Users,
  Workflow,
} from 'lucide-react';

/** The console's one-character shortcuts. One gets a `to` when its page ships. */
export const shortcuts: readonly SingleKeyShortcut[] = [
  { key: 'd', label: 'shortcuts.deploy' },
  { key: 'i', label: 'shortcuts.install' },
  { key: 'n', label: 'shortcuts.addNode', to: '/nodes', hash: 'add' },
  { key: 'k', label: 'shortcuts.apiKey' },
];

/** The console's navigation. An item gets a `to` when its page ships. */
export const navigation: readonly NavGroup[] = [
  { items: [{ label: 'nav.overview', icon: LayoutDashboard, to: '/' }] },
  {
    heading: 'nav.group.infrastructure',
    items: [
      { label: 'nav.nodes', icon: Server, to: '/nodes' },
      { label: 'nav.accelerators', icon: Cpu },
      { label: 'nav.clusters', icon: Network },
    ],
  },
  {
    heading: 'nav.group.applications',
    items: [
      { label: 'nav.catalog', icon: Package },
      { label: 'nav.workflows', icon: Workflow },
      { label: 'nav.plugins', icon: Plug },
    ],
  },
  {
    heading: 'nav.group.models',
    items: [
      { label: 'nav.models', icon: Boxes },
      { label: 'nav.library', icon: Library, to: '/library' },
      { label: 'nav.gateway', icon: Route },
    ],
  },
  {
    heading: 'nav.group.training',
    items: [
      { label: 'nav.datasets', icon: Database },
      { label: 'nav.finetune', icon: FlaskConical },
      { label: 'nav.evaluation', icon: Gauge },
    ],
  },
  {
    heading: 'nav.group.governance',
    items: [
      { label: 'nav.users', icon: Users, to: '/users' },
      { label: 'nav.roles', icon: ShieldCheck },
      { label: 'nav.sso', icon: KeyRound, to: '/sso' },
      { label: 'nav.audit', icon: ScrollText, to: '/audit' },
    ],
  },
  {
    heading: 'nav.group.operations',
    items: [
      { label: 'nav.tasks', icon: ListChecks, to: '/tasks' },
      { label: 'nav.monitoring', icon: Activity },
      { label: 'nav.alerts', icon: Bell },
      { label: 'nav.backups', icon: DatabaseBackup },
    ],
  },
  {
    heading: 'nav.group.system',
    items: [
      { label: 'nav.license', icon: BadgeCheck, to: '/license' },
      { label: 'nav.mail', icon: Mail, to: '/mail' },
      { label: 'nav.settings', icon: Settings, to: '/settings' },
      { label: 'nav.supported', icon: ClipboardCheck, to: '/support' },
    ],
  },
];
