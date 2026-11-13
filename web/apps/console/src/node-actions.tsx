// What can be done to a node, from its drawer or its own page. Renaming and disabling happen at once and
// offer undo; removing clears the node's credential for good, so its name is typed first.
import { collectNodeFacts, isProblem, updateNode, type Node } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Dialog,
  Menu,
  MenuItem,
  MenuSeparator,
  ProblemNotice,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Ban, Check, Ellipsis, Pencil, RefreshCw, Trash2 } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode, type SubmitEvent } from 'react';

function RenameDialog({
  node,
  onClose,
  onRename,
  pending,
  problem,
}: {
  node: Node;
  onClose: () => void;
  onRename: (name: string) => void;
  pending: boolean;
  problem: unknown;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(node.name);
  const usable = name.trim() !== '' && name.trim() !== node.name;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (usable && !pending) onRename(name.trim());
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('nodes.rename.title', { name: node.name })}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form="rename-node" variant="primary" disabled={!usable || pending}>
            {t('nodes.rename.submit')}
          </Button>
        </>
      }
    >
      <form id="rename-node" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
        {isProblem(problem) ? <ProblemNotice problem={problem} /> : null}
        <TextField
          label={t('nodes.add.name')}
          name="node-name"
          autoComplete="off"
          maxLength={100}
          required
          value={name}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function RemoveDialog({
  node,
  onClose,
  onRemove,
  pending,
  problem,
}: {
  node: Node;
  onClose: () => void;
  onRemove: (reason: string) => void;
  pending: boolean;
  problem: unknown;
}) {
  const { t } = useI18n();
  const [reason, setReason] = useState(t('nodes.remove.reason.default'));
  return (
    <TypedConfirmation
      title={t('nodes.remove.title')}
      consequence={t('nodes.remove.consequence', { name: node.name })}
      expected={node.name}
      confirmLabel={t('nodes.remove.submit')}
      pending={pending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') onRemove(reason.trim());
      }}
    >
      <TextField
        label={t('nodes.remove.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        hint={t('nodes.remove.reason.hint')}
        value={reason}
        error={reason.trim() === '' ? t('nodes.remove.reason.required') : undefined}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {isProblem(problem) ? <ProblemNotice problem={problem} /> : null}
    </TypedConfirmation>
  );
}

/** The drawer for one node. It shows nothing while `node` is null and no `problem` is given. */
/** The parts of a node's actions, for its surface to place. */
export interface NodeActions {
  /** The buttons; nothing once a node is removed. */
  buttons: ReactNode;
  /** What went wrong, to show above the details. */
  notices: ReactNode;
  /** To render beside the surface. */
  dialogs: ReactNode;
  /** A collection of facts was started. */
  collected: boolean;
}

/** The actions on a node. `onChanged` runs before the toast that offers undo. */
export function useNodeActions(
  node: Node | null,
  {
    onChanged,
    onRemoved,
    detail = false,
    primary = true,
  }: {
    onChanged: () => void;
    onRemoved: () => void;
    /** A detail header keeps one frequent action and the secondary command menu. */
    detail?: boolean;
    /** Activity owns the primary action while its tab is selected. */
    primary?: boolean;
  },
): NodeActions {
  const { t } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<'rename' | 'remove' | null>(null);
  const menuTrigger = useRef<HTMLButtonElement>(null);
  const restoreMenuFocus = useRef(false);
  const closeDialog = () => {
    restoreMenuFocus.current = detail;
    setDialog(null);
  };
  useEffect(() => {
    if (dialog === null && restoreMenuFocus.current) {
      restoreMenuFocus.current = false;
      menuTrigger.current?.focus();
    }
  }, [dialog]);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['nodes'] });
  const change = useMutation({
    mutationFn: (input: { target: Node; name?: string; status?: Node['status']; reason: string }) =>
      updateNode(input.target.id, {
        name: input.name ?? input.target.name,
        status: input.status ?? input.target.status,
        version: input.target.version,
        reason: input.reason,
      }),
    onSettled: refresh,
  });
  // One key per request, so a retry after a lost answer cannot start a second task.
  const collectKey = useRef<string | null>(null);
  const collect = useMutation({
    mutationFn: (id: string) => {
      collectKey.current ??= crypto.randomUUID();
      return collectNodeFacts(id, collectKey.current);
    },
    onSuccess: () => {
      collectKey.current = null;
      return refresh();
    },
  });

  /** Changes the node and offers to put back what it was. */
  const reversible = (
    target: Node,
    next: { name?: string; status?: Node['status'] },
    reason: string,
    done: string,
  ) => {
    change.mutate(
      { target, ...next, reason },
      {
        onSuccess: (updated) => {
          // The toast sits behind a modal, so its undo is out of reach until the drawer closes.
          closeDialog();
          onChanged();
          toast({
            message: done,
            undo: () =>
              updateNode(updated.id, {
                name: target.name,
                status: target.status,
                version: updated.version,
                reason: t('nodes.reason.undo'),
              }).finally(refresh),
          });
        },
      },
    );
  };

  const active = node?.status === 'active';
  const removed = node?.status === 'removed';
  const openDialog = (next: 'rename' | 'remove') => {
    change.reset();
    setDialog(next);
  };
  const toggle = () => {
    if (!node) return;
    reversible(
      node,
      { status: active ? 'disabled' : 'active' },
      t(active ? 'nodes.reason.disable' : 'nodes.reason.enable'),
      t(active ? 'nodes.disable.done' : 'nodes.enable.done', { name: node.name }),
    );
  };
  return {
    buttons:
      node && !removed ? (
        detail ? (
          <>
            <Button
              variant={primary ? 'primary' : 'secondary'}
              disabled={active ? collect.isPending : change.isPending}
              onClick={() => {
                if (active) collect.mutate(node.id);
                else toggle();
              }}
            >
              {active ? <RefreshCw aria-hidden /> : <Check aria-hidden />}
              {t(active ? 'nodes.collect.open' : 'nodes.enable.open')}
            </Button>
            <Menu
              trigger={
                <Button ref={menuTrigger}>
                  <Ellipsis aria-hidden />
                  {t('resource.actions')}
                </Button>
              }
            >
              <MenuItem
                onSelect={() => {
                  openDialog('rename');
                }}
              >
                <Pencil aria-hidden />
                {t('nodes.rename.open')}
              </MenuItem>
              {active ? (
                <MenuItem disabled={change.isPending} onSelect={toggle}>
                  <Ban aria-hidden />
                  {t('nodes.disable.open')}
                </MenuItem>
              ) : null}
              <MenuSeparator />
              <MenuItem
                className="text-critical-ink data-[highlighted]:text-critical-ink"
                onSelect={() => {
                  openDialog('remove');
                }}
              >
                <Trash2 aria-hidden />
                {t('nodes.remove.open')}
              </MenuItem>
            </Menu>
          </>
        ) : (
          <>
            <Button
              variant="danger"
              onClick={() => {
                openDialog('remove');
              }}
            >
              <Trash2 aria-hidden />
              {t('nodes.remove.open')}
            </Button>
            <Button
              onClick={() => {
                openDialog('rename');
              }}
            >
              <Pencil aria-hidden />
              {t('nodes.rename.open')}
            </Button>
            <Button disabled={change.isPending} onClick={toggle}>
              {active ? <Ban aria-hidden /> : <Check aria-hidden />}
              {active ? t('nodes.disable.open') : t('nodes.enable.open')}
            </Button>
            {active ? (
              <Button
                variant="primary"
                disabled={collect.isPending}
                onClick={() => {
                  collect.mutate(node.id);
                }}
              >
                <RefreshCw aria-hidden />
                {t('nodes.collect.open')}
              </Button>
            ) : null}
          </>
        )
      ) : null,
    notices: (
      <>
        {dialog === null && change.error && isProblem(change.error) ? (
          <ProblemNotice className="mb-4" problem={change.error} />
        ) : null}
        {collect.error && isProblem(collect.error) ? (
          <ProblemNotice
            className="mb-4"
            problem={collect.error}
            onRetry={() => {
              if (node) collect.mutate(node.id);
            }}
          />
        ) : null}
      </>
    ),
    dialogs: (
      <>
        {/* Beside the surface, not in it: a modal inside a modal can lose clicks to its parent. */}
        {node && dialog === 'rename' ? (
          <RenameDialog
            node={node}
            pending={change.isPending}
            problem={change.error}
            onClose={() => {
              closeDialog();
            }}
            onRename={(name) => {
              reversible(
                node,
                { name },
                t('nodes.reason.rename'),
                t('nodes.rename.done', { name }),
              );
            }}
          />
        ) : null}
        {node && dialog === 'remove' ? (
          <RemoveDialog
            node={node}
            pending={change.isPending}
            problem={change.error}
            onClose={() => {
              closeDialog();
            }}
            onRemove={(reason) => {
              change.mutate(
                { target: node, status: 'removed', reason },
                {
                  onSuccess: () => {
                    closeDialog();
                    onRemoved();
                    toast({ message: t('nodes.remove.done', { name: node.name }) });
                  },
                },
              );
            }}
          />
        ) : null}
      </>
    ),
    collected: collect.isSuccess,
  };
}
