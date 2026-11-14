// One workspace: its name and state, who belongs to it and what each may do. Every change states a
// reason for the audit trail and can be undone (docs/research/workspaces-page.md).
import {
  addMember,
  changeMember,
  getWorkspace,
  isProblem,
  listMembers,
  removeMember,
  updateWorkspace,
  type MemberRole,
  type Membership,
  type ManagedWorkspace as Workspace,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  CopyText,
  Dialog,
  EmptyState,
  ProblemNotice,
  SelectField,
  Skeleton,
  Spinner,
  StatusPill,
  Table,
  Tag,
  Td,
  TextField,
  Th,
  Tr,
  useToast,
} from '@ownsolid/ui';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, getRouteApi } from '@tanstack/react-router';
import { ArrowLeft, UserPlus, Users } from 'lucide-react';
import { useMemo, useState, type ReactNode, type SubmitEvent } from 'react';
import { workspacesKey } from './workspaces-page.tsx';

const route = getRouteApi('/authenticated/frame/users/workspaces/$workspaceId');

const roles: readonly MemberRole[] = ['owner', 'member', 'viewer'];

const reasonLimit = 500;

/** Follows a change everywhere it shows, and catches up after a refusal from a stale page. */
function useRefresh() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: workspacesKey });
}

function useRoleOptions() {
  const { t } = useI18n();
  return roles.map((value) => ({ value, label: t(`role.${value}`) }));
}

