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
   the admin origin and change it on the Account page, under the account menu. To set the
   language of people who have not chosen one, use **Settings** in the console. The file holds a live credential until you do. If
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
| `runtime-access` | Creates the restricted role that serving uses. On an existing installation it grants that role the tables a newer release added, and nothing else |
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

`control` pushes its process metrics to the metrics store every 15 seconds: names starting
with `ownsolid_`, labelled `service` and `instance`. They cover uptime, Go runtime memory
and garbage collection, database connection pool use, and the log queue, including records
dropped or given up on. If the store is down, control logs
`telemetry_metrics_push_failed` once, `telemetry_metrics_push_recovered` when it returns,
and keeps serving.

If a store stops, the platform keeps serving. `ownsolid-install status` reports it,
`apply` restarts it, and reading component logs returns `STORAGE_UNAVAILABLE` until it is
back. Telemetry is not backed up yet (OBS-113).

## Node agents

Add a node from the console's Nodes page: name it, and the page shows a token that works once and
the commands to run on the host, then says when the node has joined. A token not used can be revoked
from the same page. The agent program is not in the install bundle yet (FLT-102): copy it to the host
from the release. A node joins read-only, and the page shows the steps for that. Other execution
profiles need more sections in the agent's configuration, which FLT-102 adds to these steps.

Agents connect to the admin origin over HTTPS. They keep one long-lived HTTP/2 stream for
heartbeats and task claims ([ADR 0007](../decisions/0007-agent-session-protocol.md)).
Anything between agents and control must pass HTTP/2 to keep that stream; through a proxy
that speaks only HTTP/1.1, agents fall back to HTTPS long-polling, which works but uses
more requests. An agent logs `agent_session_transport` with `stream` or `https` when its
transport changes, and `agent_session_fallback` when it falls back.

### Pacing the agents

Control tells every agent how often to report and how to back off, in each heartbeat
reply. To take load off control, a platform administrator changes the pacing on the
console's Nodes page under Agent pacing, or with
`PUT /api/v1/sites/default/cells/default/admin/agent-pacing`. Agents follow within one
interval, with no restart. The page shows the slowest interval the online window allows
before it is saved, and the change works while the license refuses new work.

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

### Limits on agent calls

Control admits every agent call in memory before it touches the database
([ADR 0018](../decisions/0018-agent-call-admission.md)). Each node has its own limit for
each kind of call, each client address has one more, and calls wait for one of a fixed
number of serving seats. When seats run short, control refuses bulk transfers first, then
progress events, then heartbeats, and serves task results and stop receipts longest.
Agent calls do not use the request slots of the console, so a busy fleet cannot lock
operators out, and the other way round.

A refused call carries a retry hint: `429 RATE_LIMITED` when the node or address is over
its limit, `503 AGENT_OVERLOADED` when control sheds the call. The agent waits at least the
hint before it calls again and logs `agent_cycle_failed` with the code; control logs
`agent_call_refused` with the priority and reason at most every ten seconds, and pushes
`ownsolid_agent_admission_*` counters to the metrics store. A running task whose lease
renewal is shed keeps running and renews at the next try.

The defaults fit the default pacing. Change them under `control_settings.runtime.agent_admission`
in the install configuration and run `apply`; the fields and their ranges are in
[`control.example.yaml`](../../services/control/config/examples/control/control.example.yaml).
For example, to allow 600 transfer requests a minute per node:

```yaml
control_settings:
  runtime:
    agent_admission:
      calls:
        download: {per_minute: 600, burst: 120}
```

Limits apply per control process.

### Model containers have no network

A model container on a node can answer requests but cannot open a connection to
anything: not the site network, not the internet, not a service on the node, and not a
DNS server ([ADR 0017](../decisions/0017-workload-egress-guard.md)). The inference agent
enforces this with its own nftables table, `inet ownsolid_workload_egress`, before it
starts a model, and restores it after a reboot or a firewall reload. Leave the table in
place; the agent rebuilds it if it is changed. `nft list table inet ownsolid_workload_egress`
shows one commented group of rules per model network.

