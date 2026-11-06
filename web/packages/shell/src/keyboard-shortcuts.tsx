// One-character shortcuts and the list of them. A person can turn them off, which WCAG 2.2
// criterion 2.1.4 requires, because a stray key or a speech command would otherwise act
// (docs/research/keyboard-shortcuts.md).
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import { Button, Dialog, Kbd, Switch, usePreferences, useToast } from '@ownsolid/ui';
import { useRouter } from '@tanstack/react-router';
import { useEffect, useEffectEvent, type ReactNode } from 'react';
import { moveThroughRows } from './row-keys.ts';

/** A letter that runs an action. One without `to` has no page yet and says so. */
export interface SingleKeyShortcut {
  /** The lower-case letter. */
  key: string;
  label: TranslationKey;
  /** Where the action happens. */
  to?: string | undefined;
  /** The action on that page, as in `/nodes#add`. */
  hash?: string | undefined;
}

const editable = 'input, textarea, select, [contenteditable]:not([contenteditable="false"])';
// Letters mean something else inside these, and a layer must not move the page behind it.
const layers = 'dialog[open], [role="menu"], [role="listbox"]';

/** Whether a key press belongs to what has focus, or to an open layer. */
function taken(event: KeyboardEvent): boolean {
  const target = event.target instanceof Element ? event.target : null;
  return (
    event.metaKey ||
    event.ctrlKey ||
    event.altKey ||
    event.isComposing ||
    event.repeat ||
    target?.closest(editable) != null ||
    document.querySelector(layers) !== null
  );
}

/** Runs one-character shortcuts while the account has them on. */
export function useSingleKeyShortcuts(
  shortcuts: readonly SingleKeyShortcut[],
  onHelp: () => void,
): void {
  const { t } = useI18n();
  const { singleKeyShortcuts } = usePreferences();
  const router = useRouter();
  const toast = useToast();
  const press = useEffectEvent((event: KeyboardEvent) => {
    if (taken(event)) return;
    if (event.key === '?') {
      event.preventDefault();
      onHelp();
      return;
    }
    if (event.key === '/') {
      const search = document.querySelector<HTMLInputElement>('main input[type="search"]');
      if (!search) return;
      event.preventDefault();
      search.focus();
      return;
    }
    // Lower-case `k` moves on a page with a list; Shift+K keeps the action named `K` in reach there.
    if ((event.key === 'j' || event.key === 'k') && moveThroughRows(event.key === 'j' ? 1 : -1)) {
      event.preventDefault();
      return;
    }
    const shortcut = shortcuts.find((item) => item.key === event.key.toLowerCase());
    if (!shortcut) return;
    event.preventDefault();
    if (shortcut.to) {
      void router.navigate({
        to: shortcut.to,
        ...(shortcut.hash ? { hash: shortcut.hash } : {}),
      });
    } else {
      toast({ message: t('shortcuts.notReady', { name: t(shortcut.label) }) });
    }
  });
  useEffect(() => {
    if (!singleKeyShortcuts) return undefined;
    const listener = (event: KeyboardEvent) => {
      press(event);
    };
    document.addEventListener('keydown', listener);
    return () => {
      document.removeEventListener('keydown', listener);
    };
  }, [singleKeyShortcuts]);
}

function Row({ keys, children, muted }: { keys: ReactNode; children: ReactNode; muted?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4 py-1.5">
      <dt className={muted ? 'text-text-3' : undefined}>{children}</dt>
      <dd className="m-0 flex-none">{keys}</dd>
    </div>
  );
}

/** Props for {@link ShortcutsDialog}. */
export interface ShortcutsDialogProps {
  shortcuts: readonly SingleKeyShortcut[];
  onClose: () => void;
}

/** Every shortcut, what is not available yet, and the switch for the one-character ones. */
export function ShortcutsDialog({ shortcuts, onClose }: ShortcutsDialogProps) {
  const { t } = useI18n();
  const { singleKeyShortcuts, setSingleKeyShortcuts } = usePreferences();
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('shortcuts.title')}
      footer={<Button onClick={onClose}>{t('action.close')}</Button>}
    >
      <dl className="m-0 flex flex-col">
        <Row keys={<Kbd>{t('shortcuts.commandKeys')}</Kbd>}>{t('shortcuts.command')}</Row>
        <Row keys={<Kbd>/</Kbd>}>{t('shortcuts.search')}</Row>
        <Row keys={<Kbd>?</Kbd>}>{t('shortcuts.help')}</Row>
        {shortcuts.map((item) => (
          <Row key={item.key} keys={<Kbd>{item.key.toUpperCase()}</Kbd>} muted={!item.to}>
            {t(item.label)}
            {item.to ? null : ` (${t('shortcuts.notYet')})`}
          </Row>
        ))}
        {shortcuts.length > 0 ? (
          <Row
            keys={
              <span className="flex gap-1">
                <Kbd>{'J'}</Kbd>
                <Kbd>{'K'}</Kbd>
              </span>
            }
          >
            {t('shortcuts.rows')}
          </Row>
        ) : null}
      </dl>
      <div className="mt-4 flex items-start justify-between gap-4 border-t border-border pt-4">
        <div>
          <p className="m-0 font-semibold">{t('shortcuts.switch')}</p>
          <p className="m-0 mt-0.5 text-[13px] text-text-2">{t('shortcuts.switch.detail')}</p>
        </div>
        <Switch
          label={t('shortcuts.switch')}
          checked={singleKeyShortcuts}
          onChange={setSingleKeyShortcuts}
        />
      </div>
    </Dialog>
  );
}