/** A dialog that asks for a reason and whatever else the change needs. */
function ChangeDialog({
  title,
  detail,
  submit,
  danger = false,
  pending,
  error,
  ready = true,
  onRetry,
  onSubmit,
  onClose,
  reason,
  setReason,
  children,
}: {
  title: string;
  detail?: string | undefined;
  submit: string;
  danger?: boolean | undefined;
  pending: boolean;
  error: unknown;
  ready?: boolean | undefined;
  onRetry: () => void;
  onSubmit: () => void;
  onClose: () => void;
  reason: string;
  setReason: (value: string) => void;
  children?: ReactNode | undefined;
}) {
  const { t } = useI18n();
  const able = ready && reason.trim() !== '' && !pending;
  const send = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (able) onSubmit();
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
          <Button
            type="submit"
            form="workspace-change"
            variant={danger ? 'danger' : 'primary'}
            disabled={!able}
          >
            {pending ? <Spinner label={submit} className="text-white" /> : null}
            {submit}
          </Button>
        </>
      }
    >
      <form id="workspace-change" noValidate onSubmit={send} className="flex flex-col gap-4">
        {isProblem(error) ? <ProblemNotice problem={error} onRetry={onRetry} /> : null}
        {children}
        <TextField
          label={t('workspace.reason')}
          name="reason"
          autoComplete="off"
          maxLength={reasonLimit}
          required
          hint={t('workspace.reason.hint')}
          value={reason}
          onChange={(event) => {
            setReason(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function RenameDialog({ workspace, onClose }: { workspace: Workspace; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh();
  const [name, setName] = useState(workspace.name);
  const [reason, setReason] = useState('');
  const save = useMutation({
    mutationFn: () =>
      updateWorkspace(workspace.id, {
        name: name.trim(),
        status: workspace.status,
        version: workspace.version,
        reason: reason.trim(),
      }),
    onSuccess: (saved) => {
      void refresh();
      onClose();
      toast({
        message: t('workspace.rename.done', { name: saved.name }),
        undo: async () => {
          await updateWorkspace(saved.id, {
            name: workspace.name,
            status: saved.status,
            version: saved.version,
            reason: t('workspace.reason.undo'),
          });
          await refresh();
        },
      });
    },
    onError: () => void refresh(),
  });
  const trimmed = name.trim();
  return (
    <ChangeDialog
      title={t('workspace.rename.title')}
      submit={t('workspace.rename.submit')}
      pending={save.isPending}
      error={save.error}
      ready={trimmed !== '' && trimmed !== workspace.name}
      onRetry={() => {
        save.mutate();
      }}
      onSubmit={() => {
        save.mutate();
      }}
      onClose={onClose}
      reason={reason}
      setReason={setReason}
    >
      <TextField
        label={t('workspaces.column.name')}
        name="name"
        autoComplete="off"
        maxLength={100}
        required
        value={name}
        onChange={(event) => {
          setName(event.target.value);
        }}
      />
    </ChangeDialog>
  );
}

function StatusDialog({ workspace, onClose }: { workspace: Workspace; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh();
  const [reason, setReason] = useState('');
  const next = workspace.status === 'active' ? 'disabled' : 'active';
  const verb = next === 'disabled' ? 'disable' : 'enable';
  const save = useMutation({
    mutationFn: () =>
      updateWorkspace(workspace.id, {
        name: workspace.name,
        status: next,
        version: workspace.version,
        reason: reason.trim(),
      }),
    onSuccess: (saved) => {
      void refresh();
      onClose();
      toast({
        message: t(`workspace.${verb}.done`, { name: saved.name }),
        undo: async () => {
          await updateWorkspace(saved.id, {
            name: saved.name,
            status: workspace.status,
            version: saved.version,
            reason: t('workspace.reason.undo'),
          });
          await refresh();
        },
      });
    },
    onError: () => void refresh(),
  });
  return (
    <ChangeDialog
      title={t(`workspace.${verb}.title`, { name: workspace.name })}
      detail={t(`workspace.${verb}.detail`)}
      submit={t(`workspace.${verb}.submit`)}
      danger={next === 'disabled'}
      pending={save.isPending}
      error={save.error}
      onRetry={() => {
        save.mutate();
      }}
      onSubmit={() => {
        save.mutate();
      }}
      onClose={onClose}
      reason={reason}
      setReason={setReason}
    />
  );
}

function AddDialog({ workspace, onClose }: { workspace: Workspace; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh();
  const options = useRoleOptions();
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<MemberRole>('member');
  const add = useMutation({
    mutationFn: () => addMember(workspace.id, { email: email.trim(), role }),
    onSuccess: (member) => {
      void refresh();
      onClose();
      toast({
        message: t('workspace.add.done', { email: member.email }),
        undo: async () => {
          await removeMember(workspace.id, member.user_id, {
            version: member.version,
            reason: t('workspace.reason.undo'),
          });
          await refresh();
        },
      });
    },
  });
  const ready = email.trim() !== '' && !add.isPending;
  const missing = isProblem(add.error, 'RESOURCE_NOT_FOUND');
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready) add.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('workspace.add.title')}
      description={t('workspace.add.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form="workspace-add" variant="primary" disabled={!ready}>
            {add.isPending ? (
              <Spinner label={t('workspace.add.submit')} className="text-white" />
            ) : null}
            {t('workspace.add.submit')}
          </Button>
        </>
      }
    >
      <form id="workspace-add" noValidate onSubmit={submit} className="flex flex-col gap-4">
        {isProblem(add.error) && !missing ? (
          <ProblemNotice
            problem={add.error}
            onRetry={() => {
              add.mutate();
            }}
          />
        ) : null}
        <TextField
          label={t('workspace.member.email')}
          name="email"
          type="email"
          autoComplete="off"
          required
          error={missing ? t('workspaces.noAccount') : undefined}
          value={email}
          onChange={(event) => {
            setEmail(event.target.value);
          }}
        />
        <SelectField
          label={t('workspace.member.role')}
          name="role"
          options={options}
          value={role}
          onChange={(event) => {
            setRole(event.target.value as MemberRole);
          }}
        />
      </form>
    </Dialog>
  );
}

function RoleDialog({
  workspace,
  member,
  onClose,
}: {
  workspace: Workspace;
  member: Membership;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh();
  const options = useRoleOptions();
  const [role, setRole] = useState<MemberRole>(member.role);
  const [reason, setReason] = useState('');
  const save = useMutation({
    mutationFn: () =>
      changeMember(workspace.id, member.user_id, {
        role,
        version: member.version,
        reason: reason.trim(),
      }),
    onSuccess: (saved) => {
      void refresh();
      onClose();
      toast({
        message: t('workspace.role.done', { email: saved.email, role: t(`role.${saved.role}`) }),
        undo: async () => {
          await changeMember(workspace.id, saved.user_id, {
            role: member.role,
            version: saved.version,
            reason: t('workspace.reason.undo'),
          });
          await refresh();
        },
      });
    },
    onError: () => void refresh(),
  });
  return (
    <ChangeDialog
      title={t('workspace.role.title', { email: member.email })}
      submit={t('workspace.role.submit')}
      pending={save.isPending}
      error={save.error}
      ready={role !== member.role}
      onRetry={() => {
        save.mutate();
      }}
      onSubmit={() => {
        save.mutate();
      }}
      onClose={onClose}
      reason={reason}
      setReason={setReason}
    >
      <SelectField
        label={t('workspace.member.role')}
        name="role"
        options={options}
        value={role}
        onChange={(event) => {
          setRole(event.target.value as MemberRole);
        }}
      />
    </ChangeDialog>
  );
}

function RemoveDialog({
  workspace,
  member,
  onClose,
}: {
  workspace: Workspace;
  member: Membership;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const refresh = useRefresh();
  const [reason, setReason] = useState('');
  const remove = useMutation({
    mutationFn: () =>
      removeMember(workspace.id, member.user_id, {
        version: member.version,
        reason: reason.trim(),
      }),
    onSuccess: () => {
      void refresh();
      onClose();
      toast({
        message: t('workspace.remove.done', { email: member.email }),
        undo: async () => {
          await addMember(workspace.id, { email: member.email, role: member.role });
          await refresh();
        },
      });
    },
    onError: () => void refresh(),
  });
  return (
    <ChangeDialog
      title={t('workspace.remove.title', { email: member.email })}
      detail={t('workspace.remove.detail', { name: workspace.name })}
      submit={t('workspace.remove.submit')}
      danger
      pending={remove.isPending}
      error={remove.error}
      onRetry={() => {
        remove.mutate();
      }}
      onSubmit={() => {
        remove.mutate();
      }}
      onClose={onClose}
      reason={reason}
      setReason={setReason}
    />
  );
}

interface MemberAction {
  kind: 'role' | 'remove';
  member: Membership;
}

function Members({ workspace }: { workspace: Workspace }) {
  const { t, format } = useI18n();
  const limit = useQuery(runtimeConfigQuery()).data?.ui.pageSize ?? 20;
  const [adding, setAdding] = useState(false);
  const [action, setAction] = useState<MemberAction | null>(null);
  const members = useInfiniteQuery({
    queryKey: [...workspacesKey, workspace.id, 'members', limit],
    queryFn: ({ pageParam, signal }) =>
      listMembers(workspace.id, { limit, cursor: pageParam ?? undefined }, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = useMemo(
    () => members.data?.pages.flatMap((page) => page.items) ?? [],
    [members.data],
  );
  const close = () => {
    setAction(null);
  };

  let body;
  if (members.isPending) {
    body = <Skeleton aria-busy className="m-5 h-[88px]" />;
  } else if (members.error && items.length === 0) {
    body = isProblem(members.error) ? (
      <CardBody>
        <ProblemNotice
          problem={members.error}
          onRetry={() => {
            void members.refetch();
          }}
        />
      </CardBody>
    ) : null;
  } else if (items.length === 0) {
    body = (
      <EmptyState icon={Users} title={t('workspace.members.empty')}>
        {t('workspace.members.empty.detail')}
      </EmptyState>
    );
  } else {
    body = (
      <Table>
        <thead>
          <tr>
            <Th>{t('workspace.member.person')}</Th>
            <Th>{t('workspace.member.role')}</Th>
            <Th>{t('workspace.member.since')}</Th>
            <Th>
              <span className="sr-only">{t('workspace.member.actions')}</span>
            </Th>
          </tr>
        </thead>
        <tbody>
          {items.map((member) => (
            <Tr key={member.user_id}>
              <Td className="font-medium">
                {member.display_name || member.email}
                <span className="block text-[12.5px] font-normal text-text-3">{member.email}</span>
              </Td>
              <Td>
                {t(`role.${member.role}`)}
                {member.user_status === 'disabled' ? (
                  <span className="ml-2">
                    <StatusPill tone="neutral">{t('workspace.member.disabled')}</StatusPill>
                  </span>
                ) : null}
              </Td>
              <Td className="text-text-2">{format.date(member.created_at)}</Td>
              <Td className="text-right whitespace-nowrap">
                <Button
                  size="sm"
                  aria-label={t('workspace.role.open', { email: member.email })}
                  onClick={() => {
                    setAction({ kind: 'role', member });
                  }}
                >
                  {t('workspace.role.short')}
                </Button>{' '}
                <Button
                  size="sm"
                  aria-label={t('workspace.remove.open', { email: member.email })}
                  onClick={() => {
                    setAction({ kind: 'remove', member });
                  }}
                >
                  {t('workspace.remove.short')}
                </Button>
              </Td>
            </Tr>
          ))}
        </tbody>
      </Table>
    );
  }

  return (
    <Card className="overflow-hidden">
      <CardHeader
        title={t('workspace.members')}
        actions={
          <Button
            size="sm"
            onClick={() => {
              setAdding(true);
            }}
          >
            <UserPlus aria-hidden />
            {t('workspace.add.open')}
          </Button>
        }
      />
      <div className="mt-3">{body}</div>
      {members.hasNextPage ? (
        <div className="p-4">
          <Button
            disabled={members.isFetchingNextPage}
            onClick={() => {
              void members.fetchNextPage();
            }}
          >
            {t('workspaces.loadMore')}
          </Button>
        </div>
      ) : null}
      {adding ? (
        <AddDialog
          workspace={workspace}
          onClose={() => {
            setAdding(false);
          }}
        />
      ) : null}
      {action?.kind === 'role' ? (
        <RoleDialog workspace={workspace} member={action.member} onClose={close} />
      ) : null}
      {action?.kind === 'remove' ? (
        <RemoveDialog workspace={workspace} member={action.member} onClose={close} />
      ) : null}
    </Card>
  );
}

/** `/users/workspaces/$workspaceId`: one workspace and its members. */
export function WorkspacePage() {
  const { t } = useI18n();
  const { workspaceId } = route.useParams();
  const [renaming, setRenaming] = useState(false);
  const [changing, setChanging] = useState(false);
  const workspace = useQuery({
    queryKey: [...workspacesKey, workspaceId, 'detail'],
    queryFn: ({ signal }) => getWorkspace(workspaceId, { signal }),
  });
  const back = (
    <Link
      to="/users/workspaces"
      className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
    >
      <ArrowLeft aria-hidden className="size-4" />
      {t('workspace.back')}
    </Link>
  );
  if (workspace.isPending) {
    return (
      <>
        {back}
        <Skeleton aria-busy className="h-[200px] w-full" />
      </>
    );
  }
  if (!workspace.data) {
    return (
      <>
        {back}
        {isProblem(workspace.error) ? (
          <ProblemNotice
            titleAs="h1"
            problem={workspace.error}
            onRetry={() => {
              void workspace.refetch();
            }}
          />
        ) : null}
      </>
    );
  }
  const current = workspace.data;
  const active = current.status === 'active';
  return (
    <>
      {back}
      <div className="mb-6 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="m-0 mb-1.5 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em] break-words">
            {current.name}
          </h1>
          <div className="flex flex-wrap items-center gap-2.5">
            <StatusPill tone={active ? 'good' : 'neutral'}>
              {t(`workspaces.status.${current.status}`)}
            </StatusPill>
            {current.is_default ? <Tag>{t('workspaces.default')}</Tag> : null}
            <CopyText text={current.id} />
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            onClick={() => {
              setRenaming(true);
            }}
          >
            {t('workspace.rename.open')}
          </Button>
          {current.is_default ? null : (
            <Button
              onClick={() => {
                setChanging(true);
              }}
            >
              {t(active ? 'workspace.disable.open' : 'workspace.enable.open')}
            </Button>
          )}
        </div>
      </div>
      {current.is_default ? (
        <p className="m-0 mb-4 text-[13px] text-text-2">{t('workspace.default.note')}</p>
      ) : null}
      <Members key={current.id} workspace={current} />
      {renaming ? (
        <RenameDialog
          workspace={current}
          onClose={() => {
            setRenaming(false);
          }}
        />
      ) : null}
      {changing ? (
        <StatusDialog
          workspace={current}
          onClose={() => {
            setChanging(false);
          }}
        />
      ) : null}
    </>
  );
}