The inference agent therefore runs as root in the host's network namespace, with the
`CAP_NET_ADMIN` capability and the `AF_NETLINK` address family allowed, on a kernel with
nf_tables (every supported distribution). It needs no `nft` or `iptables` binary. When it
cannot install the rules, the deployment fails with `INFERENCE_EGRESS_GUARD_UNAVAILABLE`
and the model does not start: fix the agent's privileges and retry the deployment.

## Single sign-on

People can sign in with a Gitea server instead of a password once the installation approves
that server. Administrators then add connectors on the console's **Single sign-on** page, which
shows the callback URLs to register in Gitea and tests each connector against it. Until the
installation turns single sign-on on, that page says so and adds nothing.

`install.yaml` has no typed fields for this yet, so it is set under `control_settings` and the
two files it names are placed in `<install_root>/config/` by hand:

1. Create the keyring that encrypts connector secrets, with the control image itself, into a
   new directory owned by `runtime.uid` (10001 unless changed), then move it into place:

   ```sh
   install -d -o 10001 -g 10001 -m 0700 /root/sso-keyring
   docker run --rm --network none --user 10001:10001 -v /root/sso-keyring:/out \
     <images.control> initialize-sso-keyring --sso-keyring-output /out/sso-keyring.json
   install -o 10001 -g 10001 -m 0600 /root/sso-keyring/sso-keyring.json <install_root>/config/
   ```

   Keep a copy of this file somewhere safe. Control opens every stored client secret when it
   starts: with a keyring that cannot open them it stops with `SSO_ENCRYPTED_STATE_INVALID`, for
   password sign-in too, even after single sign-on is turned off in `control_settings`.
   Connectors cannot be deleted, so the only recovery today is restoring this file.
2. Copy the CA certificate that signed Gitea's TLS certificate to
   `<install_root>/config/gitea-ca.pem`, owned by `runtime.uid` and mode 0600. Leave
   `ca_file` out when Gitea's certificate chains to the image's system roots.
3. Add the section and run `apply`:

   ```yaml
   control_settings:
     sso:
       enabled: true
       keyring_file: /run/projectone/sso-keyring.json
       providers:
         - origin: https://git.example.com        # Gitea's ROOT_URL, without the path
           issuer: https://git.example.com        # the same, with no trailing slash
           dial_addresses: [10.0.0.5]            # where control reaches it; no DNS is used
           ca_file: /run/projectone/gitea-ca.pem
   ```

Both entrances must be HTTPS, since Gitea calls them back. Control connects only to the listed
addresses, without a proxy, and refuses any other origin. Removing a provider from the list makes
its connectors unusable at once and ends the sessions that started through them; the page marks
them. Setting `enabled: false` keeps connectors and links, signs no one in with them, and leaves
password sign-in working.

The page's **Test connection** asks Gitea's token endpoint to exchange an authorization code
that cannot exist, with the connector's client ID, secret and each callback URL. Gitea checks
those before the code, so the answer says which of them it refused, or that it accepted them all.
Nothing is created in Gitea and nobody is signed in; each test is in the audit trail as
`sso.connector_tested`.

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
| `INSTALL_BUSY` | A backup or restore is running on this installation; apply again when it ends |

## Limits of this release

- The console and portal currently offer sign-in, sign-out, password reset, registration
  by invitation (portal), single sign-on and the application frame. The console's
  **Email delivery** page shows every invitation and password email, why one failed (for
  example an unreachable SMTP host or a refused sign-in), and sends a failed one again
  once the mail server works. A failed email is retried four times over about ten minutes
  before it shows as failed. The **License** page imports the license and explains a refused
  file; the **Users** page invites people and leads to **Workspaces**; **Nodes** adds them. The **Overview** shows what needs
  attention and what is left to set up, and the **Audit trail** records every privileged action.
  **Single sign-on** adds and tests Gitea connectors once the installation approves a Gitea
  server (above).
  Other capabilities are reached
  through the API until their pages land.
- Upgrades, rollback and signed bundles are separate roadmap items (DEL-101, DEL-104,
  DEL-105). Do not reuse an install root across versions.
- Model, dataset and training stores are not set up by the installer.
- Back up with `ownsolid-install backup`; see [Backup and restore](backup-and-restore.md).
