import { useI18n, supportedLocales } from '@ownsolid/i18n';
import { ModalSurface, usePreferences, resolveTheme, useToast } from '@ownsolid/ui';
import { useRouter } from '@tanstack/react-router';
import { Command, defaultFilter, useCommandState } from 'cmdk';
import {
  Building2,
  Keyboard,
  Languages,
  LogOut,
  Moon,
  RotateCw,
  Rows3,
  Sun,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useCommandSearch, type SearchSource } from './command-search.ts';
import type { NavGroup } from './navigation.ts';
import { useWorkspaces } from './workspace.ts';

const groupClasses =
  '[&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:text-[11.5px] [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:text-text-3';

// A row the server found already matches, so the menu must not filter it again. Ranked below any
// real match, it sorts after the actions and pages that fit what was typed.
const foundPrefix = 'found:';
const foundRank = 0.001;

function rank(value: string, search: string, keywords?: string[]): number {
  return value.startsWith(foundPrefix) ? foundRank : defaultFilter(value, search.trim(), keywords);
}

function Item({
  icon: Icon,
  value,
  kind,
  detail,
  onSelect,
  children,
}: {
  icon: LucideIcon;
  /** What the menu matches and tracks the row by; the label unless the row was found. */
  value?: string;
  kind?: string | undefined;
  detail?: string | undefined;
  onSelect: () => void;
  children: string;
}) {
  return (
    <Command.Item
      value={value ?? children}
      onSelect={onSelect}
      className="flex cursor-pointer items-center gap-2.5 rounded-control px-3 py-[9px] text-text-2 data-[selected=true]:bg-accent-soft data-[selected=true]:text-text"
    >
      <Icon aria-hidden className="shrink-0" />
      <span className="min-w-0 truncate">{children}</span>
      {detail ? <span className="min-w-0 truncate text-xs text-text-3">{detail}</span> : null}
      {kind ? <span className="ml-auto shrink-0 pl-3 text-xs text-text-3">{kind}</span> : null}
    </Command.Item>
  );
}

/** Says what a search came to, for people who cannot see the rows arrive (WCAG 4.1.3). */
function SearchStatus({ pending }: { pending: boolean }) {
  const { t } = useI18n();
  const count = useCommandState((state) => state.filtered.count);
  const search = useCommandState((state) => state.search);
  let message = '';
  if (pending) message = t('command.searching');
  else if (search !== '') {
    message = count === 0 ? t('command.empty') : t('command.results', { count });
  }
  return (
    <p role="status" className="sr-only">
      {message}
    </p>
  );
}

/** Props for {@link CommandMenu}. */
export interface CommandMenuProps {
  open: boolean;
  onClose: () => void;
  navigation: readonly NavGroup[];
  onSignOut: () => void;
  onShortcuts: () => void;
  /** What can be found on the server besides the menu's own actions and pages. */
  search?: readonly SearchSource[] | undefined;
}

const noSources: readonly SearchSource[] = [];

