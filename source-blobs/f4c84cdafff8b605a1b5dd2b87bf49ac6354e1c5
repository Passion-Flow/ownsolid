import { useI18n } from '@ownsolid/i18n';
import { Menu, MenuItem, MenuLabel, Spinner, useToast } from '@ownsolid/ui';
import { Check, ChevronDown, RefreshCw } from 'lucide-react';
import { useWorkspaces } from './workspace.ts';

/** Shows the current workspace and switches to another. */
export function WorkspaceSwitcher() {
  const { t } = useI18n();
  const toast = useToast();
  const { active, items, truncated, isPending, error, retry, select } = useWorkspaces();
  return (
    <Menu
      trigger={
        <button
          type="button"
          aria-label={t('workspace.switch', { name: active?.name ?? t('workspace.none') })}
          className="flex h-9 max-w-[200px] cursor-pointer items-center gap-2 rounded-field border border-border bg-surface px-2.5 font-[550]"
        >
          <span
            aria-hidden
            className="size-[18px] shrink-0 rounded-[5px] bg-linear-135 from-[#2bb3a3] to-[#0b6e64]"
          />
          <span className="truncate">{active?.name ?? t('workspace.none')}</span>
          <ChevronDown aria-hidden className="size-3.5 text-text-3" />
        </button>
      }
    >
      <MenuLabel>{t('workspace.heading')}</MenuLabel>
      {isPending ? <Spinner label={t('status.loading')} className="px-2.5 py-1.5" /> : null}
      {error ? (
        <MenuItem
          onSelect={(event) => {
            event.preventDefault();
            retry();
          }}
        >
          <RefreshCw aria-hidden className="size-4" />
          {t('workspace.loadFailed')}
        </MenuItem>
      ) : null}
      {items.map((item) => (
        <MenuItem
          key={item.id}
          onSelect={() => {
            if (item.id === active?.id) return;
            select(item);
            toast({ message: t('workspace.switched', { name: item.name }) });
          }}
        >
          <span className="min-w-0 flex-1 truncate">{item.name}</span>
          {item.id === active?.id ? <Check aria-hidden className="size-4 text-accent" /> : null}
        </MenuItem>
      ))}
      {truncated ? (
        <p className="m-0 px-2.5 py-1 text-[12px] text-text-3">
          {t('workspace.truncated', { count: items.length })}
        </p>
      ) : null}
    </Menu>
  );
}
