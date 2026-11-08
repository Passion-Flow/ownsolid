# ownsolid CLI

Operate the Admin control plane from an SSH terminal. The CLI needs a route to
the installation's private Admin entrance. It runs as a standalone executable
on Linux and macOS.

## Build

From the repository root, using the pinned Go toolchain and prepared module cache:

```sh
go build -trimpath -o /path/to/bin/ownsolid ./tools/cli/cmd/ownsolid
go test ./tools/cli/...
```

## Configure and sign in

Set `OWNSOLID_SERVER` to your installation's advertised Admin origin, including
its scheme and non-default port. A `--server` flag overrides that variable, which
in turn overrides the `server` value in `config.json`. The config directory is
`~/.config/ownsolid` by default. `OWNSOLID_CONFIG_DIR` takes precedence over
`$XDG_CONFIG_HOME/ownsolid`. Set either directory override to an absolute path.

For an installation CA that is absent from your Linux trust store, add its
local PEM path to `config.json`. On macOS, set this path for every HTTPS
installation so verification uses local certificates. The configured PEM
becomes the trust pool for that origin. Configure an HTTP origin only if your
installation explicitly uses plaintext transport.

The following configuration fields are optional:

```json
{
  "ca_file": "/path/to/installation-ca.pem",
  "site_id": "default",
  "cell_id": "default",
  "request_timeout": "30s"
}
```

You can also set `server` and `workspace_id` in this file. Keep the CLI config
directory owned by your account with mode 0700. The CLI creates it on first login
if needed. Each Admin origin has a separate mode 0600 session file. The server
controls session expiry and permissions.

```sh
ownsolid login
ownsolid login --email "$ADMIN_EMAIL" --password-file "$PRIVATE_FILE"
ownsolid logout
```

The interactive password prompt hides input. For unattended login, supply an
owner-only mode 0600 file. The CLI saves only the server-issued session credential.
Logout revokes it on the server before removing the local file. A temporary
server failure leaves the file available for retry.

## Read resources

```sh
ownsolid node list
ownsolid node get gpu-worker-01 --output yaml
ownsolid model list --output json
ownsolid model get "$MODEL_ID" --output yaml
ownsolid deployment list --workspace "$WORKSPACE_ID"
ownsolid deployment get "$DEPLOYMENT_ID"
ownsolid version
```

Model and deployment lists use the workspace chosen by `--workspace`, then
`workspace_id` in `config.json`, then the session's current workspace. Node lists
follow every cursor. Model and deployment lists have the bounds of the Admin
API. A deployment UUID selects its detail directly. An explicit workspace from
the flag or config file also scopes that lookup.
To look up a deployment by name, use the exact model name in the selected
workspace, for example `ownsolid deployment get qwen3`. The name must select
one model and one retained deployment. If the result is ambiguous or the model list
reaches the server lookup limit, select by UUID.

The default output is an aligned table. `--output json` and `--output yaml`
emit the resource fields as an array for a list or an object for a detail.
Prompts and errors go to stderr. A failed command exits nonzero. Run
`ownsolid --help` or `ownsolid node list --help` for usage and examples.

## Maintain nodes

Read the node's current `version` with `node get --output json` before a
change. Supply that version and an audit reason. If another operator changes
the node, review the updated resource before issuing a new command.

```sh
ownsolid node disable "$NODE_ID" --version "$VERSION" --reason 'Scheduled maintenance'
ownsolid node enable "$NODE_ID" --version "$NEW_VERSION" --reason 'Maintenance complete'
ownsolid node rename "$NODE_ID" gpu-worker-02 --version "$VERSION" --reason 'Rack move'
ownsolid node remove "$NODE_ID" --version "$VERSION" --reason 'Host decommissioned' --confirm
ownsolid node collect-facts "$NODE_ID" --idempotency-key "$REQUEST_KEY"
ownsolid node tasks "$NODE_ID"
ownsolid node task "$TASK_ID" --output json
ownsolid node task-log "$TASK_ID" --output json
```

Removal revokes the agent credential. Node changes use the server's
version precondition. If the result of a write is uncertain, read the node
and inspect its current version and state before issuing another update.
Use the log response's `cursor` with `node task-log --after` to continue
reading subsequent lines.

Issue enrollment invitations in a private directory you own with mode 0700.
The credential is written once to a new mode 0600 file. Output contains the
invitation metadata. Transfer the file to the new node using your private
installation procedure. If issuance is interrupted, list pending invitations
and revoke the one whose credential was lost before creating another.

```sh
ownsolid node join-token create gpu-worker-01 --profile docker_inference \
  --token-file "$PRIVATE_DIR/enrollment-token"
ownsolid node join-token list
ownsolid node join-token revoke "$TOKEN_ID" --reason 'Installation postponed'
```

## Deploy an approved model

Read placement for the model, then specify the chosen node and GPU. Placement
is a preview. The server validates current availability again on creation.
If GPU information is stale, request a new observation and check its task
before reading placement again.

