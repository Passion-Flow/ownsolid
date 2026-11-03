# Admitted middleware maintenance

This module owns configured maintenance instances and durable operations for the selected deployment profile. It exposes bounded operations, not arbitrary shell execution or a second database administration authority.

[service.go](service.go) admits choices, registration, updates, enqueue and history. [configuration.go](configuration.go) binds typed policy and the selected instance profile. [worker.go](worker.go) and [execution.go](execution.go) execute the existing offline maintenance implementation; intent, integrity, restore and repository paths preserve the exact operation and result. The `projectone/offlineoperator/pkg/maintenance` dependency under tools/deploy is runtime code.

Migration 000026 and the local [schema fragments](schema) retain instance and operation state in both database dialects. Backup creation, independent source verification and an isolated restore are distinct stages. A receipt describing a verified backup that has not been restored must keep that meaning. Failed or uncertain operations reconcile using their original ID/profile fingerprint; they must not silently restart as a fresh successful operation.

The control root loads MiddlewareConfigFile and MiddlewareEnvironmentFile; [middleware examples](../../../../../config/examples/middleware) document the owning settings. Synchronize [M26](../../../../../docs/milestones/m26-middleware), [frontend](../../../../../apps/web/packages/middleware), HTTP contracts, offline operator contracts and [quality harnesses](../../../../../tools/quality/middleware). Every owned worker must join shutdown and preserve recovery evidence before disposable cleanup.

Backup execution, independent source inspection before restore, and persisted
backup receipt selection share the same stage admission in [execution.go](execution.go).
They require the original fixed command, complete nonpending backup/inventory
status, bound manifest, valid source/plan and original-result digests. Independent
inspection retains its inventory-only qualification. A restore reservation is an
intent with later execution pending; its pending flag is not a completed restore.

Focused Go unit checks: `go -C tools/deploy/offline test ./pkg/maintenance` and `go -C services/control test ./internal/modules/middleware`. Real process, selected SQL, native TLS, backup and independent restore profiles are separate checks. The existence of a receipt parser is not proof that an actual restore completed.

[postgres_observation.go](postgres_observation.go) validates all seventeen required
PostgreSQL health facts on persisted readback. Explicit false booleans and a zero
table count remain valid; missing, null, mixed-type, duplicate and private fields
refuse with zero observation. Only the optional observed platform tenant may be
absent. The probe uses the same value validation before publishing evidence.

[mysql.go](mysql.go) adds external MySQL 8.4.x health through the existing mysqlstore
TLS/session owner. Protected nested inputs and expected server UUID bind each
registration; the worker persists distinct MySQL observation facts and closes
the endpoint pool. External profiles cannot acquire backup/restore authority.
The [MySQL health contract](../../../../../docs/milestones/m26-middleware/mysql-health.md)
lists source-level controls, pending shared client generation and unexecuted
endpoint/workflow gates. This path does not activate MySQL control-store DDL.

Restore-source evidence keeps the original offline owner's signed release
`profile` through the maintenance projection and stored JSON readback. Its
presence identifies the source selection; the unchanged inventory-only status
still establishes no native restore, registry payload or runtime readiness.
The final MVP native proof/command bridge remains with the offline scope owner.

[native_evidence.go](native_evidence.go) binds final MVP backup/source evidence
to the same original typed native reference and separately verified signed source
manifest. Candidate, wrapper inventory and signed-source digests retain distinct
meanings. Schema29 metadata without a canonical reference cannot fall back to a
legacy receipt. The final profile requires retained registry file proof alongside
the four domain stores. Scope action/readonly-stage integration is still pending;
MVP restore refuses before the legacy SQL/database/store execution loop. This
consumer check does not reverify native bytes or confer signature authority.
