# Break-glass operator path and agent shedding

Research behind ARC-016: how an administrator keeps control of an installation when the fleet is
what is overloading it.

## What agents and operators share today

| Resource | Default | Who can fill it |
| --- | --- | --- |
| Accepted connections, `http.max_connections` | 128, one count across listeners | Agents: each holds one for hours |
| Database pool, `database.max_open_connections` | 10 | Agents: `agent_admission.seats` is 64, so seats never bound the pool |
| Request slots, `http.max_active_requests` | 64 | Not agents, since ADR 0018 |
| Control's CPU | 2 CPUs | Everyone |

So ADR 0018 finished one part of the path. A fleet larger than the connection limit, or one slow
database, still stops a console sign-in. The operator also has no way to tell agents to back off: the
only levers are editing limits, which needs the console, or stopping control.

## How others solve it

**Kubernetes** isolates by priority, and exempts its administrators. "The mandatory `exempt` priority
level is used for requests that are not subject to flow control at all: they will always be
dispatched immediately. The mandatory `exempt` FlowSchema classifies all requests from the
`system:masters` group into this priority level." The reason it gives also applies here: exemptions
"prevent an improperly-configured flow control configuration from totally disabling an API server".
([API Priority and Fairness](https://kubernetes.io/docs/concepts/cluster-administration/flow-control/))

**Envoy** sheds by turning load off, and keeps its own door open. Its overload manager will "stop
accepting requests" and "stop accepting connections", and `Admin.ignore_global_conn_limit` keeps the
admin listener reachable while other listeners are at their limit.
([Overload manager](https://www.envoyproxy.io/docs/envoy/latest/configuration/operations/overload_manager/overload_manager))

**HAProxy** gives the operator a local socket that needs no network path: `stats socket
/var/run/haproxy.sock mode 600 level admin`, with runtime commands such as `set maxconn` and `disable
frontend`. File permissions are the credential.
([Management guide](https://docs.haproxy.org/3.0/management.html))

**PostgreSQL** keeps its own last door: `superuser_reserved_connections` holds slots "as final reserve
for emergency use", and "new connections will be accepted only for superusers".
([Connection settings](https://www.postgresql.org/docs/18/runtime-config-connection.html))

Three ideas carry over: reserve capacity for the operator rather than rely on load staying low, keep
one path that does not touch what is overloaded, and give the operator a switch that reduces load
instead of only a way to look at it.

## Chosen approach

**A shed level, held in memory.** `none`, `bulk`, `telemetry` (bulk and telemetry) and `heartbeats`
(everything but quarantine reports). A shed call is refused in admission, before any database work,
with `AGENT_OVERLOADED` and its priority's retry hint, which agents already honor (ADR 0018). The
level takes effect at once in this process and needs no database to do so. It is then stored beside
the pacing row, so it survives a restart and reaches other gateways within the pacing refresh. Static
stability (ADR 0032) is what makes the strongest level safe: running work continues while agents
cannot reach control.

**A reserve in the pool.** `database.operator_reserved_connections`, default 2: agent seats in use
never exceed the pool minus the reserve. Sign-in and the console keep connections when every seat is
taken. This also protects background workers.

**A local socket for the last resort.** Control listens on a Unix socket in the install's operator
directory, mode 0600. `ownsolid-install agents shed --level ... --reason ...` sets the level through it
on the host. It uses no TCP connection, no TLS, no request slot and no pooled connection to take
effect, so it works with the connection limit full and with a slow or saturated database. It is audited
when the database can be reached, and always logged.

**A file on the host for the database outage.** Writing the test showed that control does not survive a
stopped database: losing its installation lease stops the process by design. So a host level also lives
in `socket/agent-shed.json` until it is stored. `ownsolid-install` writes it when nothing answers, and
control reads it before serving agents, which holds the fleet back at the moment control returns
([ADR 0044](../decisions/0044-a-shed-level-outlives-control.md)).

**The console for the ordinary case.** `GET` and `PUT /api/v1/admin/agent-shed` on the agent pacing page,
with the same permission and audit as pacing, and typed confirmation for `heartbeats`. The Nodes page
says so while a level is in force, because every node turning offline reads as an outage.

## Rejected

- **A separate TCP listener for operators.** Browser origins are exact (ADR 0027): a second port is a
  second origin, a second certificate name and a second CSRF scope. A socket needs none of them, and
  the console stays reachable through the reserve for everything short of a full connection table.
- **A second database pool.** Every module holds one pool; a reserve in admission gets the same
  guarantee with one number.
- **Closing agent connections when the table is full.** Agents would reconnect at once and spend TLS
  handshakes, the most expensive thing they can do. Refusing with a retry hint slows them.
- **Automatic shedding.** Admission already sheds under load, by priority. This switch is the
  operator's decision; automatic remediation belongs to later phases.

## Licences

No new dependency. Go's `net` Unix listener is standard library.

## Failure modes

| Failure | Coverage |
| --- | --- |
| The level cannot be stored because the database is down | Takes effect in memory, is retried, and the reply says it is not yet stored |
| Control stops with its database, and a restart would let the fleet in | The held file carries the level across; `TestBreakGlass` stops PostgreSQL and counts agents heard after it returns |
| The held file is damaged | Logged and ignored; the stored level stays in force |
| Shedding hides a node going offline | The console says what the level does before it is set; connectivity still changes |
| Someone other than the operator reaches the socket | Mode 0600 in a directory only the install owner can enter; refused otherwise at startup |
| Agents all return when the level is lifted | Retry hints carry jitter, and the reconnect test covers a full fleet |
| The reserve starves agents on a tiny pool | Validation refuses a reserve that leaves no connection for agents |

## What the finished item must do

1. With agents holding every seat, a sign-in and a console read succeed. Measured in `TestBreakGlass`,
   with the fleet of `TestFleetLoad`.
2. From the console, a level is set, audited, shown, and agents' calls of that priority are refused
   with a hint; lifting it restores them.
3. With control's connection limit full, `ownsolid-install agents shed` takes effect at once. With the
   database stopped it waits on the host, and control starts with it when the database returns.
4. A level survives a control restart when it was stored.
5. Works offline, in both languages, with permission enforced on the server.
