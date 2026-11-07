// The list toolbar's views: open one, save the screen as one, and change what is there.
import { isProblem, type SavedView } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Dialog,
  Menu,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  ProblemNotice,
  Spinner,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation } from '@tanstack/react-query';
import { Bookmark, Check, ChevronDown } from 'lucide-react';
import { useEffect, useId, useRef, useState, type SubmitEvent } from 'react';
import type { ListView } from './use-list-view.ts';

interface NameDialogProps {
  title: string;
  detail?: string | undefined;
  initial: string;
  onClose: () => void;
  onSave: (name: string) => Promise<unknown>;
}

function NameDialog({ title, detail, initial, onClose, onSave }: NameDialogProps) {
  const { t } = useI18n();
  const formId = useId();
  const field = useRef<HTMLInputElement>(null);
  const [name, setName] = useState(initial);
  const [missing, setMissing] = useState(false);
  const trimmed = name.trim();
  const save = useMutation({
    mutationFn: () => onSave(trimmed),
    onSuccess: onClose,
  });
  // The dialog opened in a child's effect and focused its close button; start at the name.
  useEffect(() => {
    field.current?.focus();
    field.current?.select();
  }, []);
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMissing(trimmed === '');
    if (trimmed !== '') save.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      description={detail}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? (
              <Spinner label={t('list.views.submit')} className="text-white" />
            ) : null}
            {t('list.views.submit')}
          </Button>
        </>
      }
    >
      <form id={formId} noValidate onSubmit={submit} className="flex flex-col gap-4 py-2">
        {save.error && isProblem(save.error) ? <ProblemNotice problem={save.error} /> : null}
        <TextField
          ref={field}
          label={t('list.views.name')}
          name="view-name"
          autoComplete="off"
          maxLength={100}
          required
          value={name}
          error={missing ? t('list.views.name.required') : undefined}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

type OpenDialog = { kind: 'saveAs'; id: string } | { kind: 'rename'; view: SavedView } | null;

/** Opens and manages the saved views of a list. */
export function SavedViewsMenu({ list }: { list: ListView }) {
  const { t } = useI18n();
  const toast = useToast();
  const [dialog, setDialog] = useState<OpenDialog>(null);
  const close = () => {
    setDialog(null);
  };
  const { activeView, changed } = list;
  const failed = (retry: () => void) => {
    toast({ message: t('list.views.failed'), retry });
  };
  const saveChanges = () => {
    const previous = activeView;
    list.saveChanges().then(
      (saved) => {
        if (!saved || !previous) return;
        toast({
          message: t('list.views.saved', { name: saved.name }),
          undo: async () => {
            list.openView(await list.restore(saved, previous));
          },
        });
      },
      () => {
        failed(saveChanges);
      },
    );
  };
  const remove = (view: SavedView) => {
    list.remove(view).then(
      (restore) => {
        toast({ message: t('list.views.deleted', { name: view.name }), undo: restore });
      },
      () => {
        failed(() => {
          remove(view);
        });
      },
    );
  };
  return (
    <>
      <Menu
        align="end"
        className="max-h-[70vh] overflow-y-auto"
        trigger={
          <Button
            aria-label={
              changed
                ? t('list.views.currentChanged', { name: activeView?.name ?? '' })
                : t('list.views.current', { name: activeView?.name ?? t('list.views.all') })
            }
          >
            <Bookmark aria-hidden />
            <span className="max-w-[160px] truncate">
              {activeView?.name ?? t('list.views.all')}
            </span>
            {changed ? (
              <span
                aria-hidden
                title={t('list.views.changed')}
                className="size-2 rounded-full bg-accent"
              />
            ) : null}
            <ChevronDown aria-hidden />
          </Button>
        }
      >
        <MenuLabel>{t('list.views')}</MenuLabel>
        <MenuItem
          onSelect={() => {
            list.openView(null);
          }}
        >
          <span className="flex-1">{t('list.views.all')}</span>
          {activeView ? null : <Check aria-hidden className="size-4 text-accent" />}
        </MenuItem>
        {list.views.map((view) => (
          <MenuItem
            key={view.id}
            onSelect={() => {
              list.openView(view);
            }}
          >
            <span className="flex-1 truncate">{view.name}</span>
            {view.id === activeView?.id ? (
              <Check aria-hidden className="size-4 text-accent" />
            ) : null}
          </MenuItem>
        ))}
        <MenuSeparator />
        {list.viewsAvailable ? (
          <>
            {changed ? (
              <>
                <MenuItem onSelect={saveChanges}>{t('list.views.save')}</MenuItem>
                <MenuItem onSelect={list.revert}>{t('list.views.revert')}</MenuItem>
              </>
            ) : null}
            <MenuItem
              onSelect={() => {
                setDialog({ kind: 'saveAs', id: crypto.randomUUID() });
              }}
            >
              {t('list.views.saveAs')}
            </MenuItem>
            {activeView ? (
              <>
                <MenuItem
                  onSelect={() => {
                    setDialog({ kind: 'rename', view: activeView });
                  }}
                >
                  {t('list.views.rename')}
                </MenuItem>
                <MenuItem
                  onSelect={() => {
                    remove(activeView);
                  }}
                >
                  {t('list.views.delete')}
                </MenuItem>
              </>
            ) : null}
          </>
        ) : (
          <p className="m-0 max-w-[260px] px-2.5 py-1.5 text-[12.5px] text-text-3">
            {t('list.views.unavailable')}
          </p>
        )}
      </Menu>
      {dialog?.kind === 'saveAs' ? (
        <NameDialog
          title={t('list.views.saveAs.title')}
          detail={t('list.views.saveAs.detail')}
          initial=""
          onClose={close}
          onSave={async (name) => {
            await list.saveAs(dialog.id, name);
            toast({ message: t('list.views.saved', { name }) });
          }}
        />
      ) : null}
      {dialog?.kind === 'rename' ? (
        <NameDialog
          title={t('list.views.rename.title')}
          initial={dialog.view.name}
          onClose={close}
          onSave={async (name) => {
            const renamed = await list.rename(dialog.view, name);
            toast({
              message: t('list.views.saved', { name }),
              undo: () => list.restore(renamed, dialog.view),
            });
          }}
        />
      ) : null}
    </>
  );
}
