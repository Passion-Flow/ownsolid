// The sidebar: groups that open and close, a filter, and an icon-only rail
// (docs/research/console-navigation.md, ADR 0037).
import { useI18n } from '@ownsolid/i18n';
import { Brand, Button, SearchField, cn, usePreferences } from '@ownsolid/ui';
import { Link, useRouter, useRouterState } from '@tanstack/react-router';
import { ChevronRight, PanelLeftClose, PanelLeftOpen, Pin, PinOff } from 'lucide-react';
import { useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { LicenseStatus } from './license-status.tsx';
import { findNavItem, type NavGroup, type NavItem } from './navigation.ts';
import { RailTip } from './rail-tip.tsx';
import { usePins } from './use-pins.ts';

const itemClasses =
  'relative flex h-[34px] items-center gap-2.5 rounded-control px-2.5 font-medium text-text-2 no-underline';

// The current page is marked by a bar and by weight, so it does not rest on colour alone.
const linkClasses = cn(
  'transition-colors duration-150 hover:bg-surface-2 hover:text-text',
  'data-[status=active]:bg-accent-soft data-[status=active]:font-semibold data-[status=active]:text-accent-ink',
  'data-[status=active]:before:absolute data-[status=active]:before:inset-y-1.5 data-[status=active]:before:left-0 data-[status=active]:before:w-[3px] data-[status=active]:before:rounded-full data-[status=active]:before:bg-accent',
);

/** Pins a page or takes it off. It shows on hover and focus, and always where it is pinned. */
function PinButton({
  label,
  pinned,
  onToggle,
}: {
  label: string;
  pinned: boolean;
  onToggle: () => void;
}) {
  const { t } = useI18n();
  const Icon = pinned ? PinOff : Pin;
  return (
    <button
      type="button"
      aria-label={t(pinned ? 'nav.unpin' : 'nav.pin', { name: label })}
      onClick={onToggle}
      className={cn(
        'absolute top-[5px] right-1 grid size-6 cursor-pointer place-items-center rounded-control border-0 bg-transparent text-text-3 hover:bg-surface-3 hover:text-text',
        'group-hover/item:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100',
        pinned ? 'opacity-100' : 'opacity-0',
      )}
    >
      <Icon aria-hidden className="size-3.5" />
    </button>
  );
}

function Item({
  item,
  rail,
  onNavigate,
  pin,
}: {
  item: NavItem;
  rail: boolean;
  onNavigate: (() => void) | undefined;
  /** Offers to pin the page; absent where a navigation has nothing to pin. */
  pin?: { pinned: boolean; onToggle: () => void } | undefined;
}) {
  const { t } = useI18n();
  const label = t(item.label);
  const content = (
    <>
      <item.icon aria-hidden className="shrink-0" />
      <span className={rail ? 'sr-only' : 'truncate'}>{label}</span>
    </>
  );
  const row = item.to ? (
    <Link
      to={item.to}
      onClick={onNavigate}
      activeOptions={{ exact: item.to === '/' }}
      className={cn(itemClasses, linkClasses, rail && 'justify-center px-0', pin && 'pr-8')}
    >
      {content}
    </Link>
  ) : (
    <span
      aria-disabled
      title={t('nav.notAvailable')}
      className={cn(itemClasses, 'cursor-default opacity-50', rail && 'justify-center px-0')}
    >
      {content}
    </span>
  );
  return (
    <li className="group/item relative">
      {rail ? <RailTip label={label}>{row}</RailTip> : row}
      {pin && item.to ? <PinButton label={label} {...pin} /> : null}
    </li>
  );
}

function Group({
  group,
  open,
  current,
  onToggle,
  children,
}: {
  group: NavGroup;
  open: boolean;
  /** The current page is in this group. */
  current: boolean;
  /** Absent while a filter decides what shows, or for a group without a heading. */
  onToggle: (() => void) | undefined;
  children: ReactNode;
}) {
  const { t } = useI18n();
  const id = useId();
  const hiddenCurrent = current && !open;
  return (
    <div className="mt-3.5">
      {group.heading && onToggle ? (
        <button
          type="button"
          aria-expanded={open}
          aria-controls={id}
          aria-current={hiddenCurrent ? 'true' : undefined}
          onClick={onToggle}
          className={cn(
            'relative flex h-7 w-full cursor-pointer items-center justify-between rounded-control border-0 bg-transparent px-2.5 text-left text-[11.5px] font-semibold text-text-3 transition-colors duration-150 hover:bg-surface-2 hover:text-text',
            hiddenCurrent &&
              'font-bold text-accent-ink before:absolute before:inset-y-1.5 before:left-0 before:w-[3px] before:rounded-full before:bg-accent',
          )}
        >
          <span className="truncate">{t(group.heading)}</span>
          <ChevronRight
            aria-hidden
            className={cn(
              'size-3.5 shrink-0 motion-safe:transition-transform motion-safe:duration-150',
              open && 'rotate-90',
            )}
          />
        </button>
      ) : group.heading ? (
        <div className="px-2.5 pb-1 text-[11.5px] font-semibold text-text-3">
          {t(group.heading)}
        </div>
      ) : null}
      <ul id={id} hidden={!open} className="m-0 list-none p-0">
        {children}
      </ul>
    </div>
  );
}

/** The items a person pinned, leaving out any whose page has gone. */
function usePinnedGroup(groups: readonly NavGroup[], pinned: readonly string[]): NavGroup | null {
  return useMemo(() => {
    const destinations = groups.flatMap((group) => group.items);
    const items = pinned.flatMap((path) => destinations.filter((item) => item.to === path));
    return items.length > 0 ? { heading: 'nav.pinned', items } : null;
  }, [groups, pinned]);
}

/** Which groups the person opened or closed by hand. Moving to another page forgets it. */
function useToggledGroups() {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const [toggled, setToggled] = useState<ReadonlyMap<NavGroup, boolean>>(new Map());
  const [seen, setSeen] = useState(pathname);
  if (pathname !== seen) {
    setSeen(pathname);
    setToggled(new Map());
  }
  return { pathname, toggled, setToggled };
}

function Navigation({
  groups,
  onNavigate,
}: {
  groups: readonly NavGroup[];
  onNavigate: (() => void) | undefined;
}) {
  const { t } = useI18n();
  const router = useRouter();
  const { pathname, toggled, setToggled } = useToggledGroups();
  const { pinned, toggle } = usePins();
  const pinnedGroup = usePinnedGroup(groups, pinned);
  const filterBox = useRef<HTMLInputElement>(null);
  const [filter, setFilter] = useState('');
  const [filteredAt, setFilteredAt] = useState(pathname);
  if (pathname !== filteredAt) {
    setFilteredAt(pathname);
    setFilter('');
  }
  const currentGroup = findNavItem(groups, pathname)?.group;
  const term = filter.trim().toLowerCase();
  const matching = (group: NavGroup) =>
    group.items.filter((item) => t(item.label).toLowerCase().includes(term));
  const matches = term === '' ? [] : groups.flatMap(matching);
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape' && filter !== '') {
      event.stopPropagation();
      setFilter('');
    }
    if (event.key === 'Enter') {
      const first = matches.find((item) => item.to);
      if (!first?.to) return;
      onNavigate?.();
      setFilter('');
      void router.navigate({ to: first.to });
    }
  };
  // A small navigation has nothing to open, close or filter.
  const collapsible = groups.some((group) => group.heading);
  const pinOf = (item: NavItem) =>
    collapsible && item.to
      ? {
          pinned: pinned.includes(item.to),
          onToggle: () => {
            if (item.to) toggle(item.to);
          },
        }
      : undefined;
  return (
    <nav aria-label={t('nav.label')} className="px-2.5 pt-1 pb-5">
      {pinnedGroup && term === '' ? (
        <div className="mb-3.5">
          <Group group={pinnedGroup} open current={false} onToggle={undefined}>
            {pinnedGroup.items.map((item) => {
              const pin = pinOf(item);
              return (
                <Item
                  key={item.label}
                  item={item}
                  rail={false}
                  onNavigate={onNavigate}
                  pin={
                    pin && {
                      ...pin,
                      // The button goes with its row, and focus must not go with it.
                      onToggle: () => {
                        pin.onToggle();
                        filterBox.current?.focus();
                      },
                    }
                  }
                />
              );
            })}
          </Group>
        </div>
      ) : null}
      {collapsible ? (
        <SearchField
          ref={filterBox}
          label={t('nav.filter')}
          placeholder={t('nav.filter')}
          className="mb-1 h-8 text-[13px]"
          value={filter}
          onChange={(event) => {
            setFilter(event.target.value);
          }}
          onKeyDown={onKeyDown}
        />
      ) : null}
      {term !== '' ? (
        <p role="status" className={matches.length === 0 ? 'mx-2.5 mt-3 text-text-3' : 'sr-only'}>
          {matches.length === 0
            ? t('nav.filter.none')
            : t('nav.filter.count', { count: matches.length })}
        </p>
      ) : null}
      {groups.map((group) => {
        const shown = term === '' ? group.items : matching(group);
        if (shown.length === 0) return null;
        const holdsCurrent = group === currentGroup;
        return (
          <Group
            key={group.heading ?? 'start'}
            group={group}
            current={holdsCurrent}
            open={!group.heading || term !== '' || (toggled.get(group) ?? holdsCurrent)}
            onToggle={
              term === ''
                ? () => {
                    setToggled(new Map(toggled).set(group, !(toggled.get(group) ?? holdsCurrent)));
                  }
                : undefined
            }
          >
            {shown.map((item) => (
              <Item
                key={item.label}
                item={item}
                rail={false}
                onNavigate={onNavigate}
                pin={pinOf(item)}
              />
            ))}
          </Group>
        );
      })}
    </nav>
  );
}

