import { useI18n, supportedLocales } from '@ownsolid/i18n';
import { Menu, MenuItem, MenuLabel, MenuSeparator } from '@ownsolid/ui';
import { Check, LogOut } from 'lucide-react';
import { useSession } from './session.ts';

/** Props for {@link UserMenu}. */
export interface UserMenuProps {
  onSignOut: () => void;
}

/** The avatar and its menu: who is signed in, language and sign-out. */
export function UserMenu({ onSignOut }: UserMenuProps) {
  const { t, locale, setLocale } = useI18n();
  const me = useSession();
  if (!me) return null;
  const name = me.user.display_name || me.user.email;
  return (
    <Menu
      className="min-w-[240px]"
      trigger={
        <button
          type="button"
          aria-label={t('user.menu')}
          title={name}
          className="grid size-8 shrink-0 cursor-pointer place-items-center rounded-full border-0 bg-accent-soft text-[13px] font-[650] text-accent-ink"
        >
          {name.slice(0, 1).toUpperCase()}
        </button>
      }
    >
      <div className="px-2.5 py-1.5">
        <p className="m-0 truncate font-semibold">{name}</p>
        <p className="m-0 truncate text-[12.5px] text-text-3">{me.user.email}</p>
      </div>
      <MenuSeparator />
      <MenuLabel>{t('locale.label')}</MenuLabel>
      {supportedLocales.map((value) => (
        <MenuItem
          key={value}
          onSelect={() => {
            setLocale(value);
          }}
        >
          <span className="flex-1">{t(`locale.${value}`)}</span>
          {value === locale ? <Check aria-hidden className="size-4 text-accent" /> : null}
        </MenuItem>
      ))}
      <MenuSeparator />
      <MenuItem onSelect={onSignOut}>
        <LogOut aria-hidden className="size-4" />
        {t('signOut.action')}
      </MenuItem>
    </Menu>
  );
}