```sh
ownsolid node inference "$NODE_ID" --output json
ownsolid node probe-inference "$NODE_ID" --idempotency-key "$PROBE_KEY" --reason 'Refresh placement'
ownsolid deployment placement "$MODEL_ID"
ownsolid deployment create --model "$MODEL_ID" --node "$NODE_ID" --gpu "$GPU_UUID" \
  --idempotency-key "$CREATE_KEY" --reason 'Serve approved model'
ownsolid deployment get "$DEPLOYMENT_ID" --output json
ownsolid deployment stop "$DEPLOYMENT_ID" --revision "$REVISION" \
  --idempotency-key "$STOP_KEY" --reason 'Maintenance window' --confirm
```

Deployment writes and placement use the selected workspace. Use
`--workspace` for a workspace other than the session's current one, or set
`workspace_id` in the config file.

`deployment start`, `observe`, `verify` and `remove` accept the same revision,
idempotency key and reason. Removal also requires `--confirm`. Deployment
writes return the accepted intent and last observed state. Read the
deployment or its last task to see when the node has acted. Stopping or
removing a deployment can interrupt callers.

Use a new lowercase request UUID for each new operation. If a deployment
or node task response is lost, rerun with the original key, resource UUIDs,
revision, workspace and reason. Explicit target IDs keep a replay on the
original node.
After a version conflict, review the current state and use a new key for a
new operation.

Retire a model after its references have been released:

```sh
ownsolid model retire "$MODEL_ID" --reason 'Superseded by approved version' --confirm
```

## Import a local model bundle

Read the installation's approved catalog and prepare the matching archive
before entering the disconnected site. The CLI reads the bundle from the
local filesystem and transfers it to the selected workspace.

```sh
ownsolid model import catalog --output json
ownsolid model import start /path/to/model.tar --name qwen3 --accept-license \
  --idempotency-key "$IMPORT_KEY" --reason 'Import approved model'
ownsolid model import list
ownsolid model import resume "$IMPORT_ID" /path/to/model.tar --reason 'Continue approved import'
ownsolid model import get "$IMPORT_ID" --output json
```

If the response to creating the import is lost, repeat `start` with the
original key and arguments. After a transfer interruption, use the reported
import ID with `resume` and the original bundle. The CLI checks the size
and replays the last stored chunk before appending new data. `queued`
means the server accepted the request to verify. Read the import until
it becomes `ready` and reports a model ID, or until it reports a failure.
After correcting a verification failure, use `model import verify` with
the ID and an audit reason. Use `model import cancel` with an audit reason
and `--confirm` to withdraw the import and discard the uploaded bytes.

## Maintain the node exporter

Use a node with an approved systemd management profile and the pinned
archives in the installation's component catalog. The
`systemd_maintenance` enrollment profile also supports configuration,
upgrades, rollback and journal collection.

```sh
ownsolid node exporter status "$NODE_ID" --output json
ownsolid node exporter install "$NODE_ID" --idempotency-key "$INSTALL_KEY" --reason 'Enable monitoring'
ownsolid node exporter configure "$NODE_ID" --log-level warn --max-scrape-requests 4 \
  --idempotency-key "$CONFIGURE_KEY" --reason 'Tune scrape load'
ownsolid node exporter upgrade "$NODE_ID" --version "$APPROVED_VERSION" \
  --idempotency-key "$UPGRADE_KEY" --reason 'Apply approved exporter upgrade'
ownsolid node exporter collect-logs "$NODE_ID" --idempotency-key "$LOG_KEY" --reason 'Diagnose exporter'
ownsolid node exporter logs "$NODE_ID"
```

Use `node task` with the returned task ID to follow execution. The status
command shows desired and observed state separately. To repeat a write
whose result is uncertain, reuse its original key, node UUID and arguments.
Removal requires `--confirm`. Read the approved versions from `status`.

## Publish a deployment alias

Choose an existing gateway in the workspace and the current revision of
its ready deployment. These commands manage the alias record in control.
Serving requests also requires the running gateway and model backend.

```sh
ownsolid deployment alias gateways
ownsolid deployment alias publish "$DEPLOYMENT_ID" --gateway "$GATEWAY_ID" --name qwen3 \
  --revision "$DEPLOYMENT_REVISION" --idempotency-key "$PUBLISH_KEY" --reason 'Publish approved model'
ownsolid deployment alias list
ownsolid deployment alias get "$ALIAS_ID" --output json
ownsolid deployment alias disable "$ALIAS_ID" --revision "$ALIAS_REVISION" \
  --reason 'Withdraw published model' --confirm
```

## Test the Admin workflow offline

With the Go module cache and pinned PostgreSQL image already prepared, run
`tools/cli/testdata/run-admin-tls.sh` from the checkout. The runner compiles
the CLI and dispatcher fixture for the image's Linux architecture and runs
both against a disposable PostgreSQL instance in a Docker network namespace
with only loopback reachable. The test uses synthetic agent facts and
inference observations to verify control-plane state and writes.

To include the approved model import and component catalog tests, prepare
the bundle and archives described in
[development environment](../../docs/development/environment.md) and the
pinned VictoriaLogs image in the runner. Run the combined fixture with
both paths set.

```sh
LANE_D_MODEL_BUNDLE=/path/to/model.tar LANE_D_COMPONENT_STATE=/path/to/state \
  tools/cli/testdata/run-admin-tls.sh
```

The fixture runs the actual archive verifier and reads settled component
logs from VictoriaLogs. It uses synthetic node results for systemd and GPU
operations and verifies publication against a provisioned gateway record.
