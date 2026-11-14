// Adding a node: name it, and follow the steps on the host. The token is shown here once and stays
// out of the commands, so they can be pasted anywhere; it reaches the host through a file, never a
// command line or shell history (docs/research/nodes-page.md).
import {
  checkKubernetesEnrollment,
  createJoinToken,
  isProblem,
  listNodes,
  type MintedJoinToken,
  type Node,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  capabilitiesQuery,
  readyCapabilityNames,
  runtimeConfigQuery,
  useWorkspaces,
} from '@ownsolid/shell';
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
import { Link } from '@tanstack/react-router';
import { useEffect, useState, type SubmitEvent } from 'react';

const configDirectory = '/etc/ownsolid-agent';
const stateDirectory = '/var/lib/ownsolid-agent';
const configFile = `${configDirectory}/agent.json`;
const inferenceDirectory = '/var/lib/ownsolid-inference';

/** The one socket the agent accepts: rootful Docker's, owned by root. */
const dockerSocket = '/run/docker.sock';

/** The image the approved recipe runs, by digest (api/agent/v1/inference-recipe.json). */
export const servingImage =
  'vllm/vllm-openai@sha256:a4a4c0437bf7240089da5f08aa370c4aee17ae5290f7a3b468825ee26c4c3a6b';

/** What a node can be asked to do. It is fixed when the node joins. */
const roles = [
  'read_only',
  'docker_inference',
  'kubernetes_node',
] as const satisfies readonly MintedJoinToken['execution_profile'][];

type Role = (typeof roles)[number];

