// Every workspace and how to add one. Members and changes live on each workspace's own page
// (docs/research/workspaces-page.md).
import {
  createWorkspace,
  isProblem,
  listAllWorkspaces,
  listUsers,
  type ManagedWorkspace as Workspace,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  ColumnChooser,
  CopyLinkButton,
  runtimeConfigQuery,
  SavedViewsMenu,
  useHashAction,
  useListView,
  useSession,
  useTypedFilter,
  type ListDefinition,
  type ListView,
} from '@ownsolid/shell';
import {
  Button,
  Card,
  Dialog,
  EmptyState,
  ProblemNotice,
  SearchField,
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
import { Link, useNavigate } from '@tanstack/react-router';
import { ArrowLeft, Building2, Plus } from 'lucide-react';
import { useMemo, useState, type SubmitEvent } from 'react';

/** Everything under this key follows a change, including the workspace switcher. */
export const workspacesKey = ['workspaces'] as const;

const definition: ListDefinition = {
  key: 'workspaces',
  filters: { name: { kind: 'text', maximum: 100 } },
  columns: [
    { id: 'name', label: 'workspaces.column.name', locked: true },
    { id: 'status', label: 'workspaces.column.status' },
    { id: 'created', label: 'workspaces.column.created' },
  ],
};

/** Thrown when no account has the address given for the owner. */
class NoSuchOwner extends Error {}

/** The account with exactly this address. */
async function ownerId(email: string): Promise<string> {
  const address = email.trim().toLowerCase();
  const page = await listUsers({ limit: 2, emailPrefix: address });
  const found = page.items.find((user) => user.email.toLowerCase() === address);
  if (!found) throw new NoSuchOwner();
  return found.id;
}

function CreateDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const me = useSession();
  const toast = useToast();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [owner, setOwner] = useState(me?.user.email ?? '');
  const create = useMutation({
    mutationFn: async () =>
      createWorkspace({ name: name.trim(), ownerUserId: await ownerId(owner) }),
    onSuccess: (workspace) => {
      void queryClient.invalidateQueries({ queryKey: workspacesKey });
      onClose();
      toast({ message: t('workspaces.create.done', { name: workspace.name }) });
      void navigate({
        to: '/users/workspaces/$workspaceId',
        params: { workspaceId: workspace.id },
      });
    },
  });
  const ready = name.trim() !== '' && owner.trim() !== '' && !create.isPending;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready) create.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('workspaces.create.title')}
      description={t('workspaces.create.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form="create-workspace" variant="primary" disabled={!ready}>
            {create.isPending ? (
              <Spinner label={t('workspaces.create.submit')} className="text-white" />
            ) : null}
            {t('workspaces.create.submit')}
          </Button>
        </>
      }
    >
      <form id="create-workspace" noValidate onSubmit={submit} className="flex flex-col gap-4">
        {isProblem(create.error) ? (
          <ProblemNotice
            problem={create.error}
            onRetry={() => {
              create.mutate();
            }}
          />
        ) : null}
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
        <TextField
          label={t('workspaces.create.owner')}
          name="owner"
          type="email"
          autoComplete="off"
          required
          hint={t('workspaces.create.owner.hint')}
          error={create.error instanceof NoSuchOwner ? t('workspaces.noAccount') : undefined}
          value={owner}
          onChange={(event) => {
            setOwner(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function Rows({ items, list }: { items: readonly Workspace[]; list: ListView }) {
  const { t, format } = useI18n();
  const shows = (id: string) => !list.state.hiddenColumns.includes(id);
  return (
    <Card className="overflow-hidden">
      <Table>
        <thead>
          <tr>
            <Th>{t('workspaces.column.name')}</Th>
            {shows('status') ? <Th>{t('workspaces.column.status')}</Th> : null}
            {shows('created') ? <Th>{t('workspaces.column.created')}</Th> : null}
          </tr>
        </thead>
        <tbody>
          {items.map((workspace) => (
            <Tr key={workspace.id}>
              <Td className="font-medium">
                <Link
                  to="/users/workspaces/$workspaceId"
                  params={{ workspaceId: workspace.id }}
                  className="text-text underline-offset-2 hover:underline"
                >
                  {workspace.name}
                </Link>
                {workspace.is_default ? (
                  <span className="ml-2">
                    <Tag>{t('workspaces.default')}</Tag>
                  </span>
                ) : null}
              </Td>
              {shows('status') ? (
                <Td>
                  <StatusPill tone={workspace.status === 'active' ? 'good' : 'neutral'}>
                    {t(`workspaces.status.${workspace.status}`)}
                  </StatusPill>
                </Td>
              ) : null}
              {shows('created') ? (
                <Td className="text-text-2">{format.dateTime(workspace.created_at)}</Td>
              ) : null}
            </Tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

/** `/users/workspaces`: every workspace, oldest first. */
export function WorkspacesPage() {
  const { t } = useI18n();
  const config = useQuery(runtimeConfigQuery()).data?.ui;
  const limit = config?.pageSize ?? 20;
  const list = useListView(definition);
  const name = list.state.filters.name;
  const [typed, setTyped] = useTypedFilter(list, 'name', config?.searchDebounceMilliseconds ?? 0);
  const [creating, setCreating] = useState(useHashAction('create'));
  const workspaces = useInfiniteQuery({
    queryKey: [...workspacesKey, 'all', limit, name],
    queryFn: ({ pageParam, signal }) =>
      listAllWorkspaces({ limit, cursor: pageParam ?? undefined, search: name }, { signal }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = useMemo(
    () => workspaces.data?.pages.flatMap((page) => page.items) ?? [],
    [workspaces.data],
  );
  const open = () => {
    setCreating(true);
  };

  let body;
  if (workspaces.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (workspaces.error && items.length === 0) {
    body = isProblem(workspaces.error) ? (
      <ProblemNotice
        problem={workspaces.error}
        onRetry={() => {
          void workspaces.refetch();
        }}
      />
    ) : null;
  } else if (items.length === 0 && name !== undefined) {
    body = (
      <Card>
        <EmptyState
          icon={Building2}
          title={t('workspaces.emptyFilter.title')}
          action={<Button onClick={list.clearFilters}>{t('workspaces.emptyFilter.clear')}</Button>}
        >
          {t('workspaces.emptyFilter.detail')}
        </EmptyState>
      </Card>
    );
  } else if (items.length === 0) {
    body = (
      <Card>
        <EmptyState
          icon={Building2}
          title={t('workspaces.empty.title')}
          action={
            <Button variant="primary" onClick={open}>
              <Plus aria-hidden />
              {t('workspaces.create.open')}
            </Button>
          }
        >
          {t('workspaces.empty.detail')}
        </EmptyState>
      </Card>
    );
  } else {
    body = (
      <>
        <Rows items={items} list={list} />
        {workspaces.hasNextPage ? (
          <Button
            className="mt-4"
            disabled={workspaces.isFetchingNextPage}
            onClick={() => {
              void workspaces.fetchNextPage();
            }}
          >
            {t('workspaces.loadMore')}
          </Button>
        ) : null}
      </>
    );
  }

  return (
    <>
      <Link
        to="/users"
        className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
      >
        <ArrowLeft aria-hidden className="size-4" />
        {t('workspaces.back')}
      </Link>
      <div className="mb-6 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('workspaces.title')}
          </h1>
          <p className="m-0 max-w-[760px] text-text-2">{t('workspaces.subtitle')}</p>
        </div>
        <Button variant="primary" onClick={open}>
          <Plus aria-hidden />
          {t('workspaces.create.open')}
        </Button>
      </div>
      <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
        <SearchField
          label={t('workspaces.search')}
          placeholder={t('workspaces.search')}
          className="w-full max-w-[320px] flex-1 basis-[220px]"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
        <div className="ml-auto flex items-center gap-2">
          <SavedViewsMenu list={list} />
          <ColumnChooser list={list} />
          <CopyLinkButton />
        </div>
      </div>
      {body}
      {creating ? (
        <CreateDialog
          onClose={() => {
            setCreating(false);
          }}
        />
      ) : null}
    </>
  );
}
