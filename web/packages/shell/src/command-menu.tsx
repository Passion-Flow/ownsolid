import { useI18n, supportedLocales } from '@ownsolid/i18n';
import { ModalSurface, usePreferences, resolveTheme, useToast } from '@ownsolid/ui';
import { useRouter } from '@tanstack/react-router';
import { Command } from 'cmdk';
import { Building2, Languages, LogOut, Moon, Rows3, Sun, type LucideIcon } from 'lucide-react';
import { useEffect, useRef, type ReactNode } from 'react';
import type { NavGroup } from './navigation.ts';
import { useWorkspaces } from './workspace.ts';

const groupClasses =
  '[&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:text-[11.5px] [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:text-text-3';

function Item({
  icon: Icon,
  onSelect,
  children,
}: {
  icon: LucideIcon;
  onSelect: () => void;
  children: ReactNode;
}) {
  return (
    <Command.Item
      onSelect={onSelect}
      className="flex cursor-pointer items-center gap-2.5 rounded-control px-3 py-[9px] text-text-2 data-[selected=true]:bg-accent-soft data-[selected=true]:text-text"
    >
      <Icon aria-hidden />
      <span className="min-w-0 truncate">{children}</span>
    </Command.Item>
  );
}

/** Props for {@link CommandMenu}. */
export interface CommandMenuProps {
  open: boolean;
  onClose: () => void;
  navigation: readonly NavGroup[];
  onSignOut: () => void;
}

/** Search and run: actions first, then every page that has shipped. */
export function CommandMenu({ open, onClose, navigation, onSignOut }: CommandMenuProps) {
  const { t, locale, setLocale } = useI18n();
  const router = useRouter();
  const toast = useToast();
  const { theme, setTheme, density, setDensity } = usePreferences();
  const workspaces = useWorkspaces();
  const input = useRef<HTMLInputElement>(null);

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
      <Command label={t('command.label')} loop>
        <Command.Input
          ref={input}
          placeholder={t('command.placeholder')}
          className="w-full border-0 border-b border-border bg-transparent px-5 py-[18px] text-base text-text outline-none placeholder:text-text-3"
        />
        <Command.List className="max-h-[360px] overflow-y-auto p-2">
          <Command.Empty className="px-3 py-[9px] text-text-3">{t('command.empty')}</Command.Empty>
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
            <Item icon={LogOut} onSelect={run(onSignOut)}>
              {t('signOut.action')}
            </Item>
          </Command.Group>
          <Command.Group heading={t('command.goTo')} className={groupClasses}>
            {pages.map((item) => (
              <Item
                key={item.label}
                icon={item.icon}
                onSelect={run(() => {
                  void router.navigate({ to: item.to });
                })}
              >
                {t(item.label)}
              </Item>
            ))}
          </Command.Group>
        </Command.List>
      </Command>
    </ModalSurface>
  );
}
