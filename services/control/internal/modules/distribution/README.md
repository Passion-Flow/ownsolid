# Gateway, aliases and API admission

Distribution owns gateway identity, Workspace API keys, model aliases and target history, request admission/outcomes and gateway recovery. Rust forwards model traffic but calls this owner for current business authority.

[authorization.go](authorization.go), [keys.go](keys.go) and [aliases.go](aliases.go) are the current access surface. [outcomes.go](outcomes.go), [reconciliation.go](reconciliation.go) and [recovery.go](recovery.go) bind reported outcomes. [cutovers.go](cutovers.go) and target_history.go retain model target changes. [monitoring.go](monitoring.go) projects existing observations without becoming a new traffic owner.

Migration 000007 holds gateways, aliases/history, API keys, requests and recovery records; lifecycle migrations extend target versions/intents. Keep request/gateway identity, canonical outcome digest and current alias/model target consistent. A response stream is not proof that accounting was acknowledged; revoked keys, stale alias targets, duplicate outcomes and fenced recovery stay explicit.

Synchronize [Rust gateway](../../../../gateway/README.md), contracts/gateway and HTTP/public gateway generators, nodes/deployments/modelpreparations, [distribution frontend](../../../../../web/README.md), [M07](../../../../../docs/milestones/m07-gateway) and gateway deployment/config.

Focused native check: go -C services/control test ./internal/modules/distribution. Actual admission, SQL concurrency/cutover and Rust/backend streaming faults are separate profiles; neither a synthetic gateway nor an alias row establishes real model serving.

New gateway identities select `Config.MachineOrigin()` only after the existing
idempotency replay branch. The explicit machine HTTPS origin, or the retained
shared secure Admin origin, supplies `control_origin_sha256` for both SQL
dialects. Browser-only HTTP has no usable machine origin and refuses a new
identity with `CONFIG_MACHINE_TRANSPORT_REQUIRED`, without a credential, row or
audit publication. This is the only configuration code passed through the
repository error boundary; unrelated storage diagnostics remain redacted.

An existing intent keeps its original hash and never returns its one-time secret
again. Recovery continues comparing a fence with the row's stored hash, even
after browser/machine configuration changes. No migration, backfill or receipt
rewrite is part of this selection change. Follow the
[machine-ingress contract](../../../../../docs/engineering/deployment-transport/machine-ingress.md):
drain and reconcile the old scope, retain/fence/revoke it, and provision a new
identity for a changed machine origin. Agent identity and Rust spool ownership
must not be edited in place.

`TestGatewayMachineOriginSelectionPreservesAdmissionOrder` checks refusal before
configuration access without a database or test double.
`TestDistributionMachineOriginBindingSurvivesTransportChange` requires both
`repository_integration` and `machine_ingress_integration`, with the existing
explicit isolated repository fixture for the selected PostgreSQL/MySQL engine.
It creates one fixture and exercises real SQL, sessions, signed synthetic License,
atomic provisioning, exact replay, historical fence and canonical outcome
readback. Its node/GPU/model facts and operator fence are explicitly synthetic;
it does not execute a gateway, inference engine or machine TLS listener. Source
presence is not an execution result; retain the selected engine's actual receipt
and leave unavailable engine/target qualification pending.

The native DM first chain adds explicit gateway/alias/key/admission/outcome/recovery SQL and exact native history cursors in [native_sql.go](native_sql.go). The [owner contract](../models/native-chain.md) records shared schema/session dependencies and acceptance. Native cutovers, rollback, target-history listing and monitoring remain outside this slice. No target database or real inference result is claimed.

The native schema source for this first path is the single `models_v2` contribution in `platform/database/dameng/migrate/contribution_models.go`. Central registration, shared prerequisites and fresh/upgrade DM execution remain separate gates; approved-base/publication-disable restrictions are deliberate.

The explicit cumulative `gateway` profile composes this owner with M01-M06 and entry12 rather than replaying platform000007. Its route allowlists exclude later cutovers/targets and its catalog constrains snapshots to approved-base publish/disable. Restored authority invalidation participates in the minimal coordinated transaction; unknown capacity remains held.
