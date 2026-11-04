# Install

One typed configuration and one command install ownsolid. `topology.size` selects the
deployment shape; this release runs `single-node`, on one Linux amd64 host with Docker and
Compose. Other sizes are refused until their roadmap items land.

## What you need

- Linux amd64 with Docker Engine and the Compose plugin. No network connection is needed:
  the installed platform makes no outbound connection, not even a DNS lookup of its own
  services. The internal network uses `network.state_subnet` (default `172.31.254.0/26`);
  choose another private subnet if the host already reaches that one.
- `ownsolid-install`, `compose.yaml` and an image archive holding the control, PostgreSQL,
  metrics store and logs store images.
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
| `telemetry` | Starts the metrics and logs stores and waits until both answer |
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
└── data/                       Database files in postgres/, telemetry in metrics/ and logs/
```

Every directory is mode 0700 and every file 0600.

## Telemetry

Metrics and logs live in their own stores, never in the control-plane database
([ADR 0009](../decisions/0009-telemetry-and-log-stores.md)). They keep data for
`telemetry.metrics_retention` and `telemetry.logs_retention`, 30 days by default.

If a store stops, the platform keeps serving. `ownsolid-install status` reports it,
`apply` restarts it, and reading component logs returns `STORAGE_UNAVAILABLE` until it is
back. Telemetry is not backed up yet (OBS-113).

## Node agents

Agents connect to the admin origin over HTTPS. They keep one long-lived HTTP/2 stream for
heartbeats and task claims ([ADR 0007](../decisions/0007-agent-session-protocol.md)).
Anything between agents and control must pass HTTP/2 to keep that stream; through a proxy
that speaks only HTTP/1.1, agents fall back to HTTPS long-polling, which works but uses
more requests. An agent logs `agent_session_transport` with `stream` or `https` when its
transport changes, and `agent_session_fallback` when it falls back.

### Pacing the agents

Control tells every agent how often to report and how to back off, in each heartbeat
reply. To take load off control, a platform administrator changes the pacing with
`PUT /api/v1/sites/default/cells/default/admin/agent-pacing`. Agents follow within one
interval, with no restart.

| Field | Range | Default | Meaning |
| --- | --- | --- | --- |
| `heartbeat_seconds` | 5 to 120 | 10 | Mean time between two heartbeats of an idle agent |
| `jitter_percent` | 0 to 50 | 10 | Spreads each interval around the mean |
| `reconnect_base_seconds` | 1 to 60 | 1 | Ceiling of the random delay after the first failure |
| `reconnect_max_seconds` | base to 900 | 60 | Largest ceiling of that delay as failures continue |

Send the `version` from `GET` on the same path and a `reason`, which goes into the audit
log. `VERSION_CONFLICT` means somebody else changed it: read it again.

`AGENT_PACING_EXCEEDS_ONLINE_WINDOW` means healthy nodes would look offline and lose
inference. One and a half of the longest interval, `heartbeat_seconds` plus its jitter,
must fit inside `nodes.online_seconds`, 30 by default, which allows up to 20 seconds with
no jitter. To go slower, raise `nodes.online_seconds` first. With more than one control
process, the others follow within five seconds.

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
| `INSTALL_DATABASE_START_FAILED`, `INSTALL_METRICS_START_FAILED`, `INSTALL_LOGS_START_FAILED`, `INSTALL_CONTROL_START_FAILED` | The service did not become healthy; read the log in the message |
| `INSTALL_OPERATOR_TASK_FAILED` | A schema or administrator task failed; its code is in the message |
| `INSTALL_HEALTH_FAILED` | Serving is up but an origin does not answer through the published port |

## Limits of this release

- The console and portal currently offer sign-in, sign-out, password reset, registration
  by invitation (portal), single sign-on and the application frame. Other capabilities,
  including installing a license and inviting users, are reached through the API until
  their pages land.
- Upgrades, rollback and signed bundles are separate roadmap items (DEL-101, DEL-104,
  DEL-105). Do not reuse an install root across versions.
- Model, dataset and training stores are not set up by the installer.
