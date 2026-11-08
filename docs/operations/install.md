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

### Agent gateways

Control serves agents' session streams itself, which is all a single node needs. To spread the
connections and exchanges of a large fleet over more hosts, run agent gateways beside it
([ADR 0048](../decisions/0048-agent-gateways-beside-control.md)). A gateway is the control image
started with `serve-agent-gateway --config <file>`, where the file is control's configuration
with an `agent_gateway` section added:

```yaml
agent_gateway:
  listen_address: 0.0.0.0:9443
  tls_certificate_file: /run/projectone/certificate.pem
  tls_private_key_file: /run/projectone/private-key.pem
```

The certificate must name the host of the origin agents use (the admin origin unless
`machine_server` is set), because agents verify that name on every gateway. A gateway needs
PostgreSQL, the database identity control serves with and a route to the database. It opens a
pool of `database.max_open_connections`, 10 by default, under the same database role as control,
whose connection limit is control's pool unless `resources.database_role_connections` in the
install configuration raises it: set it to 10 plus each gateway's pool and apply again. A gateway
that finds the limit full stops with `DATABASE_CONNECTION_LIMIT_REACHED`. It serves only the session stream and `/health/live` and `/health/ready`; agents keep
sending enrollment, results, renewals and transfers to the origin.

Give agents the gateways in their configuration, the addresses they reach them on:

```json
"session_addresses": ["10.0.0.21:9443", "10.0.0.22:9443"]
```

Each agent holds one session, on the first gateway of an order of its own, so the fleet spreads
evenly. When that gateway stops answering or is full, the agent's next attempt goes to the next
one; it falls back to HTTPS only after every gateway failed. A gateway that is stopped with
SIGTERM ends its sessions first, so its agents move at once. Agents that moved stay where they
are when the gateway returns.

A gateway serves only while control is running: with control stopped, its readiness probe and
every agent call answer `SERVICE_NOT_READY`, and it logs `agent_gateway_fenced`. When control
returns, the gateway checks the schema and logs `agent_gateway_serving`; after an upgrade that
changed the schema it stops, and must be upgraded with control. Stop the gateways before a
restore or a database maintenance command, which refuse while other clients are connected.
Limits on agent calls apply per gateway. `ownsolid_agent_gateway_sessions` counts each
gateway's open sessions, under `service="agent-gateway"`.

`ownsolid-install` does not place gateways on hosts yet: that comes with the topologies of more
than one host (REL-101).

### Agent certificates

Every node proves its identity with a client certificate
([ADR 0050](../decisions/0050-agent-certificates.md)). The installer creates a private agent CA at
`<install_root>/config/agent-ca.pem`, with its key beside it, and keeps it when you apply again.

A node makes its own key when it joins, and it never leaves the node. It replaces its certificate by
itself, 70% to 90% of the way through the lifetime, without restarting. Certificates last 30 days;
set `tls.agent_certificate_lifetime_hours` to change it (24 to 8760). If a node is offline for longer
than that, it gets a new certificate by itself when it returns.

Nodes joining at once from one network are limited by `runtime.rates.agent_enroll_rack_count` per
`agent_enroll_rack_window_seconds` in `control_settings`, 120 per minute by default, and the network of
one rack is a /24 or /64 unless `agent_enroll_rack_ipv4_prefix` and `agent_enroll_rack_ipv6_prefix` say
otherwise. A node
over the limit waits its turn and keeps its join token.

A node from before certificates keeps working and takes its first certificate when its agent is
upgraded; from then on it needs one. To replace the agent CA by hand, put the new CA before the old one
in `agent-ca.pem`, replace `agent-ca-key.pem` with the new key and restart control. Nodes pick up
certificates from the new CA as they renew. Remove the old CA once every node has renewed.

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

### Repeated heartbeats

Control answers a heartbeat that repeats the last one from memory, without reading the database.
When a node, workspace or tenant changes, control forgets what it held within
`runtime.nodes.outbox_poll_milliseconds`, 200 ms by default, and a held claim ends at once. If
that news stops arriving, `runtime.nodes.view_max_age_seconds`, 60 by default, is the longest a
heartbeat goes unchecked. If control cannot read the news, it goes back to reading the database
for every heartbeat until it can. Tasks, results and transfers always check the database.
`ownsolid_node_view_hits_total` and `ownsolid_node_view_fallbacks_total` show which is happening.

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

### When agents overload control

Agents leave `database.operator_reserved_connections` connections of the pool (default 3: two for you, one for the writer
that records heartbeats) to everyone else, besides the one the installation lease holds, so at most
six agent calls use the default pool of ten at once, so a sign-in and the console keep working while agents hold every serving seat. Raise
the pool, not the reserve, for a fleet that needs more seats.

