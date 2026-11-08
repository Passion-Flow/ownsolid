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
ownsolid deployment list --workspace "$WORKSPACE_ID"
ownsolid deployment get "$DEPLOYMENT_ID"
ownsolid version
```

Model and deployment lists use the workspace chosen by `--workspace`, then
`workspace_id` in `config.json`, then the session's current workspace. Node lists
follow every cursor. Model and deployment lists have the bounds of the Admin
API. A deployment detail can be retrieved by UUID across authorized workspaces.
To look up a deployment by name, use the exact model name in the selected
workspace, for example `ownsolid deployment get qwen3`. The name must select
one model and one retained deployment. If the result is ambiguous or the model list
reaches the server lookup limit, select by UUID.

The default output is an aligned table. `--output json` and `--output yaml`
emit the resource fields as an array for a list or an object for a detail.
Prompts and errors go to stderr. A failed command exits nonzero. Run
`ownsolid --help` or `ownsolid node list --help` for usage and examples.
