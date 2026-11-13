# Managed nodes and task leases

Nodes owns enrollment, join-token writes, node identity/facts/heartbeat, execution profiles, node-task leases and revocation/reconciliation. Workload owners borrow this authority; they do not manufacture their own valid node lease.

Start at [enrollment.go](enrollment.go), [join_writes.go](join_writes.go), [tasks.go](tasks.go) and the task runtime/execution repositories. [machine.go](machine.go) and workload-specific training/model-lifecycle/inference files bind admitted operations to the owning domain. Registry and task-event hooks are explicit integrations.

Migration 000003 stores node/join-token/task state. Each operation retains installed tenant/Workspace/node/task/attempt identity, current profile and credential state. A node observation is not successful work; stale lease, revoked credentials, heartbeat loss or unacknowledged result requires the existing reconciliation/fencing behavior.

[claim_wait.go](claim_wait.go) lets a claim wait for up to the longest heartbeat interval: it watches the node, claims, and claims again when a task insert for that node announces itself. The agent session stream and the HTTPS long-poll both use it ([ADR 0007](../../../../../docs/decisions/0007-agent-session-protocol.md)); [http/session_backend.go](http/session_backend.go) gives the stream gateway the same admission and node calls as the HTTPS paths. Each look for work takes an agent seat; a waiting claim holds none ([ADR 0018](../../../../../docs/decisions/0018-agent-call-admission.md)). The wake is local to this process until ARC-011.

[observed_inventory.go](observed_inventory.go) reads stored host and NVIDIA observations under
node detail's session and workspace fences. Site and cell come from the scoped node row. Missing
observations stay unknown, a measured empty GPU report stays empty, and a filesystem sample describes
the configured path. See [fleet inventory](../../../../../docs/research/fleet-inventory.md) for the
physical placement and UI handoff.

[task_progress.go](task_progress.go) stores what a running task reports under its lease, and [task_reads.go](task_reads.go) gives administrators tasks across every node with their steps and log ([ADR 0030](../../../../../docs/decisions/0030-task-progress.md)).

[task_wakes.go](task_wakes.go) delivers those wakes through the [outbox](../../platform/outbox/outbox.go) ([ADR 0028](../../../../../docs/decisions/0028-task-wakes-through-an-outbox.md)): writing a task appends an event in its transaction, and `RunWakes` reads events and wakes the claims this process holds, so a task written on one gateway wakes a claim held on another. MySQL and Dameng wake on one process only.

[task_delivery.go](task_delivery.go) owns task delivery ([ADR 0011](../../../../../docs/decisions/0011-task-delivery-semantics.md)): leases carry the per-node sequence the insert trigger assigns, `Acknowledge` records that the agent took a lease, every claim first releases a lease that was never acknowledged without spending an attempt, and a claim whose cursor disagrees with the node's record is answered with a resync, bounded per node and across the fleet.

Agent calls lock only what belongs to their node ([ADR 0020](../../../../../docs/decisions/0020-agent-call-cost-rule.md)): on PostgreSQL `machineWithin` takes the node row, then reads the Workspace status without a lock, and a status change locks the Workspace's nodes. Never lock a row the fleet shares on an agent path, and keep each call within its declared cost in `api/agent/v1/calls.go`; `make test-agent-cost` checks both. Enrollment reads `tenant_node_counts`, which a trigger keeps.

[liveness.go](liveness.go) keeps liveness out of the database ([ADR 0022](../../../../../docs/decisions/0022-liveness-outside-the-database.md)): a heartbeat that repeats the state last written runs one read and goes to [the table](../../platform/liveness/table.go), and `RunLiveness` writes last-seen times and the latest reports in batches and stores connectivity changes. Component and deployment modules write their own rows through `ComponentSightingRecorder` and `InferenceSightingRecorder`. `Node.Connectivity` stays derived from `last_heartbeat_at`, which now lags a heartbeat by up to `liveness_flush_seconds`; `updated_at` no longer moves with it. MySQL and Dameng keep the synchronous heartbeat.

[task_prune.go](task_prune.go) removes finished tasks from `node_tasks` after `retention.task_days`, in batches, once they have a history record and nothing refers to them ([ADR 0029](../../../../../docs/decisions/0029-time-partitioned-append-tables.md)). A trigger writes that record to `node_task_history`, and `Tasks` lists live tasks and the history.

[pacing.go](pacing.go) owns the pacing of this cell's agents ([ADR 0013](../../../../../docs/decisions/0013-agent-pacing.md)): the row, the administrator API, and the per-process copy heartbeat replies read.

Task-event publication is recorded through the same-transaction hook; delivery of a broker message does not itself claim a task. Preserve ownership and lock ordering when extending cross-domain callbacks.

[updates.go](updates.go) uses the selected PostgreSQL or MySQL transaction for
current-version status changes, credential revocation, task cancellation, native
workload fences and audit. Production platform composition attaches the component,
inference, training and preparation owners. The `mvp-postgresql-cpu-v1` profile
requires this same complete recovery-owner graph through `FullPlatformRuntime`;
it does not inherit the reduced owner checks of an account-only profile.
Unknown COMMIT outcomes return no
success DTO and are never replayed automatically. Inspect authoritative node,
token and task state before deliberate orphan removal and fresh enrollment;
reusing the name must not reuse the removed node's identity or credential. The
paired recovery tests attach native domain observers, but do not establish a
physical workload stop or systemd qualification.