/** The Agent accepts an HTTPS origin without a path, query, fragment or credentials. */
function agentOrigin(value: string): string {
  const address = value.trim();
  if (!/^https:\/\/[^/?#@\\\s]+\/?$/i.test(address)) return '';
  try {
    return new URL(address).origin;
  } catch {
    return '';
  }
}

function kubernetesUnavailable(error: unknown) {
  if (isProblem(error)) {
    if (error.status === 404) return 'nodes.add.kubernetes.olderControl';
    if (error.status === 403) return 'nodes.add.kubernetes.permission';
    if (error.code === 'KUBERNETES_CELLS_UNCONFIGURED') return 'nodes.add.kubernetes.unconfigured';
    if (error.code === 'KUBERNETES_CLUSTER_STORAGE_UNSUPPORTED')
      return 'nodes.add.kubernetes.storage';
  }
  return 'nodes.add.kubernetes.checkFailed';
}

/** Where the agent keeps what it needs. */
function directoryCommand(serving: boolean): string {
  const directories = [configDirectory, stateDirectory, ...(serving ? [inferenceDirectory] : [])];
  return `sudo install -d -m 0700 ${directories.join(' ')}`;
}

/** How to bring the serving image to a host with no route to a registry. */
export const imageCommands = [
  '# On a machine with a route to the registry:',
  `docker pull ${servingImage}`,
  `docker save ${servingImage} -o ownsolid-serving.tar`,
  '# Copy the file to the host, then on the host:',
  'sudo docker load -i ownsolid-serving.tar',
].join('\n');

/** How to store the token: the shell waits, and what is pasted goes only into the file. */
const tokenCommand = `sudo sh -c 'umask 077; cat > ${configDirectory}/join-token'`;

/** How to write the agent's configuration, join and start reporting. */
function joinCommands(origin: string, role: MintedJoinToken['execution_profile']): string {
  const configuration = JSON.stringify({
    control_origin: origin,
    ca_file: `${configDirectory}/ca.pem`,
    state_directory: stateDirectory,
    disk_path: '/',
    join_token_file: `${configDirectory}/join-token`,
    ...(role === 'read_only' ? {} : { execution_profile: role }),
    ...(role === 'docker_inference'
      ? { inference: { root_directory: inferenceDirectory, docker_socket: dockerSocket } }
      : {}),
  });
  return [
    `sudo sh -c 'umask 077; cat > ${configFile}' <<'EOF'`,
    configuration,
    'EOF',
    `sudo agent enroll --config ${configFile}`,
    `sudo agent run --config ${configFile}`,
  ].join('\n');
}

function JoinProgress({
  token,
  onClose,
  onOpen,
}: {
  token: MintedJoinToken;
  onClose: () => void;
  onOpen: (node: Node) => void;
}) {
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
  return (
    <div className="flex w-full flex-wrap items-center justify-end gap-2">
      <div className="mr-auto min-w-0 grow basis-48 text-text-2">
        {joined ? (
          <span role="status" className="font-semibold">
            {t('nodes.add.joined', { name: joined.name })}
          </span>
        ) : isProblem(found.error) ? (
          <ProblemNotice problem={found.error} onRetry={() => void found.refetch()} />
        ) : (
          <div className="flex items-center gap-2">
            <Spinner label={t('nodes.add.waiting')} />
            <span aria-hidden>{t('nodes.add.waiting')}</span>
          </div>
        )}
      </div>
      <Button onClick={onClose}>{t('nodes.add.close')}</Button>
      {joined ? (
        <Button
          variant="primary"
          onClick={() => {
            onOpen(joined);
          }}
        >
          {t('nodes.add.openNode')}
        </Button>
      ) : null}
    </div>
  );
}

function Steps({ token, origin }: { token: MintedJoinToken; origin: string }) {
  const { t, format } = useI18n();
  return (
    <div className="flex flex-col gap-4">
      <Notice
        title={t('nodes.add.tokenOnce', { time: format.dateTime(token.expires_at) })}
        className="m-0"
      >
        <CopyText text={token.token} className="mt-1 break-all" />
      </Notice>
      <EnrollmentCommands origin={origin} profile={token.execution_profile} />
    </div>
  );
}

/** Offline host preparation and enrollment instructions shared by the single and CSV flows. */
export function EnrollmentCommands({
  origin,
  profile,
}: {
  origin: string;
  profile: MintedJoinToken['execution_profile'];
}) {
  const { t } = useI18n();
  const serving = profile === 'docker_inference';
  const kubernetes = profile === 'kubernetes_node';
  return (
    <div className="flex flex-col gap-4">
      <ol className="m-0 flex list-decimal flex-col gap-3.5 pl-5">
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.directories')}</p>
          <CodeBlock
            code={directoryCommand(serving)}
            label={t('nodes.add.step.directoriesLabel')}
          />
          <p className="m-0 mt-2">
            {t(kubernetes ? 'nodes.add.kubernetes.files' : 'nodes.add.step.files', {
              directory: configDirectory,
            })}
          </p>
        </li>
        {serving ? (
          <li>
            <p className="m-0 mb-2">{t('nodes.add.step.image')}</p>
            <CodeBlock code={imageCommands} label={t('nodes.add.step.imageLabel')} />
          </li>
        ) : null}
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.token')}</p>
          <CodeBlock code={tokenCommand} label={t('nodes.add.step.tokenLabel')} />
        </li>
        <li>
          <p className="m-0 mb-2">{t('nodes.add.step.join')}</p>
          <CodeBlock code={joinCommands(origin, profile)} label={t('nodes.add.step.joinLabel')} />
        </li>
      </ol>
      <p className="m-0 text-[12.5px] text-text-3">
        {t(
          serving
            ? 'nodes.add.serving'
            : kubernetes
              ? 'nodes.add.kubernetes.enrolled'
              : 'nodes.add.readOnly',
        )}
      </p>
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
  const capabilities = useQuery(capabilitiesQuery());
  const systems = readyCapabilityNames(t, capabilities.data, 'node-operating-systems');
  const kubernetes = useQuery({
    queryKey: ['nodes', 'kubernetes-enrollment'],
    queryFn: ({ signal }) => checkKubernetesEnrollment({ signal, background: true }),
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const checking = kubernetes.isPending || kubernetes.isFetching;
  const kubernetesReady = kubernetes.isSuccess && !checking;
  const unavailable =
    !checking && kubernetes.isError ? kubernetesUnavailable(kubernetes.error) : null;
  const [name, setName] = useState('');
  const [chosen, setChosen] = useState<string | null>(null);
  const [role, setRole] = useState<Role>('read_only');
  const [address, setAddress] = useState('');
  const origin = agentOrigin(address);
  const wantsKubernetes = role === 'kubernetes_node';
  const workspace = chosen ?? workspaces.active?.id ?? '';
  const mint = useMutation({
    mutationFn: async () => ({
      token: await createJoinToken({
        workspaceId: workspace,
        nodeName: name.trim(),
        executionProfile: role,
      }),
      origin: wantsKubernetes ? origin : window.location.origin,
    }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['nodes', 'join-tokens'] }),
  });
  const token = mint.data?.token;
  const rejected = wantsKubernetes && isProblem(mint.error, 'AGENT_PROFILE_REJECTED');
  const canMint =
    name.trim() !== '' &&
    workspace !== '' &&
    !mint.isPending &&
    (!wantsKubernetes || (kubernetesReady && origin !== ''));
  const issue = () => {
    if (canMint) mint.mutate();
  };
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    issue();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={token ? t('nodes.add.stepsTitle', { name: token.node_name }) : t('nodes.add.title')}
      description={token ? undefined : t('nodes.add.detail')}
      footer={
        token ? (
          <JoinProgress token={token} onClose={onClose} onOpen={onOpenNode} />
        ) : (
          <>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            <Button type="submit" form="add-node" variant="primary" disabled={!canMint}>
              {t(wantsKubernetes ? 'nodes.add.kubernetes.submit' : 'nodes.add.submit')}
            </Button>
          </>
        )
      }
    >
      {mint.data ? (
        <Steps token={mint.data.token} origin={mint.data.origin} />
      ) : (
        <form id="add-node" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
          {mint.error && isProblem(mint.error) ? (
            <ProblemNotice
              problem={mint.error}
              onRetry={canMint ? issue : undefined}
              title={rejected ? t('nodes.add.kubernetes.rejected') : undefined}
              detail={rejected ? t('nodes.add.kubernetes.rejectedDetail') : undefined}
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
            disabled={mint.isPending}
            hint={t('nodes.add.name.hint')}
            value={name}
            onChange={(event) => {
              setName(event.target.value);
            }}
          />
          <SelectField
            label={t('nodes.add.role')}
            hint={
              <>
                {t('nodes.add.role.hint')}{' '}
                <span role="status" className="block">
                  {checking ? t('nodes.add.kubernetes.checking') : null}
                  {unavailable ? t(unavailable) : null}
                </span>
              </>
            }
            disabled={mint.isPending}
            value={role}
            options={roles.map((value) => ({
              value,
              label: t(`nodes.add.role.${value}`),
              disabled: value === 'kubernetes_node' && !kubernetesReady,
            }))}
            onChange={(event) => {
              setRole(roles.find((value) => value === event.target.value) ?? 'read_only');
              mint.reset();
            }}
          />
          {unavailable === 'nodes.add.kubernetes.checkFailed' ? (
            <Button size="sm" className="self-start" onClick={() => void kubernetes.refetch()}>
              {t('action.retry')}
            </Button>
          ) : null}
          {wantsKubernetes ? (
            <TextField
              label={t('nodes.add.kubernetes.origin')}
              name="agent-origin"
              type="url"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={mint.isPending}
              value={address}
              hint={t('nodes.add.kubernetes.originHint')}
              error={
                address.trim() !== '' && !origin
                  ? t('nodes.add.kubernetes.originInvalid')
                  : undefined
              }
              onChange={(event) => {
                setAddress(event.target.value);
              }}
            />
          ) : null}
          {workspaces.items.length > 1 ? (
            <SelectField
              label={t('nodes.add.workspace')}
              disabled={mint.isPending}
              value={workspace}
              options={workspaces.items.map((item) => ({ value: item.id, label: item.name }))}
              onChange={(event) => {
                setChosen(event.target.value);
              }}
            />
          ) : null}
          {systems.length > 0 ? (
            <p className="m-0 text-[13px] text-text-2">
              {t('nodes.add.systems', { systems: systems.join(', ') })}{' '}
              <Link to="/support" className="text-accent-ink underline">
                {t('nodes.add.systems.link')}
              </Link>
            </p>
          ) : null}
        </form>
      )}
    </Dialog>
  );
}
