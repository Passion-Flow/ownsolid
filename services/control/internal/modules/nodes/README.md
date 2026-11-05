# Managed nodes and task leases

Nodes owns enrollment, join-token writes, node identity/facts/heartbeat, execution profiles, node-task leases and revocation/reconciliation. Workload owners borrow this authority; they do not manufacture their own valid node lease.

Start at [enrollment.go](enrollment.go), [join_writes.go](join_writes.go), [tasks.go](tasks.go) and the task runtime/execution repositories. [machine.go](machine.go) and workload-specific training/model-lifecycle/inference files bind admitted operations to the owning domain. Registry and task-event hooks are explicit integrations.

Migration 000003 stores node/join-token/task state. Each operation retains installed tenant/Workspace/node/task/attempt identity, current profile and credential state. A node observation is not successful work; stale lease, revoked credentials, heartbeat loss or unacknowledged result requires the existing reconciliation/fencing behavior.

[claim_wait.go](claim_wait.go) lets a claim wait for up to the longest heartbeat interval: it watches the node, claims, and claims again when a task insert for that node announces itself. The agent session stream and the HTTPS long-poll both use it ([ADR 0007](../../../../../docs/decisions/0007-agent-session-protocol.md)); [http/session_backend.go](http/session_backend.go) gives the stream gateway the same admission and node calls as the HTTPS paths. Each look for work takes an agent seat; a waiting claim holds none ([ADR 0017](../../../../../docs/decisions/0017-agent-call-admission.md)). The wake is local to this process until ARC-011.

[task_delivery.go](task_delivery.go) owns task delivery ([ADR 0011](../../../../../docs/decisions/0011-task-delivery-semantics.md)): leases carry the per-node sequence the insert trigger assigns, `Acknowledge` records that the agent took a lease, every claim first releases a lease that was never acknowledged without spending an attempt, and a claim whose cursor disagrees with the node's record is answered with a resync, bounded per node and across the fleet.

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

Synchronize [contracts/agent](../../../../../contracts/agent), [agent](../../../../agent/README.md), [M03](../../../../../docs/milestones/m03-nodes), node policy/examples and deployment service profiles, [nodes frontend](../../../../../web/README.md), SQL schemas and workload result validators.

Focused native check: go -C services/control test ./internal/modules/nodes. Enrollment races, current lease/result authority and selected vendor persistence need real SQL tests; daemon/profile and hardware execution are distinct gates.

Machine request, transfer and enrollment IP quotas use the existing dispatcher's
validated effective client identity, preserving the actual proxy socket peer in
the correlated transport audit. A standalone internal handler can observe only
its socket peer; forwarding headers without that context never select a bucket.
The new transport SQL cases separately verify distinct proxy clients and exact
persisted quota buckets. Source selection alone does not establish that outcome.

The optional native Dameng repository path, five-table schema contribution,
transaction/type boundaries and executable target profile are documented in
[NATIVE_DAMENG.md](NATIVE_DAMENG.md). Its source is prepared; native
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
see [current integration](../../../../../docs/execution/mvp/m06-cumulative-integration-20261026.md).


The cumulative M10 `lifecycle` graph admits explicit docker_model_lifecycle/protocol1.5 enrollment and only its exact preparation transfer/event/output/stop leaves. Existing docker_ai1.4 nodes retain their original capability set. Earlier minimal profiles do not acquire preparation or derived inference authority.