Synchronize [agent contracts](../../../../../api/agent), [agent](../../../../agent/README.md), [fleet scope](../../../../../docs/product/roadmap/fleet-and-infrastructure.md), node policy/examples and deployment service profiles, [nodes frontend](../../../../../web/README.md), SQL schemas and workload result validators.

Focused native check: go -C services/control test ./internal/modules/nodes. Enrollment races, current lease/result authority and selected vendor persistence need real SQL tests; daemon/profile and hardware execution are distinct gates.

Machine request, transfer and enrollment IP quotas use the existing dispatcher's
validated effective client identity, preserving the actual proxy socket peer in
the correlated transport audit. A standalone internal handler can observe only
its socket peer; forwarding headers without that context never select a bucket.
The new transport SQL cases separately verify distinct proxy clients and exact
persisted quota buckets. Source selection alone does not establish that outcome.

The optional native Dameng repository path, five-table schema contribution,
transaction/type boundaries and executable target profile are documented in
[native Dameng notes](native-dameng.md). Its source is prepared; native
SQL execution and whole-platform selection remain separate integration gates.
The component native upsert owner aborts on a SQL error without reusing the
poisoned transaction. No native support is inferred from the PostgreSQL/MySQL
fixtures or an ODBC calling-layer build.

The minimal `nodes` composition constructs only Workspaces and this service.
`http/discovery_routes.go` closes human/machine routing to approved M03 operations
before optional callback dispatch. Enrollment profiles are restricted to
`read_only`; database checks independently enforce that boundary. The existing
selected repositories, heartbeat/claim/result state machine and sweep are reused.
Minimal logical restore invokes only account and node authority invalidation.

The cumulative M04 `components` composition additionally constructs only the
existing component owner and its observation/result/artifact callbacks. Exact
`systemd_components` tokens admit protocol1.1and the six approved capabilities;
read_only remains1.0. Later enrollment profiles/operations are refused, and
ComponentsHumanOwns/ComponentsMachineOwns close routing before optional dispatch.


The cumulative maintenance composition admits exactly read_only1.0, systemd_components1.1 and deliberately enrolled systemd_maintenance1.2. MaintenanceHumanOwns adds only the Admin log route; machine routes reuse the exact component allowlist. Later workload owners/profiles are structurally unavailable. Heartbeat cannot upgrade authority; disabled passive recovery remains stale and task-ineligible.


M06 cumulative integration (2026-10-26): explicit `models` extends maintenance
through entry11 and `upgrade-models`, retaining the installed prefix/principal.
Only approved immutable import, deployment/GPU ownership, exact leased file
transfer and bounded model worker are added. Later owners remain structurally
inactive. Minimal restore invalidates imports and preserves uncertain GPU leases;
see [model integration](../models/README.md).


The cumulative M10 `lifecycle` graph admits explicit docker_model_lifecycle/protocol1.5 enrollment and only its exact preparation transfer/event/output/stop leaves. Existing docker_ai1.4 nodes retain their original capability set. Earlier minimal profiles do not acquire preparation or derived inference authority.

[certificates.go](certificates.go) checks a connection's client certificate against the keys pinned to the node and issues, rotates and recovers node certificates ([ADR 0050](../../../../../docs/decisions/0050-agent-certificates.md)).

## Observation cost measurements

`TestComponentHeartbeatCostAtFleetSizes` and
`TestInferenceHeartbeatStatementCost` use scoped HTTPS and enrolled client
certificates. Both compare 50 and 3,000 seeded peer nodes, with their actual
observation, task and audit history counts checked before recording. Every
exchange runs while an independent transaction holds the shared authority
rows. The current v1 statement ceiling, equal statement counts, scanned-row
growth and missing-index checks remain enforced. Independent readback checks
stored reports, committed heartbeat times and unchanged state after denial.

`make test-agent-cost` runs these node roots before the existing training
cost root. Follow the test inputs in the
[development environment](../../../../../docs/development/environment.md).
An ordinary package run without PostgreSQL skips database cases. Seeded rows
measure query cost, and do not establish simulated-node load or hardware
support. Other component/inference exchanges, preparation and registry
coverage remain required for ARC-108. See the
[approach and acceptance scope](../../../../../docs/research/agent-operation-cost.md).

The prepared inference extension also includes a retained deployment after a
dispatcher restart and a delayed report after a desired-revision change. It seeds
peer deployments and held leases, retains the original statement/index/growth
checks and compares complete deployment/reservation rows after the stale report.
These two additional exchanges still require their allocated native replay;
their source presence does not extend the measured scope below.

The shared-fixture and inference HTTP extension passed both actual cost roots
on disconnected Linux arm64 PostgreSQL, affected race/vet and the full
repository gate at a91a817b. See the
[exact counts, source and remaining coverage](../../../../../docs/research/agent-operation-cost.md#qualified-observation-coverage).
This does not qualify other operation families or concurrent fleet capacity.
