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

## Inspect gateway request history

```sh
ownsolid gateway request list --limit 20
ownsolid gateway request list --api-key-id "$KEY_ID" --output json
ownsolid gateway request list --api-key-id "$KEY_ID" --cursor "$NEXT_CURSOR" --output json
ownsolid gateway request get "$REQUEST_ID" --output json
```

Use the gateway response's `X-Request-ID` to find the recorded call in the
matching site and cell. The commands use the selected workspace. List
reads one page. In JSON and YAML, `items` contains the records and
`next_cursor` is null at the end. In table output, the continuation is shown
after the rows. Reuse it with the same workspace and key filter. Without
`--limit`, the installation selects the page size.

Detail JSON or YAML includes the stored outcome. A null outcome means no
reported receipt. An outcome can have null usage. A numeric zero in usage
is a verified token count.

## Query installation audit events

```sh
ownsolid audit list --category auth --limit 20
ownsolid audit list --category auth --cursor "$NEXT_CURSOR" --output json
ownsolid audit list --request-id "$REQUEST_ID" --assistant-actor --output json
```

The audit list uses the platform Admin session and covers the installation.
Narrow it by exact action, action family, actor user, outcome, request,
target, or RFC 3339 time window. The server applies its configured window
and page size when omitted. Use the same filters with the returned cursor.
JSON and YAML include `items` and `next_cursor`, which is null at the end.
The table displays the continuation after the rows.

Use `--assistant-actor` to request verified assistant attribution when
the server has it. The assistant and the user subject stay separate in
structured output.

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

## Manage cell agent pacing

```sh
ownsolid node pacing get --output json
ownsolid node pacing set --heartbeat-seconds 15 --jitter-percent 10 \\
  --reconnect-base-seconds 2 --reconnect-max-seconds 120 \\
  --version "$VERSION" --reason 'Reduce maintenance load' --confirm
```

Use the site and cell configured for the Admin origin. Read the current
`version` and `online_window_seconds` before replacing the settings. A
write supplies all four values, the version, and an audit reason. After
a conflict or uncertain response, read the retained settings and version
before issuing another write.

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

## Preview and apply an alias cutover

Read the current alias revision with `deployment alias get --output json`.
Preview the action before applying it. The table shows both revisions,
deployment targets and pause states.

```sh
ownsolid deployment alias cutover history "$ALIAS_ID" --limit 10
ownsolid deployment alias cutover preview "$ALIAS_ID" pause \\
  --revision "$ALIAS_REVISION" --reason 'Pause new calls for maintenance'
ownsolid deployment alias cutover apply "$ALIAS_ID" pause \\
  --revision "$ALIAS_REVISION" --reason 'Pause new calls for maintenance' \\
  --idempotency-key "$REQUEST_KEY" --confirm
```

Use `retarget` with `--deployment` and `--deployment-revision` for a
ready replacement. Use `rollback` with those flags and `--source-revision`
from the alias target history. The target must serve the model recorded at
that source revision. Both actions leave the alias paused. Use `resume`
to open it to new calls once it is ready. Pause and resume use the current
target. If the server reports undrained requests, wait for those requests
to finish or follow the gateway recovery procedure before proceeding.

If the revision has changed, read it again and preview the new change.
If an apply response is lost, retry with the same UUID and arguments.
A replay prints the originally applied revision and the current alias
state in JSON or YAML. To read earlier history, reuse the same workspace
and alias with the returned `--cursor`.

## Create and manage a training job

Use an approved model, an immutable published dataset version, and an
eligible training node and GPU in the selected workspace. Put the training
configuration in a local JSON file. The file holds only the
[training configuration](../../api/openapi/v1/training/openapi.json),
for example:

```json
{
  "device": "nvidia_bfloat16",
  "maximum_steps": 100,
  "maximum_tokens": 256,
  "batch_size": 1,
  "gradient_accumulation_steps": 1,
  "learning_rate": "0.0002",
  "rank": 8,
  "alpha": 16,
  "dropout": "0",
  "checkpoint_steps": 10,
  "seed": 42,
  "threads": 2
}
```

```sh
ownsolid training create --node "$NODE_ID" --gpu "$GPU_UUID" --model "$MODEL_ID" \
  --dataset "$DATASET_ID" --dataset-version "$VERSION_ID" \
  --configuration training.json --idempotency-key "$REQUEST_KEY" \
  --reason 'Train an approved adapter' --output json
ownsolid training get "$JOB_ID" --output json
ownsolid training cancel "$JOB_ID" --revision "$REVISION" --reason 'Withdraw this job'
```

Read an attempt ID from the retained job history and use it to select that
attempt's metrics. Each command reads one page. Use the returned cursor with
the same workspace, job and attempt. Metric records are ordered by event
sequence and keep the reported optimizer step and values.

```sh
ownsolid training attempts "$JOB_ID" --limit 10 --output json
ownsolid training metrics "$JOB_ID" --attempt "$ATTEMPT_ID" --limit 20
ownsolid training metrics "$JOB_ID" --attempt "$ATTEMPT_ID" --cursor "$NEXT_CURSOR" --output json
```

Reuse the request UUID and original arguments if a create result is uncertain.
The workspace comes from `--workspace`, then `workspace_id` in `config.json`,
then the current Admin session. Read the current revision before cancellation.
If the server reports a revision conflict or an uncertain cancellation,
read the job again before retrying. A queued job becomes `cancelled` when
the request commits. A running job stays `cancelling` until its worker stops.

To run the focused Admin TLS fixture, set `PROJECT_ONE_TEST_DSN` for a
disposable PostgreSQL database with pgvector and `PROJECT_ONE_DATA_PYTHON`
to the canonical absolute path of an installed Python 3.12 executable.
Run the fixture in an isolated
network namespace with the database available through loopback.

```sh
tools/cli/testdata/run-training-tls.sh
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

### Register a node-managed gateway

When the installation has a model gateway CA and the selected inference node
supports gateway tasks, queue the node's gateway with:

```sh
ownsolid deployment alias provision "$NODE_ID" --name rack-seven \
  --idempotency-key "$REQUEST_KEY" --reason 'Start the node gateway' --output json
ownsolid deployment alias gateways --output json
```

Use the same request key and arguments after a lost response. Read
`delivery_state` and `delivery_error_code` in the gateway list. Once the
state is `running`, use `deployment alias publish` with the gateway ID
and the deployment's current revision. The agent keeps the gateway's
credential and supplies its configured serving origin.