function Rail({ groups }: { groups: readonly NavGroup[] }) {
  const { t } = useI18n();
  const { pinned } = usePins();
  const pinnedGroup = usePinnedGroup(groups, pinned);
  return (
    <nav aria-label={t('nav.label')} className="px-2 pt-1 pb-5">
      {(pinnedGroup ? [pinnedGroup, ...groups] : groups).map((group, index) => (
        <ul
          key={group.heading ?? 'start'}
          aria-label={group.heading ? t(group.heading) : undefined}
          className={cn('m-0 list-none p-0', index > 0 && 'mt-2 border-t border-border pt-2')}
        >
          {group.items.map((item) => (
            <Item key={item.label} item={item} rail onNavigate={undefined} />
          ))}
        </ul>
      ))}
    </nav>
  );
}

/** Props for {@link Sidebar}. */
export interface SidebarProps {
  groups: readonly NavGroup[];
  licensePath: string | undefined;
  /** The sidebar is in a drawer, which closes when a page is chosen and has no rail. */
  onNavigate?: (() => void) | undefined;
}

/** What the sidebar and its drawer hold. */
export function Sidebar({ groups, licensePath, onNavigate }: SidebarProps) {
  const { t } = useI18n();
  const { navigationCollapsed, setNavigationCollapsed } = usePreferences();
  const inDrawer = onNavigate !== undefined;
  const rail = navigationCollapsed && !inDrawer;
  const toggle = inDrawer ? null : (
    <Button
      variant="ghost"
      size="icon"
      aria-label={rail ? t('nav.expand') : t('nav.collapse')}
      onClick={() => {
        setNavigationCollapsed(!navigationCollapsed);
      }}
    >
      {rail ? <PanelLeftOpen aria-hidden /> : <PanelLeftClose aria-hidden />}
    </Button>
  );
  return (
    <>
      {rail ? (
        <div className="flex flex-col items-center gap-2 pt-[18px] pb-2.5">
          <Brand markOnly />
          {toggle}
        </div>
      ) : (
        <div className="flex items-center justify-between pt-[18px] pr-2.5 pb-2.5 pl-[18px]">
          <Brand />
          {toggle}
        </div>
      )}
      {rail ? <Rail groups={groups} /> : <Navigation groups={groups} onNavigate={onNavigate} />}
      {licensePath ? (
        <div className={cn('mt-auto border-t border-border', rail ? 'p-2' : 'p-3.5')}>
          <LicenseStatus to={licensePath} onNavigate={onNavigate} compact={rail} />
        </div>
      ) : null}
    </>
  );
}
