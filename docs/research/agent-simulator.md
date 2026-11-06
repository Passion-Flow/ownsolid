# Agent simulator

Research behind ARC-014: a process that stands in for thousands of node agents, speaks the
real agent protocol to a real control plane, and scripts the faults that matter at fleet
scale. ARC-015 uses it for the P0 load test and ARC-105, ARC-203 and ARC-304 for the later
ones.

## How others solve it

**Kubernetes kubemark** runs "hollow" nodes: the real kubelet and proxy code "with all
'moving' parts mocked out". A `HollowKubelet` "pretends to be an ordinary Kubelet, but does
not start anything, nor mount any volumes". Each hollow node is a pod of its own on a
second cluster, budgeted at 0.09 CPU and 220 MB, "allowing ~10 HollowNodes to run per
core". It has no scripted faults.
([Kubemark guide](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-scalability/kubemark-guide.md))

**KWOK** ("Kubernetes WithOut Kubelet") runs no node code at all: it "simply simulates the
behavior of the node" through the API, and "can reliably maintain 1k nodes and 100k pods
easily". Lifecycles and faults are scripted as `Stage` resources with a selector, a delay
with jitter, a weight for random choice and a next state, so a fault applies to a chosen
share of objects after a chosen time.
([KWOK](https://kwok.sigs.k8s.io/), [Stages](https://kwok.sigs.k8s.io/docs/user/stages-configuration/))

**Salt** keeps `tests/minionswarm.py`, which "will start a group of salt minions with
different ids on a single system to test scale capabilities": real minion processes, one
per simulated node, with randomised grains.
([minionswarm.py](https://github.com/saltstack/salt/blob/master/tests/minionswarm.py))

Two designs, then. Real agent code with its effects mocked (kubemark, minionswarm) proves
the agent's own logic but costs a process or a pod per node. A protocol-level simulator
(KWOK) reaches thousands of nodes per process and scripts faults freely, but can drift from
the real agent. None of these projects is used or copied; they are references only.

## What the ownsolid agent does on the wire

Read from `services/agent/internal/runtime` and `services/agent/internal/client` at commit
62825ed, and from the contract in `api/agent/v1`:

- **Enrollment:** `POST /agent/v1/enroll` with a single-use join token and the handshake.
  The answer carries node ID, scope, a 32-byte credential and the heartbeat interval.
- **One cycle per interval:** a heartbeat, then a claim that control may hold for
  `Pacing.ClaimWaitSeconds()`; a lease is acknowledged before anything runs, the cursor is
  the highest acknowledged sequence, and a resync answer replaces the cursor (ADR 0011).
- **Transport:** one gRPC stream (`/ownsolid.agent.v1.AgentSession/Connect`, JSON codec,
  bearer credential and node, site and cell in metadata) carries heartbeats, claims and
  acknowledgements. When the stream cannot be opened the agent falls back to HTTPS with a
  long-polled claim for `SessionFallbackRetrySeconds`. A stream idle longer than the
  session idle time is replaced, not tried (ADR 0007).
- **Task traffic over HTTPS:** progress reports (`task-progress`) and results. A
  `collect_facts.v1` task, the one task a `read_only` node runs, reports three steps and
  posts a result with fresh facts.
- **Pacing:** every disposition carries the pacing. The wait after a good cycle is a jittered
  interval minus the cycle's length; after a failed one it is full-jitter backoff, and at
  least control's retry hint spread by half (ADR 0013, ADR 0018). A 401 ends the agent.

## Chosen approach

A protocol-level simulator in `tools/dev/simulator`, in the KWOK style, built on the
contract package rather than on the agent's code:

- **Shared contract, not shared client.** Every message type, the session codec, the
  validators, the call names and the pacing arithmetic come from `api/agent/v1`. The
  full-jitter backoff, the jittered interval and the spread above a retry hint move there
  from the agent, so both pace identically by construction. The agent's client stays
  internal: it is built around one node, a state directory and real executors, and a
  goroutine-per-agent simulator would only use its transport.
- **One goroutine per agent, one connection per agent by default.** Each simulated agent has
  its own HTTP/2 connection for the session, as a real node does, because the control
  plane's connection budget and per-connection costs are part of what a load test
  measures. `connections` can multiplex several agents on one connection when a run needs
  more agents than the machine has sockets. HTTPS task traffic shares one bounded pool.
- **Identities persist.** `enroll` turns a file of join tokens into an identities file
  (mode 0600) with each node's credential and cursor. `run` loads it and writes the cursors
  back at the end, so a second run continues the same fleet without a resync storm, and a
  simulator restart is itself a fleet restart.
- **Scripted faults**, each at a time, for a percentage of agents chosen at random, some for
  a duration:

  | Fault | What the chosen agents do |
  | --- | --- |
  | `disconnect` | Close their connections at once; they reconnect on their own backoff, as after a gateway restart |
  | `restart` | Close their connections and start a new cycle at once, without backoff, as after a mass agent restart |
  | `partition` | Reach nothing for the duration, then all come back at once |
  | `https_only` | Cannot open the stream for the duration, so they fall back to long-polling |
  | `lose_replies` | Lose control's reply to acknowledgements and results for the duration, so delivery must be idempotent |
  | `lose_cursor` | Forget their task cursor, so control must answer with a rate-limited resync |

- **Measured, not asserted.** The simulator writes a JSON report: per call, successes,
  failures by code and latency percentiles; tasks run; resyncs; and, for every fault, how
  many agents it hit and how long until every one of them had a good heartbeat again. Load
  tests judge the report; the simulator decides nothing.

### Rejected

- **Many real agent processes** (minionswarm). Each needs its own state directory, lock and
  process; 2,000 of them cost far more memory than 2,000 goroutines and cannot script faults
  across the fleet at one instant.
- **Moving the agent's client out of `internal`** for the simulator to import. It depends on
  the agent's settings, component downloads and executors; exporting it would freeze an
  internal API for a test tool.
- **Seeding nodes straight into the database.** It skips enrollment, the credential and the
  admission path, which are part of what has to hold at scale.

## Licences

The simulator uses the Go standard library, `google.golang.org/grpc` (Apache-2.0, already a
dependency of the agent and control) and the repository's own `api` module. Nothing is
downloaded at run time; it runs offline. kubemark and KWOK (Apache-2.0) and Salt
(Apache-2.0) were read, not used.

## What a run needs from the control plane

Found while reading the limits a fleet meets, all per control process:

- `http.max_connections` (default 128, at most 4,096) bounds open connections, and every
  agent holds one. A default install therefore serves about a hundred agents, matching
  `nodes.maximum_live_nodes` (default 100, at most 10,000). Beyond 4,096 agents per process
  needs the gateway tier of ARC-102.
- `agent_admission.address` (default 1,200 calls a minute) limits one client address. All
  simulated agents share the simulator's address, so a run raises it; real nodes each have
  their own.
- `rates.agent_enroll_ip_count` (default 20 a minute) and `nodes.maximum_live_join_tokens`
  pace enrollment from one address; the simulator obeys the retry hint.

## Failure modes and how the tests cover them

| Failure | Test |
| --- | --- |
| The simulator paces differently from the agent | Pacing arithmetic lives once in `api/agent/v1` with its tests; the agent and the simulator call it |
| A stream that cannot open stops an agent | Unit test against a fake control that refuses the stream: the agent falls back to HTTPS and runs a task |
| Retry hints ignored, causing a retry storm | Unit test: refused heartbeats are spaced by at least the hint |
| A lost reply loses a task | Unit test and end-to-end run with `lose_replies`: every task still succeeds once |
| A resync answer is mishandled | Unit test: a lost cursor is replaced by control's |
| Reconnect time misreported | Unit test of the measurement; end-to-end run with `disconnect` and `partition` checks every hit agent came back |
| The simulator fails at fleet size | End-to-end run against a real offline install with the agent count at two thousand |

## What the finished item does

- `simulator enroll --config <file>` enrolls one agent per join token, with bounded
  concurrency and retry hints obeyed, and writes the identities file.
- `simulator run --config <file>` runs the configured number of agents for the configured
  time over the real session stream, with HTTPS fallback, executes `collect_facts.v1` tasks
  with progress and results, injects the scripted faults, logs a summary at an interval,
  writes the report and the cursors, and stops cleanly on SIGINT or SIGTERM.
- An end-to-end test enrolls a simulated fleet into a real offline install, runs tasks on
  it through the administrator API across `disconnect`, `partition`, `lose_replies` and
  `lose_cursor` faults, and checks that every task succeeded, every hit agent reconnected
  and control lists every node online.
- One process ran two thousand agents against a real install.

## Open questions

- Simulated nodes run only the `read_only` profile. Training, inference and component
  traffic (renewals, transfers, events) needs executors that pretend to run work; ARC-015
  decides whether the P0 load needs them.
- Real nodes come from distinct addresses. A run from one host shares one; per-address
  limits are raised for the run rather than spoofed.