To tell a fleet to back off, refuse its calls, lowest priority first:

| Level | Control refuses | Effect |
| --- | --- | --- |
| `none` | Nothing | The default |
| `bulk` | Transfers | Nodes stay online |
| `telemetry` | Transfers and telemetry | Nodes stay online; metrics and logs from nodes stop arriving |
| `heartbeats` | Everything except quarantine reports | Nodes turn offline in the console and receive no new work. Tasks and model services already running continue |

Set it in the console under **Nodes**, **Agent pacing**. When the console cannot be reached, set it
on the server, as root or the runtime user:

```sh
ownsolid-install agents shed --config install.yaml --level heartbeats --reason "Database saturated"
ownsolid-install agents status --config install.yaml
```

This goes through a Unix socket at `<install_root>/socket/control.sock`, mode 0600, and needs no
TCP connection, so it works with the connection limit full. Agents honor the retry hint they are
refused with, and return by themselves after `--level none`.

| Line | Meaning |
| --- | --- |
| `AGENT_SHED_LEVEL` | Control enforces this level now |
| `AGENT_SHED_NOT_STORED` | The database did not answer in time. Control acts on the level and keeps trying to store and audit it |
| `AGENT_SHED_HELD` | Control is not answering, for example because its database is stopped: it stops by design without one. The level waits in `socket/agent-shed.json`, and control starts with it, so the fleet stays out when control returns |

Every change is audited; one set on the server has the `operator` audience and names no user. The
gauge `ownsolid_agent_shed_level` holds the level from 0 to 3, and refused calls count in
`ownsolid_agent_admission_refused_total` with the reason `shed`. MySQL and Dameng installs cannot
store a level yet (CMP-201, CMP-202).

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
| `CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED` | This release cannot run that size; the message says why and names the roadmap item. Nothing changed |
| `INSTALL_CONFIG_INVALID` | A field is unknown or invalid; the message names it |
| `INSTALL_ROOT_REQUIRED` | Run `apply` as root |
| `INSTALL_DOCKER_UNAVAILABLE`, `INSTALL_COMPOSE_UNAVAILABLE` | Install or start Docker and the Compose plugin |
| `INSTALL_IMAGE_MISSING` | The named image is not loaded. Check `images.archive_file` and the image names |
| `INSTALL_DATABASE_START_FAILED`, `INSTALL_METRICS_START_FAILED`, `INSTALL_LOGS_START_FAILED`, `INSTALL_CONTROL_START_FAILED` | The service did not become healthy; read the log in the message |
| `INSTALL_OPERATOR_TASK_FAILED` | A schema or administrator task failed; its code is in the message |
| `INSTALL_HEALTH_FAILED` | Serving is up but an origin does not answer through the published port |
| `INSTALL_BUSY` | A backup or restore is running on this installation; apply again when it ends |
| `INSTALL_ROOT_TOO_LONG` | `install_root` leaves no room for the operator socket's address; choose a shorter path |
| `OPERATOR_SOCKET_UNAVAILABLE` | `agents status` found no control on the socket and no held level. Check that control is running and that you are root or its runtime user |

## When control stops unexpectedly

Control needs no shutdown to be safe. If its process is killed, by the out-of-memory
killer or a failing host, Docker starts it again and it picks up from the database:

- Work on nodes keeps running, and agents reconnect and report what finished.
- Accepted tasks stay accepted. A write repeated with the same `Idempotency-Key` finds the
  first result.
- A model upload continues from the import's `uploaded_bytes`. The console does this when
  the same file is chosen again.
- A model import that was being unpacked starts again by itself and spends one of its three
  attempts, which the import's `attempts` shows. An import that uses them all stops with
  `MODEL_RECOVERY_REQUIRED`: cancel it and import the bundle again.
- Mail whose delivery was cut off is sent again. If control cannot tell whether the server
  took it, a repeat is possible, and after the last attempt it is marked failed.

A restart count that keeps rising means something is stopping control:
`docker inspect --format '{{.RestartCount}} {{.State.OOMKilled}}' <project>-control-1`.

## Limits of this release

Every option a release offers, and why any of them cannot be chosen yet, is one declaration. Run
`ownsolid-install capabilities` before you have a configuration, or open **What is supported** in the
console or the portal; both read the same list, and `apply` refuses a size it marks as not ready.

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
- The model store is `data/models` under `install_root`, on the same disk unless that directory
  is a mount. Give it twice the size of every model: the store keeps each uploaded bundle beside
  the model unpacked from it until MOD-120 removes finished uploads. Dataset and training stores
  are not set up by the installer.
- Back up with `ownsolid-install backup`; see [Backup and restore](backup-and-restore.md).