/** Search and run: actions first, then every page that has shipped. */
export function CommandMenu({
  open,
  onClose,
  navigation,
  onSignOut,
  onShortcuts,
  search = noSources,
}: CommandMenuProps) {
  const { t, locale, setLocale } = useI18n();
  const router = useRouter();
  const toast = useToast();
  const { theme, setTheme, density, setDensity } = usePreferences();
  const workspaces = useWorkspaces();
  const input = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState('');
  // Each opening starts a new search.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (!open) setTyped('');
  }
  const found = useCommandSearch(search, typed);

  // The dialog would focus cmdk's root, which is focusable but cannot be typed in.
  useEffect(() => {
    if (open) input.current?.focus();
  }, [open]);

  const dark = resolveTheme(theme) === 'dark';
  const compact = density === 'compact';
  const run = (action: () => void) => () => {
    onClose();
    action();
  };
  const pages = navigation
    .flatMap((group) => group.items)
    .flatMap((item) => (item.to ? [{ ...item, to: item.to }] : []));
  const otherWorkspaces = workspaces.items.filter((item) => item.id !== workspaces.active?.id);

  return (
    <ModalSurface
      open={open}
      onClose={onClose}
      label={t('command.label')}
      className="mx-auto mt-[16vh] mb-auto w-[580px] max-w-[94vw] overflow-hidden rounded-dialog"
    >
      <Command label={t('command.label')} loop filter={rank}>
        <SearchStatus pending={found.pending} />
        <Command.Input
          ref={input}
          value={typed}
          onValueChange={setTyped}
          placeholder={t(search.length > 0 ? 'command.placeholder.search' : 'command.placeholder')}
          className="w-full border-0 border-b border-border bg-transparent px-5 py-[18px] text-base text-text outline-none placeholder:text-text-3 focus-visible:shadow-[inset_0_-2px_0_var(--accent)]"
        />
        <Command.List className="max-h-[360px] overflow-y-auto p-2">
          {found.pending ? null : (
            <Command.Empty className="px-3 py-[9px] text-text-3">
              {t('command.empty')}
            </Command.Empty>
          )}
          <Command.Group heading={t('command.actions')} className={groupClasses}>
            <Item
              icon={dark ? Sun : Moon}
              onSelect={run(() => {
                setTheme(dark ? 'light' : 'dark');
              })}
            >
              {dark ? t('preferences.useLightTheme') : t('preferences.useDarkTheme')}
            </Item>
            <Item
              icon={Rows3}
              onSelect={run(() => {
                setDensity(compact ? 'comfortable' : 'compact');
              })}
            >
              {compact ? t('preferences.useComfortable') : t('preferences.useCompact')}
            </Item>
            {supportedLocales
              .filter((value) => value !== locale)
              .map((value) => (
                <Item
                  key={value}
                  icon={Languages}
                  onSelect={run(() => {
                    setLocale(value);
                  })}
                >
                  {t(`locale.${value}`)}
                </Item>
              ))}
            {otherWorkspaces.map((item) => (
              <Item
                key={item.id}
                icon={Building2}
                onSelect={run(() => {
                  workspaces.select(item);
                  toast({ message: t('workspace.switched', { name: item.name }) });
                })}
              >
                {t('command.switchWorkspace', { name: item.name })}
              </Item>
            ))}
            <Item icon={Keyboard} onSelect={run(onShortcuts)}>
              {t('shortcuts.title')}
            </Item>
            <Item icon={LogOut} onSelect={run(onSignOut)}>
              {t('signOut.action')}
            </Item>
          </Command.Group>
          <Command.Group heading={t('command.goTo')} className={groupClasses}>
            {pages.map((item) => (
              <Item
                key={item.label}
                icon={item.icon}
                kind={t('command.kind.page')}
                onSelect={run(() => {
                  void router.navigate({ to: item.to });
                })}
              >
                {t(item.label)}
              </Item>
            ))}
            {found.results.flatMap(({ source, hits, more, failed, retry }) => [
              ...hits.map((hit) => (
                <Item
                  key={`${source.key}:${hit.id}`}
                  value={`${foundPrefix}${source.key}:${hit.id}`}
                  icon={source.icon}
                  kind={t(source.kind)}
                  detail={hit.detail}
                  onSelect={run(() => {
                    void router.navigate({ href: hit.href });
                  })}
                >
                  {hit.label}
                </Item>
              )),
              more ? (
                <Item
                  key={`${source.key}:more`}
                  value={`${foundPrefix}${source.key}:more`}
                  icon={source.icon}
                  onSelect={run(() => {
                    void router.navigate({ href: more });
                  })}
                >
                  {t(source.seeAll, { term: typed.trim() })}
                </Item>
              ) : null,
              failed ? (
                <Item
                  key={`${source.key}:failed`}
                  value={`${foundPrefix}${source.key}:failed`}
                  icon={RotateCw}
                  onSelect={retry}
                >
                  {t(source.failed)}
                </Item>
              ) : null,
            ])}
          </Command.Group>
          {found.pending ? (
            <Command.Loading label={t('command.searching')} className="px-3 py-[9px] text-text-3">
              {t('command.searching')}
            </Command.Loading>
          ) : null}
        </Command.List>
      </Command>
    </ModalSurface>
  );
}
