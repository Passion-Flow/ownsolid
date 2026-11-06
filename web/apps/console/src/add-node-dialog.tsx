// Adding a node: name it, and follow the steps on the host. The token is shown here once and stays
// out of the commands, so they can be pasted anywhere; it reaches the host through a file, never a
// command line or shell history (docs/research/nodes-page.md).
import {
  createJoinToken,
  isProblem,
  listNodes,
  type MintedJoinToken,
  type Node,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery, useWorkspaces } from '@ownsolid/shell';
import {
  Button,
  CodeBlock,
  CopyText,
  Dialog,
  Notice,
  ProblemNotice,
  SelectField,
  Spinner,
  TextField,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState, type SubmitEvent } from 'react';

const configDirectory = '/etc/ownsolid-agent';
const stateDirectory = '/var/lib/ownsolid-agent';
const configFile = `${configDirectory}/agent.json`;

/** Where the agent keeps what it needs. */
const directoryCommand = `sudo install -d -m 0700 ${configDirectory} ${stateDirectory}`;

/** How to store the token: the shell waits, and what is pasted goes only into the file. */
const tokenCommand = `sudo sh -c 'umask 077; cat > ${configDirectory}/join-token'`;

/** How to write the agent's configuration, join and start reporting. */
function joinCommands(origin: string): string {
  const configuration = JSON.stringify({
    control_origin: origin,
    ca_file: `${configDirectory}/ca.pem`,
    state_directory: stateDirectory,
    disk_path: '/',
    join_token_file: `${configDirectory}/join-token`,
  });
  return [
    `sudo sh -c 'umask 077; cat > ${configFile}' <<'EOF'`,
    configuration,
    'EOF',
    `sudo agent enroll --config ${configFile}`,
    `sudo agent run --config ${configFile}`,
  ].join('\n');
}

function Waiting({ token, onOpen }: { token: MintedJoinToken; onOpen: (node: Node) => void }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const found = useQuery({
    queryKey: ['nodes', 'joining', token.id],
    queryFn: async ({ signal }) => {
      const page = await listNodes(
        { limit: 1, namePrefix: token.node_name },
        { signal, background: true },
      );
      return (
        page.items.find(
          (node) => node.name === token.node_name && node.created_at >= token.created_at,
        ) ?? null
      );
    },
    refetchInterval: (query) => (query.state.data ? false : poll),
  });
  const joined = found.data ?? null;
  useEffect(() => {
    if (joined) void queryClient.invalidateQueries({ queryKey: ['nodes'] });
  }, [joined, queryClient]);
  return joined ? (
    <div role="status" className="flex flex-wrap items-center justify-between gap-3">
      <span className="font-semibold">{t('nodes.add.joined', { name: joined.name })}</span>
      <Button
        variant="primary"
        onClick={() => {
          onOpen(joined);
        }}
      >
        {t('nodes.add.openNode')}
      </Button>
    </div>
  ) : (
    <div className="flex items-center gap-2 text-text-2">
      <Spinner label={t('nodes.add.waiting')} />
      <span aria-hidden>{t('nodes.add.waiting')}</span>
    </div>
  );
}

function Steps({ token, onOpen }: { token: MintedJoinToken; onOpen: (node: Node) => void }) {
  const { t, format } = useI18n();
  const origin = window.location.origin;
  return (
    <div className="flex flex-col gap-4">
      <Notice
        title={t('nodes.add.tokenOnce', { time: format.dateTime(token.expires_at) })}
        className="m-0"
      >
        <CopyText text={token.token} className="mt-1 break-all" />
      </Notice>
      <ol className="m-0 flex list-decimal flex-col gap-3.5 pl-5">
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.directories')}</p>
          <CodeBlock code={directoryCommand} label={t('nodes.add.step.directoriesLabel')} />
          <p className="m-0 mt-2">{t('nodes.add.step.files', { directory: configDirectory })}</p>
        </li>
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.token')}</p>
          <CodeBlock code={tokenCommand} label={t('nodes.add.step.tokenLabel')} />
        </li>
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.join')}</p>
          <CodeBlock code={joinCommands(origin)} label={t('nodes.add.step.joinLabel')} />
        </li>
      </ol>
      <p className="m-0 text-[12.5px] text-text-3">{t('nodes.add.readOnly')}</p>
      <Waiting token={token} onOpen={onOpen} />
    </div>
  );
}

/** Mints a token for a new node and shows how to use it. Open it fresh each time. */
export function AddNodeDialog({
  onClose,
  onOpenNode,
}: {
  onClose: () => void;
  onOpenNode: (node: Node) => void;
}) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const workspaces = useWorkspaces();
  const [name, setName] = useState('');
  const [chosen, setChosen] = useState<string | null>(null);
  const workspace = chosen ?? workspaces.active?.id ?? '';
  const mint = useMutation({
    mutationFn: () => createJoinToken({ workspaceId: workspace, nodeName: name.trim() }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['nodes', 'join-tokens'] }),
  });
  const token = mint.data;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (name.trim() !== '' && workspace !== '' && !mint.isPending) mint.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={token ? t('nodes.add.stepsTitle', { name: token.node_name }) : t('nodes.add.title')}
      description={token ? undefined : t('nodes.add.detail')}
      footer={
        token ? (
          <Button onClick={onClose}>{t('nodes.add.close')}</Button>
        ) : (
          <>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            <Button
              type="submit"
              form="add-node"
              variant="primary"
              disabled={name.trim() === '' || workspace === '' || mint.isPending}
            >
              {t('nodes.add.submit')}
            </Button>
          </>
        )
      }
    >
      {token ? (
        <Steps token={token} onOpen={onOpenNode} />
      ) : (
        <form id="add-node" className="flex flex-col gap-3.5" onSubmit={submit}>
          {mint.error && isProblem(mint.error) ? (
            <ProblemNotice
              problem={mint.error}
              onRetry={() => {
                mint.mutate();
              }}
            />
          ) : null}
          {workspaces.error && isProblem(workspaces.error) ? (
            <ProblemNotice problem={workspaces.error} onRetry={workspaces.retry} />
          ) : null}
          {!workspaces.isPending && !workspaces.error && workspaces.items.length === 0 ? (
            <Notice title={t('nodes.add.noWorkspace')} />
          ) : null}
          <TextField
            label={t('nodes.add.name')}
            name="node-name"
            autoComplete="off"
            maxLength={100}
            required
            hint={t('nodes.add.name.hint')}
            value={name}
            onChange={(event) => {
              setName(event.target.value);
            }}
          />
          {workspaces.items.length > 1 ? (
            <SelectField
              label={t('nodes.add.workspace')}
              value={workspace}
              options={workspaces.items.map((item) => ({ value: item.id, label: item.name }))}
              onChange={(event) => {
                setChosen(event.target.value);
              }}
            />
          ) : null}
        </form>
      )}
    </Dialog>
  );
}
