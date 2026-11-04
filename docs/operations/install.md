# Install

One typed configuration and one command install ownsolid. `topology.size` selects the
deployment shape; this release runs `single-node`, on one Linux amd64 host with Docker and
Compose. Other sizes are refused until their roadmap items land.

## What you need

- Linux amd64 with Docker Engine and the Compose plugin. No network connection is needed.
- `ownsolid-install`, `compose.yaml` and an image archive holding the control and
  PostgreSQL images.
- The license issuer's public trust file.
- An internal SMTP relay for account mail.

## Install

1. Copy [`install.example.yaml`](../../deploy/offline/install.example.yaml), fill it in and
   check it:

   ```sh
   ownsolid-install validate --config install.yaml
   ```

2. Apply it as root:

   ```sh
   sudo ownsolid-install apply --config install.yaml
   ```

   Each step prints as it finishes. The last line is `INSTALL_COMPLETE`, with both origins
   and the path of the administrator credential.

3. Read the generated password from `<install_root>/operator/admin-credential`, sign in at
   the admin origin and change it. The file holds a live credential until you do. If
   `tls.source` is `generate`, clients must trust `<install_root>/config/ca.pem`.

4. Check the installation at any time:

   ```sh
   ownsolid-install status --config install.yaml
   ```

## What apply does

| Step | Effect |
| --- | --- |
| `preflight` | Finds Docker and Compose |
| `configuration` | Writes the files below; generated secrets are kept across runs |
| `images` | Loads `images.archive_file` if set and confirms each image is local. Nothing is pulled |
| `stop-control` | Stops serving, because schema tasks hold the installation lock |
| `database` | Starts PostgreSQL and waits until it is healthy |
| `vector-extension` | Installs pgvector 0.8.7 |
| `schema` | Applies migrations |
| `runtime-access` | Creates the restricted role that serving uses |
| `check-schema` | Verifies the ledger, catalog and grants |
| `administrator` | Creates the first administrator once |
| `control` | Starts serving and waits until it is healthy |
| `health` | Requests `/health/ready` on both origins through the published port |

## Files

```text
<install_root>/
├── compose.yaml, compose.env   What Compose runs; edit install.yaml, not these
├── config/                     Mounted read-only into serving; owned by runtime.uid
├── operator/                   Privileged database identity and administrator credential.
│                               Serving never mounts this directory
├── secrets/                    PostgreSQL and runtime passwords, generated CA key
└── data/postgres/              Database files
```

Every directory is mode 0700 and every file 0600.

## When a step fails

The message names the step and the cause, followed by the end of the service log when one
started. Fix the cause and run `apply` again. Steps repeat safely: migrations already
applied are skipped, and an interrupted administrator setup reuses its credential file
instead of issuing a second one.

| Code | Meaning |
| --- | --- |
| `CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED` | This release cannot run that size. Nothing changed |
| `INSTALL_CONFIG_INVALID` | A field is unknown or invalid; the message names it |
| `INSTALL_ROOT_REQUIRED` | Run `apply` as root |
| `INSTALL_DOCKER_UNAVAILABLE`, `INSTALL_COMPOSE_UNAVAILABLE` | Install or start Docker and the Compose plugin |
| `INSTALL_IMAGE_MISSING` | The named image is not loaded. Check `images.archive_file` and the image names |
| `INSTALL_DATABASE_START_FAILED`, `INSTALL_CONTROL_START_FAILED` | The service did not become healthy; read the log in the message |
| `INSTALL_OPERATOR_TASK_FAILED` | A schema or administrator task failed; its code is in the message |
| `INSTALL_HEALTH_FAILED` | Serving is up but an origin does not answer through the published port |

## Limits of this release

- The console and portal are not in the image yet: both origins serve a placeholder page,
  and the API is the only interface. This install path is not complete until they land
  (DEL-001, UX-001).
- Upgrades, rollback and signed bundles are separate roadmap items (DEL-101, DEL-104,
  DEL-105). Do not reuse an install root across versions.
- Model, dataset and training stores are not set up by the installer.
